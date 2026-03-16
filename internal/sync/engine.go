package sync

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

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

	mu      sync.Mutex // prevents concurrent Tick() calls
	syncing bool

	manualSync bool // true = dry-run updater for polling, real updater for SyncNow

	privacySettingID int            // fetched from Hardcover on first sync
	statusNames      map[int]string // fetched from Hardcover on first sync
}

// NewEngine constructs an Engine with the given dependencies.
// If manualSync is true, the polling loop uses a dry-run updater (reads only),
// and SyncNow() must be called to push changes to Hardcover.
func NewEngine(readest ReadestPuller, finder BookFinder, updater ProgressUpdater,
	st *state.State, matcher *Matcher, logger *slog.Logger, manualSync bool) *Engine {
	e := &Engine{
		readest:     readest,
		finder:      finder,
		updater:     updater,
		realUpdater: updater,
		state:       st,
		matcher:     matcher,
		logger:      logger,
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
	saved := e.updater
	e.updater = e.realUpdater
	defer func() { e.updater = saved }()
	return e.Tick(ctx)
}

// Tick performs a single sync cycle. Returns immediately if a sync is already in progress.
func (e *Engine) Tick(ctx context.Context) error {
	e.mu.Lock()
	if e.syncing {
		e.mu.Unlock()
		e.logger.Info("sync already in progress, skipping tick")
		return nil
	}
	e.syncing = true
	e.mu.Unlock()

	defer func() {
		e.mu.Lock()
		e.syncing = false
		e.mu.Unlock()
	}()

	// Step 1: Cache privacy_setting_id and status names from Hardcover on first call.
	if e.privacySettingID == 0 {
		me, err := e.updater.GetMe(ctx)
		if err != nil {
			return err
		}
		if me != nil {
			e.privacySettingID = me.AccountPrivacySettingID
		}

		statuses, err := e.updater.GetStatuses(ctx)
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

	// Step 2: Pull books since last sync.
	books, err := e.readest.PullBooks(ctx, e.state.LastBookSync)
	if err != nil {
		return err
	}

	var maxBookUpdatedAt int64
	var newBooks, updatedBooks int
	for _, book := range books {
		if book.DeletedAt != nil {
			continue
		}
		ts := parseTimestamp(book.UpdatedAt)
		if ts > maxBookUpdatedAt {
			maxBookUpdatedAt = ts
		}
		_, exists := e.state.GetBook(book.BookHash)
		if err := e.processBook(ctx, book); err != nil {
			e.logger.Error("failed to process book", "book_hash", book.BookHash, "title", book.Title, "error", err)
		}
		if exists {
			updatedBooks++
		} else {
			newBooks++
		}
	}

	// Step 3: Pull configs since last sync.
	configs, err := e.readest.PullConfigs(ctx, e.state.LastConfigSync)
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
		if err := e.processConfig(ctx, cfg); err != nil {
			e.logger.Error("failed to process config", "book_hash", cfg.BookHash, "error", err)
		}
	}

	// Step 4: Update state timestamps from max updated_at of returned records.
	if maxBookUpdatedAt > e.state.LastBookSync {
		e.state.LastBookSync = maxBookUpdatedAt
	}
	if maxConfigUpdatedAt > e.state.LastConfigSync {
		e.state.LastConfigSync = maxConfigUpdatedAt
	}

	// Step 5: Save state.
	e.logger.Info("sync tick complete",
		"new_books", newBooks,
		"updated_books", updatedBooks,
		"configs_processed", len(configs),
	)
	return e.state.Save()
}

// processBook handles a single DBBook: updates existing state or matches and creates new entry.
func (e *Engine) processBook(ctx context.Context, book readest.DBBook) error {
	// If already in state, just update ReadestStatus if changed.
	if existing, ok := e.state.GetBook(book.BookHash); ok {
		changed := false
		if book.ReadingStatus != "" && existing.ReadestStatus != book.ReadingStatus {
			existing.ReadestStatus = book.ReadingStatus
			changed = true
		}
		if changed {
			e.state.SetBook(book.BookHash, existing)
		}
		return nil
	}

	// Parse identifiers from metadata, title, author.
	ids := identifier.Parse(book.Metadata, book.Title, book.Author)

	// Match via matcher.
	result, err := e.matcher.Match(ctx, ids)
	if err != nil {
		return err
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
	} else {
		bs.HardcoverBookID = result.BookID
		bs.HardcoverSlug = result.Slug
		bs.EditionID = result.EditionID
		bs.EditionPages = result.EditionPages
		bs.ReadingFormatID = result.ReadingFormatID
		bs.MatchMethod = result.MatchMethod
		e.logger.Info("matched book", "title", book.Title, "slug", result.Slug, "method", result.MatchMethod)
	}

	e.state.SetBook(book.BookHash, bs)
	return nil
}

// processConfig handles a single DBBookConfig: updates progress/status on Hardcover.
func (e *Engine) processConfig(ctx context.Context, cfg readest.DBBookConfig) error {
	// Get book from state; skip if not exists or not matched.
	bs, ok := e.state.GetBook(cfg.BookHash)
	if !ok || bs.HardcoverBookID == 0 {
		return nil
	}

	// Parse progress.
	progress, err := readest.ParseConfigProgress(cfg.Progress)
	if err != nil {
		return err
	}

	current, total := progress[0], progress[1]

	// DeriveStatus; skip if 0.
	statusID := DeriveStatus(current, total, bs.ReadestStatus)
	if statusID == 0 {
		return nil
	}

	// ConvertProgress (skip progress update if EditionPages == 0).
	var hardcoverPages int
	if bs.EditionPages > 0 {
		hardcoverPages = ConvertProgress(current, total, bs.EditionPages)
	}

	// Skip if nothing changed.
	if statusID == bs.LastStatusSent && hardcoverPages == bs.LastProgressSent {
		return nil
	}

	var pct int
	if total > 0 {
		pct = current * 100 / total
	}
	progressMsg := "syncing progress"
	if e.manualSync && e.updater != e.realUpdater {
		progressMsg = "would sync progress (manual mode)"
	}
	e.logger.Info(progressMsg,
		"title", bs.Title,
		"slug", bs.HardcoverSlug,
		"edition_id", bs.EditionID,
		"status", e.statusName(statusID),
		"progress", fmt.Sprintf("%d%% (%d/%d pages)", pct, hardcoverPages, bs.EditionPages),
	)

	// Ensure UserBookID exists.
	userBookID := bs.UserBookID
	if userBookID == 0 {
		// Try GetUserBook first.
		var editionIDPtr *int
		if bs.EditionID != 0 {
			edID := bs.EditionID
			editionIDPtr = &edID
		}

		existing, err := e.updater.GetUserBook(ctx, bs.HardcoverBookID)
		if err != nil {
			return err
		}

		if existing != nil {
			userBookID = existing.ID
		} else {
			inserted, err := e.updater.InsertUserBook(ctx, bs.HardcoverBookID, statusID, e.privacySettingID, editionIDPtr)
			if err != nil {
				return err
			}
			userBookID = inserted.ID
			bs.LastStatusSent = statusID
		}
		bs.UserBookID = userBookID
	}

	// Update status via UpdateUserBook if changed.
	if statusID != bs.LastStatusSent {
		if _, err := e.updater.UpdateUserBook(ctx, userBookID, statusID); err != nil {
			return err
		}
		bs.LastStatusSent = statusID
	}

	// Update progress.
	if hardcoverPages != bs.LastProgressSent && bs.EditionPages > 0 {
		var finishedAt *string
		if statusID == 3 {
			today := time.Now().Format("2006-01-02")
			finishedAt = &today
		}

		var editionIDPtr *int
		if bs.EditionID != 0 {
			edID := bs.EditionID
			editionIDPtr = &edID
		}

		if bs.UserBookReadID == 0 {
			// Insert new reading session.
			startedAt := time.Now().Format("2006-01-02")
			read, err := e.updater.InsertUserBookRead(ctx, userBookID, hardcoverPages, editionIDPtr, startedAt, finishedAt)
			if err != nil {
				return err
			}
			bs.UserBookReadID = read.ID
		} else {
			// Update existing reading session.
			if _, err := e.updater.UpdateUserBookRead(ctx, bs.UserBookReadID, hardcoverPages, finishedAt); err != nil {
				return err
			}
		}

		bs.LastProgressSent = hardcoverPages
	}

	bs.ReadestProgress = [2]int{current, total}
	e.state.SetBook(cfg.BookHash, bs)
	return nil
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

func (e *Engine) statusName(id int) string {
	if name, ok := e.statusNames[id]; ok {
		return name
	}
	return fmt.Sprintf("unknown(%d)", id)
}
