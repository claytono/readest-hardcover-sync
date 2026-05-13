package sync

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/claytono/readest-hardcover-sync/internal/hardcover"
	"github.com/claytono/readest-hardcover-sync/internal/identifier"
	"github.com/claytono/readest-hardcover-sync/internal/readest"
	"github.com/claytono/readest-hardcover-sync/internal/state"
)

// Engine runs the sync cycle against Readest and Hardcover.
type Engine struct {
	readest     ReadestPuller
	finder      BookFinder
	updater     ProgressUpdater // active updater (may be dry-run in manual mode)
	realUpdater ProgressUpdater // real updater, used when user triggers sync
	state       *state.State
	matcher     *Matcher
	logger      *slog.Logger
	events      *EventBus // optional; nil disables event emission
	notifier    Notifier  // optional; nil disables external notifications
	coversDir   string    // directory for cached cover images; empty disables

	mu              sync.Mutex // prevents concurrent Tick() calls
	syncing         bool
	pendingFullSync bool // reset timestamps at start of next Tick

	notifyMu                 sync.Mutex
	criticalNotificationDays map[string]string
	now                      func() time.Time

	manualSync bool // true = dry-run updater for polling, real updater for SyncNow

	minSyncPercent float64 // minimum progress % before syncing as "currently reading"
	minSyncPages   int     // minimum Readest pages before syncing as "currently reading"

	privacySettingID int            // fetched from Hardcover on first sync
	statusNames      map[int]string // fetched from Hardcover on first sync
}

// EngineOptions holds optional dependencies for NewEngine.
type EngineOptions struct {
	Events         *EventBus
	Notifier       Notifier
	CoversDir      string
	MinSyncPercent float64 // minimum progress % before syncing (0 = no threshold)
	MinSyncPages   int     // minimum Readest pages before syncing (0 = no threshold)
}

type bookAddedNotification struct {
	bookHash   string
	autoLinked bool
}

type bookCompletedNotification struct {
	book state.BookState
}

type bookUnlinkedReadingNotification struct {
	bookHash string
}

type bookProcessNotifications struct {
	added           *bookAddedNotification
	unlinkedReading *bookUnlinkedReadingNotification
}

type configProcessNotifications struct {
	completed       *bookCompletedNotification
	unlinkedReading *bookUnlinkedReadingNotification
}

// NewEngine constructs an Engine with the given dependencies.
// If manualSync is true, the polling loop uses a dry-run updater (reads only),
// and SyncNow() must be called to push changes to Hardcover.
func NewEngine(readest ReadestPuller, finder BookFinder, updater ProgressUpdater,
	st *state.State, matcher *Matcher, logger *slog.Logger, manualSync bool, opts *EngineOptions) *Engine {
	e := &Engine{
		readest:     readest,
		finder:      finder,
		updater:     updater,
		realUpdater: updater,
		state:       st,
		matcher:     matcher,
		logger:      logger,
		now:         time.Now,
	}
	if opts != nil {
		e.events = opts.Events
		e.notifier = opts.Notifier
		e.coversDir = opts.CoversDir
		e.minSyncPercent = opts.MinSyncPercent
		e.minSyncPages = opts.MinSyncPages
	}
	if manualSync {
		e.manualSync = true
		e.updater = NewDryRunUpdater(updater, logger)
	}
	return e
}

// Run is a polling loop that calls Tick immediately, then on each interval tick.
// It blocks until ctx is cancelled.
func (e *Engine) Run(ctx context.Context, interval time.Duration) {
	if err := e.Tick(ctx); err != nil {
		e.logger.Error("initial sync tick failed", "error", err)
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := e.Tick(ctx); err != nil {
				e.logger.Error("sync tick failed", "error", err)
			}
		}
	}
}

// SyncNow runs a tick with the real updater, pushing changes to Hardcover.
// Use this for manual sync mode where polling uses the dry-run updater.
func (e *Engine) SyncNow(ctx context.Context) error {
	err := e.tick(ctx, e.realUpdater)
	e.notifyCriticalSyncError(ctx, err)
	return err
}

// FullSync requests a full re-pull of all books and configs from Readest.
// Timestamps are reset at the start of the next Tick under the sync mutex.
// Uses the real updater regardless of manual mode.
func (e *Engine) FullSync(ctx context.Context) error {
	e.mu.Lock()
	e.pendingFullSync = true
	e.mu.Unlock()
	return e.SyncNow(ctx)
}

// Tick performs a single sync cycle. Returns immediately if a sync is already in progress.
func (e *Engine) Tick(ctx context.Context) error {
	err := e.tick(ctx, e.updater)
	e.notifyCriticalSyncError(ctx, err)
	return err
}

func (e *Engine) tick(ctx context.Context, updater ProgressUpdater) error {
	e.mu.Lock()
	if e.syncing {
		e.mu.Unlock()
		e.logger.Info("sync already in progress, skipping tick")
		return nil
	}
	e.syncing = true
	if e.pendingFullSync {
		e.state.ResetSyncTimestamps()
		e.pendingFullSync = false
	}
	e.mu.Unlock()

	released := false
	releaseSync := func() {
		e.mu.Lock()
		e.syncing = false
		e.mu.Unlock()
		released = true
	}
	defer func() {
		if !released {
			releaseSync()
		}
	}()

	// Step 1: Cache privacy_setting_id and status names from Hardcover on first call.
	if e.privacySettingID == 0 {
		me, err := updater.GetMe(ctx)
		if err != nil {
			return err
		}
		if me != nil {
			e.privacySettingID = me.AccountPrivacySettingID
		}

		statuses, err := updater.GetStatuses(ctx)
		if err != nil {
			return err
		}
		e.statusNames = make(map[int]string, len(statuses))
		for _, s := range statuses {
			e.statusNames[s.ID] = s.Status
		}
	}

	// Log current state on startup.
	allBooks := e.state.ListBooks()
	var matchedCount, unmatchedCount int
	for _, b := range allBooks {
		if b.HardcoverBookID != 0 {
			matchedCount++
		} else if b.Unmatched {
			unmatchedCount++
		}
	}
	e.logger.Info("starting sync tick",
		"books_in_state", len(allBooks),
		"matched", matchedCount,
		"unmatched", unmatchedCount,
	)
	if e.events != nil {
		e.events.ClearHistory()
	}
	e.emit(SyncEvent{Type: "sync_start", Detail: fmt.Sprintf("%d books in state", len(allBooks))})

	// Step 2: Pull books since last sync.
	books, err := e.readest.PullBooks(ctx, e.state.GetLastBookSync())
	if err != nil {
		return err
	}

	var maxBookUpdatedAt int64
	var newBooks, updatedBooks int
	var pendingBookNotifications []bookAddedNotification
	var pendingCompletionNotifications []bookCompletedNotification
	var pendingUnlinkedReadingNotifications []bookUnlinkedReadingNotification
	for _, book := range books {
		if book.DeletedAt != nil {
			continue
		}
		ts := parseTimestamp(book.UpdatedAt)
		if ts > maxBookUpdatedAt {
			maxBookUpdatedAt = ts
		}
		_, exists := e.state.GetBook(book.BookHash)
		notifications, err := e.processBook(ctx, book)
		if err != nil {
			e.logger.Error("failed to process book", "book_hash", book.BookHash, "title", book.Title, "error", err)
			e.emit(SyncEvent{Type: "error", Title: book.Title, Detail: err.Error()})
		} else {
			if notifications.added != nil {
				pendingBookNotifications = append(pendingBookNotifications, *notifications.added)
			}
			if notifications.unlinkedReading != nil {
				pendingUnlinkedReadingNotifications = append(pendingUnlinkedReadingNotifications, *notifications.unlinkedReading)
			}
		}
		if exists {
			updatedBooks++
		} else {
			newBooks++
		}
	}

	// Step 3: Pull configs since last sync.
	configs, err := e.readest.PullConfigs(ctx, e.state.GetLastConfigSync())
	if err != nil {
		return err
	}

	var maxConfigUpdatedAt int64
	for _, cfg := range configs {
		if cfg.DeletedAt != nil {
			continue
		}
		ts := parseTimestamp(cfg.UpdatedAt)
		if ts > maxConfigUpdatedAt {
			maxConfigUpdatedAt = ts
		}
		notifications, err := e.processConfig(ctx, cfg, updater)
		if err != nil {
			e.logger.Error("failed to process config", "book_hash", cfg.BookHash, "error", err)
			e.emit(SyncEvent{Type: "error", Detail: err.Error()})
		} else {
			if notifications.completed != nil {
				pendingCompletionNotifications = append(pendingCompletionNotifications, *notifications.completed)
			}
			if notifications.unlinkedReading != nil {
				pendingUnlinkedReadingNotifications = append(pendingUnlinkedReadingNotifications, *notifications.unlinkedReading)
			}
		}
	}

	// Step 4: Sync matched books that have stored progress but haven't pushed to Hardcover yet.
	// This covers books that were manually linked after their config was already processed.
	for _, bs := range e.state.ListBooks() {
		if bs.HardcoverBookID == 0 {
			continue
		}
		if bs.LastStatusSent != hardcover.StatusNone || bs.LastProgressSent != 0 {
			continue
		}
		if bs.ReadestProgress[0] == 0 && bs.ReadestProgress[1] == 0 && bs.ReadestStatus == "" {
			continue
		}
		// Synthesize a config from stored state to push to Hardcover.
		synthCfg := readest.DBBookConfig{
			BookHash: bs.BookHash,
			Progress: fmt.Sprintf("[%d, %d]", bs.ReadestProgress[0], bs.ReadestProgress[1]),
		}
		notifications, err := e.processConfig(ctx, synthCfg, updater)
		if err != nil {
			e.logger.Error("failed to sync pending progress", "book_hash", bs.BookHash, "title", bs.Title, "error", err)
		} else {
			if notifications.completed != nil {
				pendingCompletionNotifications = append(pendingCompletionNotifications, *notifications.completed)
			}
			if notifications.unlinkedReading != nil {
				pendingUnlinkedReadingNotifications = append(pendingUnlinkedReadingNotifications, *notifications.unlinkedReading)
			}
		}
	}

	// Step 5: Retry pending unlinked-reading warnings until one is delivered.
	for _, bs := range e.state.ListBooks() {
		if unlinkedReadingNotificationReady(bs, e.minSyncPercent, e.minSyncPages) {
			pendingUnlinkedReadingNotifications = append(pendingUnlinkedReadingNotifications, bookUnlinkedReadingNotification{bookHash: bs.BookHash})
		}
	}

	// Step 6: Download missing covers for matched books.
	if e.coversDir != "" {
		for _, bs := range e.state.ListBooks() {
			if bs.HardcoverBookID == 0 || bs.HardcoverSlug == "" {
				continue
			}
			if bs.CoverPath != "" && bs.CoverURL != "" {
				continue
			}
			// Look up the book to get the cover URL.
			book, err := e.finder.FindBookBySlug(ctx, bs.HardcoverSlug)
			if err != nil {
				e.logger.Error("failed to look up book for cover", "slug", bs.HardcoverSlug, "error", err)
				continue
			}
			if book == nil {
				continue
			}
			newSeries := ""
			if bs.Series == "" && book.SeriesName() != "" {
				newSeries = book.SeriesName()
			}
			newCoverURL := ""
			if bs.CoverURL == "" && book.CoverURL() != "" {
				newCoverURL = book.CoverURL()
			}
			newCoverPath := ""
			if bs.CoverPath == "" && book.CoverURL() != "" {
				coverPath, err := DownloadCover(e.coversDir, bs.BookHash, book.CoverURL())
				if err != nil {
					e.logger.Error("failed to download cover", "title", bs.Title, "error", err)
				} else if coverPath != "" {
					newCoverPath = coverPath
				}
			}
			if newSeries != "" || newCoverURL != "" || newCoverPath != "" {
				e.state.UpdateBook(bs.BookHash, func(b *state.BookState) {
					if newSeries != "" {
						b.Series = newSeries
					}
					if newCoverURL != "" {
						b.CoverURL = newCoverURL
					}
					if newCoverPath != "" {
						b.CoverPath = newCoverPath
					}
				})
			}
		}
	}

	// Step 7: Update state timestamps from max updated_at of returned records.
	if maxBookUpdatedAt > e.state.GetLastBookSync() {
		e.state.SetLastBookSync(maxBookUpdatedAt)
	}
	if maxConfigUpdatedAt > e.state.GetLastConfigSync() {
		e.state.SetLastConfigSync(maxConfigUpdatedAt)
	}

	// Step 8: Save state.
	// Recount after processing.
	allBooks = e.state.ListBooks()
	matchedCount = 0
	unmatchedCount = 0
	for _, b := range allBooks {
		if b.HardcoverBookID != 0 {
			matchedCount++
		} else {
			unmatchedCount++
		}
	}
	e.logger.Info("sync tick complete",
		"new_books", newBooks,
		"updated_books", updatedBooks,
		"configs_processed", len(configs),
	)
	e.state.SetLastSyncRanAt(time.Now())
	e.emit(SyncEvent{Type: "sync_complete", Detail: fmt.Sprintf("%d matched, %d unmatched", matchedCount, unmatchedCount)})
	if err := e.state.Save(); err != nil {
		return fmt.Errorf("save sync state: %w", err)
	}
	releaseSync()
	addedNotificationHashes := make(map[string]struct{}, len(pendingBookNotifications))
	for _, notification := range pendingBookNotifications {
		addedNotificationHashes[notification.bookHash] = struct{}{}
		book, ok := e.state.GetBook(notification.bookHash)
		if !ok {
			continue
		}
		if e.notifyBookAdded(ctx, book, notification.autoLinked) && book.UnlinkedReadingNotificationPending {
			e.markUnlinkedReadingNotificationSent(notification.bookHash)
		}
	}
	for _, notification := range pendingCompletionNotifications {
		e.notifyBookCompleted(ctx, notification.book)
	}
	attemptedUnlinkedReadingNotifications := make(map[string]struct{}, len(pendingUnlinkedReadingNotifications))
	for _, notification := range pendingUnlinkedReadingNotifications {
		if _, alreadyNotifiedAsAdded := addedNotificationHashes[notification.bookHash]; alreadyNotifiedAsAdded {
			continue
		}
		if _, alreadyAttempted := attemptedUnlinkedReadingNotifications[notification.bookHash]; alreadyAttempted {
			continue
		}
		attemptedUnlinkedReadingNotifications[notification.bookHash] = struct{}{}
		book, ok := e.state.GetBook(notification.bookHash)
		if !ok {
			continue
		}
		if !unlinkedReadingNotificationReady(book, e.minSyncPercent, e.minSyncPages) {
			continue
		}
		if e.notifyBookStartedUnlinked(ctx, book) {
			e.markUnlinkedReadingNotificationSent(notification.bookHash)
		}
	}
	return nil
}

// processBook handles a single DBBook: updates existing state or matches and creates new entry.
func (e *Engine) processBook(ctx context.Context, book readest.DBBook) (bookProcessNotifications, error) {
	// If already in state, just update ReadestStatus if changed.
	var notifications bookProcessNotifications
	if e.state.UpdateBook(book.BookHash, func(bs *state.BookState) {
		previousStatusID := DeriveStatus(bs.ReadestProgress[0], bs.ReadestProgress[1], bs.ReadestStatus, e.minSyncPercent, e.minSyncPages)
		if book.ReadingStatus != "" {
			bs.ReadestStatus = book.ReadingStatus
		}
		currentStatusID := DeriveStatus(bs.ReadestProgress[0], bs.ReadestProgress[1], bs.ReadestStatus, e.minSyncPercent, e.minSyncPages)
		if updateUnlinkedReadingNotificationState(bs, previousStatusID, currentStatusID) {
			notifications.unlinkedReading = &bookUnlinkedReadingNotification{bookHash: bs.BookHash}
		}
	}) {
		return notifications, nil
	}

	// Parse identifiers from metadata, title, author.
	ids := identifier.Parse(book.Metadata, book.Title, book.Author)

	// Match via matcher.
	result, err := e.matcher.Match(ctx, ids)
	if err != nil {
		return bookProcessNotifications{}, fmt.Errorf("match book %q: %w", book.BookHash, err)
	}

	bs := state.BookState{
		BookHash:      book.BookHash,
		Title:         book.Title,
		Author:        book.Author,
		ReadestStatus: book.ReadingStatus,
		Metadata:      book.Metadata,
	}

	if result == nil {
		bs.Unmatched = true
		e.logger.Info("no match found for book", "title", book.Title, "author", book.Author)
		e.emit(SyncEvent{Type: "book_unmatched", Title: book.Title})
	} else {
		bs.HardcoverBookID = result.BookID
		bs.HardcoverSlug = result.Slug
		bs.EditionID = result.EditionID
		bs.EditionPages = result.EditionPages
		bs.ReadingFormatID = result.ReadingFormatID
		bs.MatchMethod = result.MatchMethod
		bs.CoverURL = result.CoverURL
		bs.Series = result.Series
		e.logger.Info("matched book", "title", book.Title, "slug", result.Slug, "method", result.MatchMethod)
		e.emit(SyncEvent{Type: "book_matched", Title: book.Title, Detail: "via " + result.MatchMethod})

		// Download cover image if available.
		if e.coversDir != "" && result.CoverURL != "" {
			if coverPath, err := DownloadCover(e.coversDir, book.BookHash, result.CoverURL); err != nil {
				e.logger.Error("failed to download cover", "title", book.Title, "error", err)
			} else if coverPath != "" {
				bs.CoverPath = coverPath
			}
		}
	}

	notifications = bookProcessNotifications{
		added: &bookAddedNotification{bookHash: book.BookHash, autoLinked: result != nil},
	}
	if result == nil {
		currentStatusID := DeriveStatus(bs.ReadestProgress[0], bs.ReadestProgress[1], bs.ReadestStatus, e.minSyncPercent, e.minSyncPages)
		if updateUnlinkedReadingNotificationState(&bs, hardcover.StatusNone, currentStatusID) {
			notifications.unlinkedReading = &bookUnlinkedReadingNotification{bookHash: bs.BookHash}
		}
	}
	e.state.SetBook(book.BookHash, bs)
	return notifications, nil
}

// processConfig handles a single DBBookConfig: updates progress/status on Hardcover.
func (e *Engine) processConfig(ctx context.Context, cfg readest.DBBookConfig, updater ProgressUpdater) (configProcessNotifications, error) {
	// Get book from state; skip if not in state at all.
	bs, ok := e.state.GetBook(cfg.BookHash)
	if !ok {
		return configProcessNotifications{}, nil
	}

	// Parse progress.
	progress, err := readest.ParseConfigProgress(cfg.Progress)
	if err != nil {
		return configProcessNotifications{}, fmt.Errorf("parse progress for book %q: %w", cfg.BookHash, err)
	}

	current, total := progress[0], progress[1]
	previousStatusID := DeriveStatus(bs.ReadestProgress[0], bs.ReadestProgress[1], bs.ReadestStatus, e.minSyncPercent, e.minSyncPages)

	// Always store Readest progress so it's available when the book gets linked later.
	bs.ReadestProgress = [2]int{current, total}

	// Track activity time for sort ordering.
	if ts := parseTimestamp(cfg.UpdatedAt); ts > bs.LastActivityAt {
		bs.LastActivityAt = ts
	}

	// DeriveStatus; skip if no status update needed.
	statusID := DeriveStatus(current, total, bs.ReadestStatus, e.minSyncPercent, e.minSyncPages)

	// If not matched yet, save progress and warn once when reading starts.
	if bs.HardcoverBookID == 0 {
		shouldNotify := updateUnlinkedReadingNotificationState(&bs, previousStatusID, statusID)
		e.state.SetBook(cfg.BookHash, bs)
		if !shouldNotify {
			return configProcessNotifications{}, nil
		}
		return configProcessNotifications{
			unlinkedReading: &bookUnlinkedReadingNotification{bookHash: bs.BookHash},
		}, nil
	}
	bs.UnlinkedReadingNotificationPending = false

	if statusID == hardcover.StatusNone {
		e.state.SetBook(cfg.BookHash, bs)
		return configProcessNotifications{}, nil
	}

	// ConvertProgress (skip progress update if EditionPages == 0).
	var hardcoverPages int
	if bs.EditionPages > 0 {
		hardcoverPages = ConvertProgress(current, total, bs.EditionPages)
	}

	var pct int
	if total > 0 {
		pct = current * 100 / total
	}

	var editionIDPtr *int
	if bs.EditionID != 0 {
		edID := bs.EditionID
		editionIDPtr = &edID
	}
	userBookID := bs.UserBookID
	lastStatusBeforeWrite := bs.LastStatusSent
	writesToHardcover := !e.manualSync || updater == e.realUpdater
	statusUpdated := false
	needsManualCompletionReconcile := writesToHardcover && e.manualSync && statusID == hardcover.StatusRead
	cachedLinkageMatchesEdition := localLinkageMatchesEdition(bs, editionIDPtr)
	needsUserBookLookup := userBookID == 0 || !cachedLinkageMatchesEdition || needsManualCompletionReconcile
	canSkipBeforeReconcile := userBookID == 0 || !needsUserBookLookup
	shouldNotifyComplete := func() bool {
		return writesToHardcover && statusUpdated && statusID == hardcover.StatusRead && lastStatusBeforeWrite != hardcover.StatusRead
	}

	if statusID == bs.LastStatusSent && hardcoverPages == bs.LastProgressSent && canSkipBeforeReconcile {
		bs.ReadestProgress = [2]int{current, total}
		e.state.SetBook(cfg.BookHash, bs)
		return configProcessNotifications{}, nil
	}

	// Ensure UserBookID exists and adopt any existing read record for the linked
	// edition. Hardcover can already have a Want to Read user_book with an
	// edition-specific read; updating that row keeps progress visible in the UI.
	userBookEditionMatches := editionIDPtr == nil || userBookID != 0
	if needsUserBookLookup {
		existing, err := updater.GetUserBook(ctx, bs.HardcoverBookID)
		if err != nil {
			return configProcessNotifications{}, fmt.Errorf("get Hardcover user book for book %q: %w", cfg.BookHash, err)
		}

		if existing != nil {
			userBookID = existing.ID
			// Sync our record of the Hardcover status so transition checks work.
			if existing.StatusID != 0 {
				bs.LastStatusSent = existing.StatusID
			}
			if existing.EditionID != nil {
				bs.UserBookEditionID = *existing.EditionID
			} else {
				bs.UserBookEditionID = hardcover.StatusNone
			}
			lastStatusBeforeWrite = bs.LastStatusSent
			userBookEditionMatches = editionIDPtr == nil || (existing.EditionID != nil && *existing.EditionID == *editionIDPtr)
			if read, ok := selectUserBookRead(existing.UserBookReads, bs.EditionID, bs.UserBookReadID); ok {
				bs.UserBookReadID = read.ID
				if read.ProgressPages != nil {
					bs.LastProgressSent = *read.ProgressPages
				} else {
					bs.LastProgressSent = 0
				}
			}
		} else {
			lastStatusBeforeWrite = hardcover.StatusNone
		}
		if existing == nil && userBookID == 0 {
			inserted, err := updater.InsertUserBook(ctx, bs.HardcoverBookID, statusID, e.privacySettingID, editionIDPtr)
			if err != nil {
				return configProcessNotifications{}, fmt.Errorf("insert Hardcover user book for book %q: %w", cfg.BookHash, err)
			}
			if inserted.ID > 0 {
				userBookID = inserted.ID
				bs.LastStatusSent = statusID
				if editionIDPtr != nil {
					bs.UserBookEditionID = *editionIDPtr
				}
				statusUpdated = true
				userBookEditionMatches = true
			}
		}
		if userBookID > 0 {
			bs.UserBookID = userBookID
		}
	}

	// Skip if nothing changed after reconciling the existing Hardcover rows.
	if statusID == bs.LastStatusSent && hardcoverPages == bs.LastProgressSent && userBookEditionMatches {
		bs.ReadestProgress = [2]int{current, total}
		e.state.SetBook(cfg.BookHash, bs)
		if shouldNotifyComplete() {
			return configProcessNotifications{completed: &bookCompletedNotification{book: bs}}, nil
		}
		return configProcessNotifications{}, nil
	}

	progressMsg := "syncing progress"
	if e.manualSync && updater != e.realUpdater {
		progressMsg = "would sync progress (manual mode)"
	}
	e.logger.Info(progressMsg,
		"title", bs.Title,
		"slug", bs.HardcoverSlug,
		"edition_id", bs.EditionID,
		"status", e.statusName(statusID),
		"progress", fmt.Sprintf("%d%% (%d/%d pages)", pct, hardcoverPages, bs.EditionPages),
	)

	// If we don't have a valid UserBookID (dry-run), skip Hardcover writes.
	if userBookID == 0 {
		e.emit(SyncEvent{Type: "progress_pending", Title: bs.Title, Detail: fmt.Sprintf("would sync %d%%", pct)})
		bs.ReadestProgress = [2]int{current, total}
		e.state.SetBook(cfg.BookHash, bs)
		return configProcessNotifications{}, nil
	}

	// Only allow forward status transitions (want-to-read → reading → read).
	// If the book is in a state like DNF, don't overwrite it.
	if statusID != bs.LastStatusSent && !IsAllowedTransition(bs.LastStatusSent, statusID) {
		e.logger.Info("skipping status update: transition not allowed",
			"title", bs.Title,
			"current_status", e.statusName(bs.LastStatusSent),
			"desired_status", e.statusName(statusID),
		)
		bs.ReadestProgress = [2]int{current, total}
		e.state.SetBook(cfg.BookHash, bs)
		return configProcessNotifications{}, nil
	}

	// Update status and linked edition if changed.
	if statusID != bs.LastStatusSent || !userBookEditionMatches {
		statusChanged := statusID != bs.LastStatusSent
		if _, err := updater.UpdateUserBook(ctx, userBookID, statusID, editionIDPtr); err != nil {
			return configProcessNotifications{}, fmt.Errorf("update Hardcover user book for book %q: %w", cfg.BookHash, err)
		}
		bs.LastStatusSent = statusID
		if editionIDPtr != nil {
			bs.UserBookEditionID = *editionIDPtr
		}
		if statusChanged {
			statusUpdated = true
		}
	}

	// Update progress.
	if hardcoverPages != bs.LastProgressSent && bs.EditionPages > 0 {
		var finishedAt *string
		if statusID == hardcover.StatusRead {
			today := time.Now().Format("2006-01-02")
			finishedAt = &today
		}

		if bs.UserBookReadID == 0 {
			// Insert new reading session.
			startedAt := time.Now().Format("2006-01-02")
			read, err := updater.InsertUserBookRead(ctx, userBookID, hardcoverPages, editionIDPtr, startedAt, finishedAt)
			if err != nil {
				return configProcessNotifications{}, fmt.Errorf("insert Hardcover user book read for book %q: %w", cfg.BookHash, err)
			}
			if read.ID > 0 {
				bs.UserBookReadID = read.ID
			}
		} else {
			// Update existing reading session.
			if _, err := updater.UpdateUserBookRead(ctx, bs.UserBookReadID, hardcoverPages, finishedAt); err != nil {
				return configProcessNotifications{}, fmt.Errorf("update Hardcover user book read for book %q: %w", cfg.BookHash, err)
			}
		}

		if userBookID > 0 {
			bs.LastProgressSent = hardcoverPages
		}
	}

	markedComplete := shouldNotifyComplete()

	e.emit(SyncEvent{Type: "progress_synced", Title: bs.Title, Detail: fmt.Sprintf("%d%% → Hardcover", pct)})

	bs.ReadestProgress = [2]int{current, total}
	e.state.SetBook(cfg.BookHash, bs)
	if markedComplete {
		return configProcessNotifications{completed: &bookCompletedNotification{book: bs}}, nil
	}
	return configProcessNotifications{}, nil
}

func localLinkageMatchesEdition(bs state.BookState, editionIDPtr *int) bool {
	if editionIDPtr == nil {
		return true
	}
	return bs.UserBookID != 0 &&
		bs.UserBookEditionID == *editionIDPtr &&
		bs.UserBookReadID != 0
}

func updateUnlinkedReadingNotificationState(bs *state.BookState, previousStatusID, currentStatusID int) bool {
	if bs.HardcoverBookID != 0 {
		bs.UnlinkedReadingNotificationPending = false
		return false
	}
	if !isActiveReadestStatus(currentStatusID) {
		bs.UnlinkedReadingNotificationPending = false
		return false
	}
	if !isActiveReadestStatus(previousStatusID) {
		bs.UnlinkedReadingNotificationPending = true
	}
	return bs.UnlinkedReadingNotificationPending
}

func unlinkedReadingNotificationReady(bs state.BookState, minSyncPercent float64, minSyncPages int) bool {
	if bs.HardcoverBookID != 0 || !bs.UnlinkedReadingNotificationPending {
		return false
	}
	statusID := DeriveStatus(bs.ReadestProgress[0], bs.ReadestProgress[1], bs.ReadestStatus, minSyncPercent, minSyncPages)
	return isActiveReadestStatus(statusID)
}

func isActiveReadestStatus(statusID int) bool {
	return statusID == hardcover.StatusCurrentlyReading || statusID == hardcover.StatusRead
}

func selectUserBookRead(reads []hardcover.UserBookRead, editionID, currentReadID int) (hardcover.UserBookRead, bool) {
	if editionID != 0 {
		for _, read := range reads {
			if read.ID != 0 && read.EditionID != nil && *read.EditionID == editionID {
				return read, true
			}
		}
	}
	if currentReadID != 0 {
		for _, read := range reads {
			if read.ID == currentReadID {
				return read, true
			}
		}
	}
	for _, read := range reads {
		if read.ID != 0 && read.EditionID == nil {
			return read, true
		}
	}
	for _, read := range reads {
		if read.ID != 0 {
			return read, true
		}
	}
	return hardcover.UserBookRead{}, false
}

// parseTimestamp parses an RFC3339 timestamp string and returns Unix milliseconds.
// Returns 0 on error or empty string.
func parseTimestamp(s string) int64 {
	if s == "" {
		return 0
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		// Try without timezone.
		t, err = time.Parse("2006-01-02T15:04:05", s)
		if err != nil {
			return 0
		}
	}
	return t.UnixMilli()
}

func (e *Engine) emit(evt SyncEvent) {
	if e.events != nil {
		e.events.Publish(evt)
	}
}

func (e *Engine) notifyBookAdded(ctx context.Context, book state.BookState, autoLinked bool) bool {
	if e.notifier == nil {
		return false
	}
	if err := e.notifier.NotifyBookAdded(ctx, book, autoLinked); err != nil {
		e.logger.Error("notification failed", "event", "book_added", "title", book.Title, "error", err)
		return false
	}
	return true
}

func (e *Engine) notifyBookCompleted(ctx context.Context, book state.BookState) {
	if e.notifier == nil {
		return
	}
	if err := e.notifier.NotifyBookCompleted(ctx, book); err != nil {
		e.logger.Error("notification failed", "event", "book_completed", "title", book.Title, "error", err)
	}
}

func (e *Engine) notifyBookStartedUnlinked(ctx context.Context, book state.BookState) bool {
	if e.notifier == nil {
		return false
	}
	if err := e.notifier.NotifyBookStartedUnlinked(ctx, book); err != nil {
		e.logger.Error("notification failed", "event", "book_started_unlinked", "title", book.Title, "error", err)
		return false
	}
	return true
}

func (e *Engine) markUnlinkedReadingNotificationSent(bookHash string) {
	updated := e.state.UpdateBook(bookHash, func(b *state.BookState) {
		b.UnlinkedReadingNotificationPending = false
	})
	if !updated {
		return
	}
	if err := e.state.Save(); err != nil {
		e.logger.Error("failed to save notification state", "book_hash", bookHash, "error", err)
	}
}

func (e *Engine) notifyCriticalSyncError(ctx context.Context, err error) {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return
	}
	e.notifyCriticalError(ctx, err)
}

func (e *Engine) notifyCriticalError(ctx context.Context, err error) {
	if e.notifier == nil || err == nil {
		return
	}

	now := time.Now
	if e.now != nil {
		now = e.now
	}
	today := now().Format("2006-01-02")
	key := criticalNotificationKey(err)

	e.notifyMu.Lock()
	if e.criticalNotificationDays == nil {
		e.criticalNotificationDays = make(map[string]string)
	}
	if e.criticalNotificationDays[key] == today {
		e.notifyMu.Unlock()
		return
	}

	if notifyErr := e.notifier.NotifyCriticalError(ctx, err); notifyErr != nil {
		e.notifyMu.Unlock()
		e.logger.Error("notification failed", "event", "critical_error", "error", notifyErr)
		return
	}
	e.criticalNotificationDays[key] = today
	e.notifyMu.Unlock()
}

func criticalNotificationKey(err error) string {
	return fmt.Sprintf("%T:%s", err, err.Error())
}

func (e *Engine) statusName(id int) string {
	if name, ok := e.statusNames[id]; ok {
		return name
	}
	return fmt.Sprintf("unknown(%d)", id)
}
