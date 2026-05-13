package sync

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/claytono/readest-hardcover-sync/internal/hardcover"
	"github.com/claytono/readest-hardcover-sync/internal/readest"
	"github.com/claytono/readest-hardcover-sync/internal/state"
)

// --- Mock implementations ---

type mockReadestPuller struct {
	books   []readest.DBBook
	configs []readest.DBBookConfig
	bookErr error
	cfgErr  error
}

func (m *mockReadestPuller) PullBooks(_ context.Context, _ int64) ([]readest.DBBook, error) {
	return m.books, m.bookErr
}

func (m *mockReadestPuller) PullConfigs(_ context.Context, _ int64) ([]readest.DBBookConfig, error) {
	return m.configs, m.cfgErr
}

type mockBookFinder struct {
	bySlug      map[string]*hardcover.Book
	bySlugErr   error
	bySlugCalls []string
	byISBN13    map[string]*hardcover.Edition
	byISBN10    map[string]*hardcover.Edition
	search      []hardcover.Book
}

func (m *mockBookFinder) FindBookBySlug(_ context.Context, slug string) (*hardcover.Book, error) {
	m.bySlugCalls = append(m.bySlugCalls, slug)
	if m.bySlugErr != nil {
		return nil, m.bySlugErr
	}
	if m.bySlug != nil {
		return m.bySlug[slug], nil
	}
	return nil, nil
}

func (m *mockBookFinder) FindEditionByISBN13(_ context.Context, isbn string) (*hardcover.Edition, error) {
	if m.byISBN13 != nil {
		return m.byISBN13[isbn], nil
	}
	return nil, nil
}

func (m *mockBookFinder) FindEditionByISBN10(_ context.Context, isbn string) (*hardcover.Edition, error) {
	if m.byISBN10 != nil {
		return m.byISBN10[isbn], nil
	}
	return nil, nil
}

func (m *mockBookFinder) SearchBooks(_ context.Context, _ string) ([]hardcover.Book, error) {
	return m.search, nil
}

type insertUserBookCall struct {
	bookID           int
	statusID         int
	privacySettingID int
	editionID        *int
}

type updateUserBookCall struct {
	id        int
	statusID  int
	editionID *int
}

type insertUserBookReadCall struct {
	userBookID    int
	progressPages int
	editionID     *int
	startedAt     string
	finishedAt    *string
}

type updateUserBookReadCall struct {
	id            int
	progressPages int
	finishedAt    *string
}

type mockProgressUpdater struct {
	meResponse *hardcover.MeResponse
	meErr      error

	statusesErr error

	userBook         *hardcover.UserBook
	getUserErr       error
	getUserBookCalls []int

	insertedUserBook    *hardcover.UserBook
	insertUserBookErr   error
	insertUserBookCalls []insertUserBookCall

	updatedUserBook     *hardcover.UserBook
	updateUserBookErr   error
	updateUserBookCalls []updateUserBookCall

	insertedRead        *hardcover.UserBookRead
	insertUserReadErr   error
	insertUserReadCalls []insertUserBookReadCall

	updatedRead         *hardcover.UserBookRead
	updateUserReadErr   error
	updateUserReadCalls []updateUserBookReadCall
}

type addedBookNotification struct {
	book       state.BookState
	autoLinked bool
}

type mockNotifier struct {
	added     []addedBookNotification
	completed []state.BookState
	critical  []error
	err       error
}

type syncingObserverNotifier struct {
	engine           *Engine
	called           bool
	addedSyncing     bool
	completedCalled  bool
	completedSyncing bool
}

func (m *mockNotifier) NotifyBookAdded(_ context.Context, book state.BookState, autoLinked bool) error {
	m.added = append(m.added, addedBookNotification{book: book, autoLinked: autoLinked})
	return m.err
}

func (m *mockNotifier) NotifyBookCompleted(_ context.Context, book state.BookState) error {
	m.completed = append(m.completed, book)
	return m.err
}

func (m *mockNotifier) NotifyCriticalError(_ context.Context, err error) error {
	m.critical = append(m.critical, err)
	return m.err
}

func (n *syncingObserverNotifier) NotifyBookAdded(_ context.Context, _ state.BookState, _ bool) error {
	n.engine.mu.Lock()
	n.addedSyncing = n.engine.syncing
	n.engine.mu.Unlock()
	n.called = true
	return nil
}

func (n *syncingObserverNotifier) NotifyBookCompleted(_ context.Context, _ state.BookState) error {
	n.engine.mu.Lock()
	n.completedSyncing = n.engine.syncing
	n.engine.mu.Unlock()
	n.completedCalled = true
	return nil
}

func (n *syncingObserverNotifier) NotifyCriticalError(_ context.Context, _ error) error {
	return nil
}

func (m *mockProgressUpdater) GetMe(_ context.Context) (*hardcover.MeResponse, error) {
	return m.meResponse, m.meErr
}

func (m *mockProgressUpdater) GetStatuses(_ context.Context) ([]hardcover.BookStatus, error) {
	if m.statusesErr != nil {
		return nil, m.statusesErr
	}
	return []hardcover.BookStatus{
		{ID: hardcover.StatusWantToRead, Status: "Want to Read"},
		{ID: hardcover.StatusCurrentlyReading, Status: "Currently Reading"},
		{ID: hardcover.StatusRead, Status: "Read"},
		{ID: hardcover.StatusPaused, Status: "Paused"},
		{ID: hardcover.StatusDidNotFinish, Status: "Did Not Finish"},
		{ID: hardcover.StatusIgnored, Status: "Ignored"},
	}, nil
}

func (m *mockProgressUpdater) GetUserBook(_ context.Context, bookID int) (*hardcover.UserBook, error) {
	m.getUserBookCalls = append(m.getUserBookCalls, bookID)
	return m.userBook, m.getUserErr
}

func (m *mockProgressUpdater) InsertUserBook(_ context.Context, bookID, statusID, privacySettingID int, editionID *int) (*hardcover.UserBook, error) {
	m.insertUserBookCalls = append(m.insertUserBookCalls, insertUserBookCall{
		bookID:           bookID,
		statusID:         statusID,
		privacySettingID: privacySettingID,
		editionID:        editionID,
	})
	if m.insertUserBookErr != nil {
		return nil, m.insertUserBookErr
	}
	if m.insertedUserBook != nil {
		return m.insertedUserBook, nil
	}
	return &hardcover.UserBook{ID: 99, BookID: bookID, StatusID: statusID}, nil
}

func (m *mockProgressUpdater) UpdateUserBook(_ context.Context, id, statusID int, editionID *int) (*hardcover.UserBook, error) {
	m.updateUserBookCalls = append(m.updateUserBookCalls, updateUserBookCall{id: id, statusID: statusID, editionID: editionID})
	if m.updateUserBookErr != nil {
		return nil, m.updateUserBookErr
	}
	if m.updatedUserBook != nil {
		return m.updatedUserBook, nil
	}
	return &hardcover.UserBook{ID: id, StatusID: statusID, EditionID: editionID}, nil
}

func (m *mockProgressUpdater) InsertUserBookRead(_ context.Context, userBookID, progressPages int, editionID *int, startedAt string, finishedAt *string) (*hardcover.UserBookRead, error) {
	m.insertUserReadCalls = append(m.insertUserReadCalls, insertUserBookReadCall{
		userBookID:    userBookID,
		progressPages: progressPages,
		editionID:     editionID,
		startedAt:     startedAt,
		finishedAt:    finishedAt,
	})
	if m.insertUserReadErr != nil {
		return nil, m.insertUserReadErr
	}
	if m.insertedRead != nil {
		return m.insertedRead, nil
	}
	return &hardcover.UserBookRead{ID: 42, ProgressPages: &progressPages}, nil
}

func (m *mockProgressUpdater) UpdateUserBookRead(_ context.Context, id, progressPages int, finishedAt *string) (*hardcover.UserBookRead, error) {
	m.updateUserReadCalls = append(m.updateUserReadCalls, updateUserBookReadCall{
		id:            id,
		progressPages: progressPages,
		finishedAt:    finishedAt,
	})
	if m.updateUserReadErr != nil {
		return nil, m.updateUserReadErr
	}
	if m.updatedRead != nil {
		return m.updatedRead, nil
	}
	return &hardcover.UserBookRead{ID: id, ProgressPages: &progressPages}, nil
}

// --- Helpers ---

func newTestEngine(t *testing.T, puller *mockReadestPuller, finder *mockBookFinder, updater *mockProgressUpdater) (*Engine, *state.State) {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "state-*.json")
	require.NoError(t, err)
	require.NoError(t, f.Close())

	st := state.New(f.Name())
	matcher := NewMatcher(finder, false)
	logger := newNopLogger()

	e := NewEngine(puller, finder, updater, st, matcher, logger, false, nil)
	return e, st
}

func newNopLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

func metaWithISBN(isbn13 string) *string {
	s := `{"identifier":"` + isbn13 + `"}`
	return &s
}

// --- Tests ---

// TestEngine_NewBook_Matched: PullBooks returns 1 book with ISBN, PullConfigs returns
// config with progress [350,700]. Matcher returns match. Verify InsertUserBook called
// with status=2, InsertUserBookRead called with converted pages.
func TestEngine_NewBook_Matched(t *testing.T) {
	isbn := "9780987654321"
	pages := 400
	editionID := 10
	edition := &hardcover.Edition{
		ID:     editionID,
		BookID: 100,
		Pages:  &pages,
		ISBN13: isbn,
	}

	puller := &mockReadestPuller{
		books: []readest.DBBook{
			{
				BookHash:  "hash1",
				Title:     "Test Book",
				Author:    "Test Author",
				Metadata:  metaWithISBN(isbn),
				UpdatedAt: "2024-01-01T00:00:00Z",
			},
		},
		configs: []readest.DBBookConfig{
			{
				BookHash:  "hash1",
				Progress:  "[350,700]",
				UpdatedAt: "2024-01-01T00:01:00Z",
			},
		},
	}

	finder := &mockBookFinder{
		byISBN13: map[string]*hardcover.Edition{
			isbn: edition,
		},
	}

	updater := &mockProgressUpdater{
		meResponse: &hardcover.MeResponse{ID: 1, AccountPrivacySettingID: 5},
	}

	engine, st := newTestEngine(t, puller, finder, updater)

	err := engine.Tick(context.Background())
	require.NoError(t, err)

	// Verify book was matched in state.
	bs, ok := st.GetBook("hash1")
	require.True(t, ok)
	assert.Equal(t, 100, bs.HardcoverBookID)
	assert.Equal(t, "isbn13", bs.MatchMethod)

	// Verify InsertUserBook was called with status=2 (currently reading).
	require.Len(t, updater.insertUserBookCalls, 1)
	call := updater.insertUserBookCalls[0]
	assert.Equal(t, 100, call.bookID)
	assert.Equal(t, 2, call.statusID)
	assert.Equal(t, 5, call.privacySettingID)

	// Verify InsertUserBookRead was called with converted pages.
	// ConvertProgress(350, 700, 400) = round(350/700 * 400) = 200
	require.Len(t, updater.insertUserReadCalls, 1)
	readCall := updater.insertUserReadCalls[0]
	assert.Equal(t, 200, readCall.progressPages)
}

func TestEngine_NewBook_NotifyAutoLinked(t *testing.T) {
	isbn := "9781234567890"
	pages := 400
	edition := &hardcover.Edition{
		ID:     10,
		BookID: 100,
		Pages:  &pages,
		ISBN13: isbn,
	}
	puller := &mockReadestPuller{
		books: []readest.DBBook{
			{
				BookHash: "hashNotify",
				Title:    "Notify Book",
				Author:   "Notify Author",
				Metadata: metaWithISBN(isbn),
			},
		},
		configs: []readest.DBBookConfig{},
	}
	finder := &mockBookFinder{
		byISBN13: map[string]*hardcover.Edition{isbn: edition},
	}
	updater := &mockProgressUpdater{meResponse: &hardcover.MeResponse{ID: 1, AccountPrivacySettingID: 5}}
	notifier := &mockNotifier{}

	f, err := os.CreateTemp(t.TempDir(), "state-*.json")
	require.NoError(t, err)
	require.NoError(t, f.Close())
	st := state.New(f.Name())
	engine := NewEngine(puller, finder, updater, st, NewMatcher(finder, false), newNopLogger(), false, &EngineOptions{
		Notifier: notifier,
	})

	require.NoError(t, engine.Tick(context.Background()))

	require.Len(t, notifier.added, 1)
	assert.True(t, notifier.added[0].autoLinked)
	assert.Equal(t, "Notify Book", notifier.added[0].book.Title)
	assert.Equal(t, "isbn13", notifier.added[0].book.MatchMethod)
}

func TestEngine_NewBook_NotificationUsesBackfilledCover(t *testing.T) {
	coverSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("fake-cover"))
	}))
	defer coverSrv.Close()

	isbn := "9781234567890"
	pages := 400
	edition := &hardcover.Edition{
		ID:     10,
		BookID: 100,
		Pages:  &pages,
		ISBN13: isbn,
		Book: &hardcover.Book{
			ID:   100,
			Slug: "cover-backfilled-book",
		},
	}
	puller := &mockReadestPuller{
		books: []readest.DBBook{
			{
				BookHash: "hashBackfilledNotify",
				Title:    "Backfilled Cover Notify",
				Author:   "Notify Author",
				Metadata: metaWithISBN(isbn),
			},
		},
		configs: []readest.DBBookConfig{},
	}
	finder := &mockBookFinder{
		byISBN13: map[string]*hardcover.Edition{isbn: edition},
		bySlug: map[string]*hardcover.Book{
			"cover-backfilled-book": {
				ID:   100,
				Slug: "cover-backfilled-book",
				CachedImage: &hardcover.CachedImage{
					URL: coverSrv.URL + "/cover.jpg",
				},
			},
		},
	}
	updater := &mockProgressUpdater{meResponse: &hardcover.MeResponse{ID: 1, AccountPrivacySettingID: 5}}
	notifier := &mockNotifier{}

	f, err := os.CreateTemp(t.TempDir(), "state-*.json")
	require.NoError(t, err)
	require.NoError(t, f.Close())
	st := state.New(f.Name())
	engine := NewEngine(puller, finder, updater, st, NewMatcher(finder, false), newNopLogger(), false, &EngineOptions{
		CoversDir: t.TempDir(),
		Notifier:  notifier,
	})

	require.NoError(t, engine.Tick(context.Background()))

	require.Len(t, notifier.added, 1)
	assert.Equal(t, coverSrv.URL+"/cover.jpg", notifier.added[0].book.CoverURL)
	assert.NotEmpty(t, notifier.added[0].book.CoverPath)
}

func TestEngine_NotificationsRunAfterSyncReleased(t *testing.T) {
	puller := &mockReadestPuller{
		books: []readest.DBBook{
			{
				BookHash: "hashReleaseNotify",
				Title:    "Release Notify",
			},
		},
		configs: []readest.DBBookConfig{},
	}
	finder := &mockBookFinder{}
	updater := &mockProgressUpdater{meResponse: &hardcover.MeResponse{ID: 1, AccountPrivacySettingID: 5}}

	f, err := os.CreateTemp(t.TempDir(), "state-*.json")
	require.NoError(t, err)
	require.NoError(t, f.Close())
	st := state.New(f.Name())
	engine := NewEngine(puller, finder, updater, st, NewMatcher(finder, false), newNopLogger(), false, nil)
	notifier := &syncingObserverNotifier{engine: engine}
	engine.notifier = notifier

	require.NoError(t, engine.Tick(context.Background()))

	require.True(t, notifier.called)
	assert.False(t, notifier.addedSyncing)
}

func TestEngine_CompletionNotificationsRunAfterSyncReleased(t *testing.T) {
	puller := &mockReadestPuller{
		books: []readest.DBBook{},
		configs: []readest.DBBookConfig{
			{
				BookHash:  "hashReleaseCompleteNotify",
				Progress:  "[400,400]",
				UpdatedAt: "2024-01-01T00:01:00Z",
			},
		},
	}
	finder := &mockBookFinder{}
	updater := &mockProgressUpdater{meResponse: &hardcover.MeResponse{ID: 1, AccountPrivacySettingID: 5}}

	engine, st := newTestEngine(t, puller, finder, updater)
	notifier := &syncingObserverNotifier{engine: engine}
	engine.notifier = notifier
	st.SetBook("hashReleaseCompleteNotify", state.BookState{
		BookHash:          "hashReleaseCompleteNotify",
		Title:             "Release Complete Notify",
		HardcoverBookID:   100,
		EditionID:         10,
		EditionPages:      400,
		UserBookID:        55,
		UserBookEditionID: 10,
		UserBookReadID:    99,
		LastStatusSent:    hardcover.StatusCurrentlyReading,
		LastProgressSent:  200,
	})

	require.NoError(t, engine.Tick(context.Background()))

	require.True(t, notifier.completedCalled)
	assert.False(t, notifier.completedSyncing)
}

func TestEngine_NewBook_NotifyUnmatched(t *testing.T) {
	puller := &mockReadestPuller{
		books: []readest.DBBook{
			{
				BookHash: "hashUnmatchedNotify",
				Title:    "Unmatched Notify",
			},
		},
		configs: []readest.DBBookConfig{},
	}
	finder := &mockBookFinder{}
	updater := &mockProgressUpdater{meResponse: &hardcover.MeResponse{ID: 1, AccountPrivacySettingID: 5}}
	notifier := &mockNotifier{}

	f, err := os.CreateTemp(t.TempDir(), "state-*.json")
	require.NoError(t, err)
	require.NoError(t, f.Close())
	st := state.New(f.Name())
	engine := NewEngine(puller, finder, updater, st, NewMatcher(finder, false), newNopLogger(), false, &EngineOptions{
		Notifier: notifier,
	})

	require.NoError(t, engine.Tick(context.Background()))

	require.Len(t, notifier.added, 1)
	assert.False(t, notifier.added[0].autoLinked)
	assert.Equal(t, "Unmatched Notify", notifier.added[0].book.Title)
	assert.True(t, notifier.added[0].book.Unmatched)
}

func TestEngine_NewBook_NotificationFailureDoesNotFailTick(t *testing.T) {
	puller := &mockReadestPuller{
		books: []readest.DBBook{
			{
				BookHash: "hashNotifyFails",
				Title:    "Notify Fails",
			},
		},
		configs: []readest.DBBookConfig{},
	}
	finder := &mockBookFinder{}
	updater := &mockProgressUpdater{meResponse: &hardcover.MeResponse{ID: 1, AccountPrivacySettingID: 5}}
	notifier := &mockNotifier{err: errors.New("slack unavailable")}

	engine, st := newTestEngine(t, puller, finder, updater)
	engine.notifier = notifier

	require.NoError(t, engine.Tick(context.Background()))

	require.Len(t, notifier.added, 1)
	assert.Empty(t, notifier.critical)
	bs, ok := st.GetBook("hashNotifyFails")
	require.True(t, ok)
	assert.Equal(t, "Notify Fails", bs.Title)
}

func TestEngine_NewBook_DoesNotNotifyWhenSaveFails(t *testing.T) {
	puller := &mockReadestPuller{
		books: []readest.DBBook{
			{
				BookHash: "hashSaveFails",
				Title:    "Save Fails",
			},
		},
		configs: []readest.DBBookConfig{},
	}
	finder := &mockBookFinder{}
	updater := &mockProgressUpdater{meResponse: &hardcover.MeResponse{ID: 1, AccountPrivacySettingID: 5}}
	notifier := &mockNotifier{}
	st := state.New(filepath.Join(t.TempDir(), "missing", "state.json"))
	engine := NewEngine(puller, finder, updater, st, NewMatcher(finder, false), newNopLogger(), false, &EngineOptions{
		Notifier: notifier,
	})

	err := engine.Tick(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "save sync state")

	assert.Empty(t, notifier.added)
	require.Len(t, notifier.critical, 1)
	assert.Contains(t, notifier.critical[0].Error(), "save sync state")
}

// TestEngine_ProgressUpdate: State has matched book with UserBookID=1 and UserBookReadID=1.
// PullConfigs returns updated progress. Verify UpdateUserBookRead called (not insert).
func TestEngine_ProgressUpdate(t *testing.T) {
	puller := &mockReadestPuller{
		books: []readest.DBBook{},
		configs: []readest.DBBookConfig{
			{
				BookHash:  "hash2",
				Progress:  "[300,600]",
				UpdatedAt: "2024-01-02T00:00:00Z",
			},
		},
	}

	finder := &mockBookFinder{}
	updater := &mockProgressUpdater{
		meResponse: &hardcover.MeResponse{ID: 1, AccountPrivacySettingID: 5},
	}

	engine, st := newTestEngine(t, puller, finder, updater)

	// Pre-populate state with a matched book that already has UserBookID and UserBookReadID.
	st.SetBook("hash2", state.BookState{
		BookHash:          "hash2",
		Title:             "Existing Book",
		HardcoverBookID:   200,
		HardcoverSlug:     "existing-book",
		EditionID:         20,
		EditionPages:      600,
		UserBookID:        1,
		UserBookEditionID: 20,
		UserBookReadID:    1,
		LastStatusSent:    2,
		LastProgressSent:  150,
	})

	err := engine.Tick(context.Background())
	require.NoError(t, err)

	// Verify UpdateUserBookRead was called, not InsertUserBookRead.
	assert.Empty(t, updater.getUserBookCalls, "cached linkage should avoid an extra GetUserBook call")
	assert.Empty(t, updater.insertUserReadCalls, "InsertUserBookRead should not be called")
	require.Len(t, updater.updateUserReadCalls, 1)
	updateCall := updater.updateUserReadCalls[0]
	assert.Equal(t, 1, updateCall.id)
	// ConvertProgress(300, 600, 600) = 300
	assert.Equal(t, 300, updateCall.progressPages)
}

func TestEngine_ProgressUpdate_NoEditionCachedReadSkipsLookup(t *testing.T) {
	puller := &mockReadestPuller{
		books: []readest.DBBook{},
		configs: []readest.DBBookConfig{
			{
				BookHash:  "hashNoEdition",
				Progress:  "[50,100]",
				UpdatedAt: "2024-01-02T00:00:00Z",
			},
		},
	}
	finder := &mockBookFinder{}
	updater := &mockProgressUpdater{
		meResponse: &hardcover.MeResponse{ID: 1, AccountPrivacySettingID: 5},
	}

	engine, st := newTestEngine(t, puller, finder, updater)
	st.SetBook("hashNoEdition", state.BookState{
		BookHash:         "hashNoEdition",
		Title:            "No Edition Book",
		HardcoverBookID:  201,
		EditionPages:     200,
		UserBookID:       11,
		UserBookReadID:   12,
		LastStatusSent:   hardcover.StatusCurrentlyReading,
		LastProgressSent: 50,
	})

	require.NoError(t, engine.Tick(context.Background()))

	assert.Empty(t, updater.getUserBookCalls, "cached no-edition linkage should avoid GetUserBook")
	require.Len(t, updater.updateUserReadCalls, 1)
	assert.Equal(t, 12, updater.updateUserReadCalls[0].id)
	assert.Equal(t, 100, updater.updateUserReadCalls[0].progressPages)
}

// TestEngine_Finished: Config progress [700,700] or status "finished".
// Verify status 3, finished_at set.
func TestEngine_Finished(t *testing.T) {
	puller := &mockReadestPuller{
		books: []readest.DBBook{},
		configs: []readest.DBBookConfig{
			{
				BookHash:  "hash3",
				Progress:  "[700,700]",
				UpdatedAt: "2024-01-03T00:00:00Z",
			},
		},
	}

	finder := &mockBookFinder{}
	updater := &mockProgressUpdater{
		meResponse: &hardcover.MeResponse{ID: 1, AccountPrivacySettingID: 5},
	}

	engine, st := newTestEngine(t, puller, finder, updater)
	notifier := &mockNotifier{}
	engine.notifier = notifier

	// Pre-populate state.
	st.SetBook("hash3", state.BookState{
		BookHash:        "hash3",
		Title:           "Finished Book",
		HardcoverBookID: 300,
		EditionID:       30,
		EditionPages:    700,
		UserBookID:      5,
		UserBookReadID:  5,
		LastStatusSent:  2,
	})

	err := engine.Tick(context.Background())
	require.NoError(t, err)

	// Verify UpdateUserBook was called with status=3 (finished).
	require.Len(t, updater.updateUserBookCalls, 1)
	assert.Equal(t, 3, updater.updateUserBookCalls[0].statusID)

	// Verify UpdateUserBookRead was called with finished_at set.
	require.Len(t, updater.updateUserReadCalls, 1)
	assert.NotNil(t, updater.updateUserReadCalls[0].finishedAt)
	assert.Regexp(t, `^\d{4}-\d{2}-\d{2}$`, *updater.updateUserReadCalls[0].finishedAt)

	// State should be updated.
	bs, ok := st.GetBook("hash3")
	require.True(t, ok)
	assert.Equal(t, 3, bs.LastStatusSent)
	require.Len(t, notifier.completed, 1)
	assert.Equal(t, "Finished Book", notifier.completed[0].Title)
}

// TestEngine_SkipUnmatched: Matcher returns nil. Verify no Hardcover calls, book marked Unmatched in state.
func TestEngine_SkipUnmatched(t *testing.T) {
	puller := &mockReadestPuller{
		books: []readest.DBBook{
			{
				BookHash:  "hash4",
				Title:     "Unknown Book",
				Author:    "Unknown Author",
				UpdatedAt: "2024-01-04T00:00:00Z",
			},
		},
		configs: []readest.DBBookConfig{
			{
				BookHash:  "hash4",
				Progress:  "[100,400]",
				UpdatedAt: "2024-01-04T00:01:00Z",
			},
		},
	}

	finder := &mockBookFinder{} // returns nil for everything
	updater := &mockProgressUpdater{
		meResponse: &hardcover.MeResponse{ID: 1, AccountPrivacySettingID: 5},
	}

	engine, st := newTestEngine(t, puller, finder, updater)

	err := engine.Tick(context.Background())
	require.NoError(t, err)

	// Book should be marked as unmatched.
	bs, ok := st.GetBook("hash4")
	require.True(t, ok)
	assert.True(t, bs.Unmatched)
	assert.Equal(t, 0, bs.HardcoverBookID)

	// No Hardcover progress calls should have been made.
	assert.Empty(t, updater.insertUserBookCalls)
	assert.Empty(t, updater.updateUserBookCalls)
	assert.Empty(t, updater.insertUserReadCalls)
	assert.Empty(t, updater.updateUserReadCalls)
}

// TestEngine_SkipDuplicateProgress: State has LastProgressSent=200, new config produces same 200.
// Verify no Hardcover call.
func TestEngine_SkipDuplicateProgress(t *testing.T) {
	puller := &mockReadestPuller{
		books: []readest.DBBook{},
		configs: []readest.DBBookConfig{
			{
				BookHash:  "hash5",
				Progress:  "[350,700]",
				UpdatedAt: "2024-01-05T00:00:00Z",
			},
		},
	}

	finder := &mockBookFinder{}
	updater := &mockProgressUpdater{
		meResponse: &hardcover.MeResponse{ID: 1, AccountPrivacySettingID: 5},
	}

	engine, st := newTestEngine(t, puller, finder, updater)

	// ConvertProgress(350, 700, 400) = 200, so last progress is already 200 with status 2.
	st.SetBook("hash5", state.BookState{
		BookHash:          "hash5",
		Title:             "Same Progress Book",
		HardcoverBookID:   500,
		EditionID:         50,
		EditionPages:      400,
		UserBookID:        10,
		UserBookEditionID: 50,
		UserBookReadID:    10,
		LastStatusSent:    2,
		LastProgressSent:  200, // same as what ConvertProgress would produce
	})

	err := engine.Tick(context.Background())
	require.NoError(t, err)

	// No Hardcover calls should be made.
	assert.Empty(t, updater.getUserBookCalls)
	assert.Empty(t, updater.updateUserBookCalls)
	assert.Empty(t, updater.updateUserReadCalls)
	assert.Empty(t, updater.insertUserBookCalls)
	assert.Empty(t, updater.insertUserReadCalls)
}

// TestEngine_ErrorDoesntStopOthers: 2 books, first update fails, second succeeds.
// Verify second still processed.
func TestEngine_ErrorDoesntStopOthers(t *testing.T) {
	isbn1 := "9781111111111"
	isbn2 := "9782222222222"
	pages := 400
	edition1 := &hardcover.Edition{ID: 11, BookID: 101, Pages: &pages, ISBN13: isbn1}
	edition2 := &hardcover.Edition{ID: 22, BookID: 202, Pages: &pages, ISBN13: isbn2}

	puller := &mockReadestPuller{
		books: []readest.DBBook{},
		configs: []readest.DBBookConfig{
			{
				BookHash:  "hashA",
				Progress:  "[100,400]",
				UpdatedAt: "2024-01-06T00:00:00Z",
			},
			{
				BookHash:  "hashB",
				Progress:  "[200,400]",
				UpdatedAt: "2024-01-06T00:01:00Z",
			},
		},
	}

	finder := &mockBookFinder{
		byISBN13: map[string]*hardcover.Edition{
			isbn1: edition1,
			isbn2: edition2,
		},
	}

	// InsertUserBook will fail for the first call, succeed for the second.
	callCount := 0
	updater := &mockProgressUpdater{
		meResponse: &hardcover.MeResponse{ID: 1, AccountPrivacySettingID: 5},
	}

	engine, st := newTestEngine(t, puller, finder, updater)

	// Override: make the first InsertUserBookRead fail by giving hashA a UserBookID
	// but causing UpdateUserBook to fail, then recovering for hashB.
	// Simpler approach: pre-populate both books, first has no read ID and we make insert fail.
	// We'll use a custom updater that fails the first call but succeeds the second.
	callCount = 0
	updater.insertUserReadErr = nil // reset

	// Pre-populate books already matched, both with no UserBookReadID.
	st.SetBook("hashA", state.BookState{
		BookHash:        "hashA",
		Title:           "Book A",
		HardcoverBookID: 101,
		EditionID:       11,
		EditionPages:    400,
		UserBookID:      1,
		LastStatusSent:  2,
	})
	st.SetBook("hashB", state.BookState{
		BookHash:        "hashB",
		Title:           "Book B",
		HardcoverBookID: 202,
		EditionID:       22,
		EditionPages:    400,
		UserBookID:      2,
		LastStatusSent:  2,
	})

	// Make the InsertUserBookRead fail for the first call only.
	failOnFirst := &failFirstProgressUpdater{
		mockProgressUpdater: updater,
		insertReadErr:       errors.New("simulated insert failure"),
		callCount:           &callCount,
	}
	engine.updater = failOnFirst

	err := engine.Tick(context.Background())
	require.NoError(t, err) // tick should succeed overall even with individual failures

	// hashB should have been processed successfully.
	bs, ok := st.GetBook("hashB")
	require.True(t, ok)
	assert.Greater(t, bs.LastProgressSent, 0, "hashB should have progress sent")

	// hashA should NOT have progress sent due to failure.
	bsA, ok := st.GetBook("hashA")
	require.True(t, ok)
	assert.Equal(t, 0, bsA.LastProgressSent, "hashA progress should not be updated due to error")
}

// failFirstProgressUpdater wraps mockProgressUpdater and fails InsertUserBookRead on the first call.
type failFirstProgressUpdater struct {
	*mockProgressUpdater
	insertReadErr error
	callCount     *int
}

func (f *failFirstProgressUpdater) InsertUserBookRead(ctx context.Context, userBookID, progressPages int, editionID *int, startedAt string, finishedAt *string) (*hardcover.UserBookRead, error) {
	*f.callCount++
	if *f.callCount == 1 {
		return nil, f.insertReadErr
	}
	return f.mockProgressUpdater.InsertUserBookRead(ctx, userBookID, progressPages, editionID, startedAt, finishedAt)
}

// signalingPuller wraps mockReadestPuller, counts PullBooks calls, and signals on each call.
type signalingPuller struct {
	*mockReadestPuller
	count  *int
	called chan struct{} // receives a value on each PullBooks call
}

func (s *signalingPuller) PullBooks(ctx context.Context, since int64) ([]readest.DBBook, error) {
	*s.count++
	select {
	case s.called <- struct{}{}:
	default:
	}
	return s.mockReadestPuller.PullBooks(ctx, since)
}

// TestEngine_Run: Run calls Tick immediately then stops when context is cancelled.
func TestEngine_Run(t *testing.T) {
	callCount := 0
	puller := &mockReadestPuller{
		books:   []readest.DBBook{},
		configs: []readest.DBBookConfig{},
	}
	finder := &mockBookFinder{}
	updater := &mockProgressUpdater{
		meResponse: &hardcover.MeResponse{ID: 1, AccountPrivacySettingID: 5},
	}

	engine, _ := newTestEngine(t, puller, finder, updater)

	called := make(chan struct{}, 10)
	sp := &signalingPuller{mockReadestPuller: puller, count: &callCount, called: called}
	engine.readest = sp

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		defer close(done)
		// Use a very long interval so only the initial tick fires before cancel.
		engine.Run(ctx, 10*time.Minute)
	}()

	// Wait for the initial tick to fire.
	select {
	case <-called:
	case <-time.After(5 * time.Second):
		t.Fatal("initial tick did not fire")
	}
	cancel()

	select {
	case <-done:
		// Run returned after cancel — correct.
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not stop after context cancel")
	}
	assert.GreaterOrEqual(t, callCount, 1, "Run should have called Tick at least once immediately")
}

// TestEngine_Run_TickOnInterval: Run fires Tick again on the ticker interval.
func TestEngine_Run_TickOnInterval(t *testing.T) {
	puller := &mockReadestPuller{
		books:   []readest.DBBook{},
		configs: []readest.DBBookConfig{},
	}
	finder := &mockBookFinder{}
	updater := &mockProgressUpdater{
		meResponse: &hardcover.MeResponse{ID: 1, AccountPrivacySettingID: 5},
	}

	engine, _ := newTestEngine(t, puller, finder, updater)

	callCount := 0
	called := make(chan struct{}, 10)
	sp := &signalingPuller{mockReadestPuller: puller, count: &callCount, called: called}
	engine.readest = sp

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		engine.Run(ctx, 20*time.Millisecond)
	}()

	// Wait for at least 2 ticks (initial + at least one interval).
	for i := 0; i < 2; i++ {
		select {
		case <-called:
		case <-time.After(5 * time.Second):
			t.Fatalf("tick %d did not fire", i+1)
		}
	}
	cancel()

	<-done
	assert.GreaterOrEqual(t, callCount, 2, "Run should have ticked at least twice")
}

// TestEngine_SyncNow: Verifies SyncNow uses the real updater even in manual sync mode.
func TestEngine_SyncNow(t *testing.T) {
	puller := &mockReadestPuller{
		books:   []readest.DBBook{},
		configs: []readest.DBBookConfig{},
	}
	finder := &mockBookFinder{}
	realUpdater := &mockProgressUpdater{
		meResponse: &hardcover.MeResponse{ID: 1, AccountPrivacySettingID: 5},
	}

	f, err := os.CreateTemp(t.TempDir(), "state-*.json")
	require.NoError(t, err)
	require.NoError(t, f.Close())
	st := state.New(f.Name())
	matcher := NewMatcher(finder, false)
	logger := newNopLogger()

	engine := NewEngine(puller, finder, realUpdater, st, matcher, logger, true, nil)

	// In manual sync mode, engine.updater is the dry-run wrapper.
	assert.NotEqual(t, realUpdater, engine.updater, "manual mode should use dry-run updater for polling")

	// SyncNow should use the real updater and return no error.
	err = engine.SyncNow(context.Background())
	require.NoError(t, err)

	// After SyncNow completes, updater should be restored to dry-run.
	assert.NotEqual(t, realUpdater, engine.updater, "updater should be restored to dry-run after SyncNow")
}

func TestEngine_SyncNow_DoesNotMutateSharedUpdater(t *testing.T) {
	blocker := make(chan struct{})
	entered := make(chan struct{}, 1)
	puller := &blockingPuller{ready: blocker, entered: entered}
	finder := &mockBookFinder{}
	realUpdater := &mockProgressUpdater{
		meResponse: &hardcover.MeResponse{ID: 1, AccountPrivacySettingID: 5},
	}

	f, err := os.CreateTemp(t.TempDir(), "state-*.json")
	require.NoError(t, err)
	require.NoError(t, f.Close())
	st := state.New(f.Name())
	matcher := NewMatcher(finder, false)
	logger := newNopLogger()

	engine := NewEngine(puller, finder, realUpdater, st, matcher, logger, true, nil)
	dryRunUpdater := engine.updater

	done := make(chan error, 1)
	go func() {
		done <- engine.SyncNow(context.Background())
	}()

	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("SyncNow did not enter PullBooks")
	}

	assert.Equal(t, dryRunUpdater, engine.updater, "SyncNow must not swap the shared updater while running")

	close(blocker)
	require.NoError(t, <-done)
}

// TestEngine_FullSync resets timestamps and uses real updater.
func TestEngine_FullSync(t *testing.T) {
	puller := &mockReadestPuller{
		books:   []readest.DBBook{},
		configs: []readest.DBBookConfig{},
	}
	finder := &mockBookFinder{}
	realUpdater := &mockProgressUpdater{
		meResponse: &hardcover.MeResponse{ID: 1, AccountPrivacySettingID: 5},
	}

	f, err := os.CreateTemp(t.TempDir(), "state-*.json")
	require.NoError(t, err)
	require.NoError(t, f.Close())
	st := state.New(f.Name())
	st.SetLastBookSync(999)
	st.SetLastConfigSync(888)
	matcher := NewMatcher(finder, false)
	logger := newNopLogger()

	engine := NewEngine(puller, finder, realUpdater, st, matcher, logger, true, nil)

	err = engine.FullSync(context.Background())
	require.NoError(t, err)

	// Timestamps should have been reset (and remain 0 since puller returns no records).
	assert.Equal(t, int64(0), st.GetLastBookSync())
	assert.Equal(t, int64(0), st.GetLastConfigSync())
}

// TestEngine_PendingProgressSync verifies that matched books with stored progress
// but no prior Hardcover sync get their progress pushed on the next tick.
func TestEngine_PendingProgressSync(t *testing.T) {
	puller := &mockReadestPuller{
		books:   []readest.DBBook{},
		configs: []readest.DBBookConfig{},
	}
	finder := &mockBookFinder{}
	updater := &mockProgressUpdater{
		meResponse: &hardcover.MeResponse{ID: 1, AccountPrivacySettingID: 5},
		userBook:   &hardcover.UserBook{ID: 42},
	}

	engine, st := newTestEngine(t, puller, finder, updater)

	// A book that was manually linked after its config was processed:
	// has progress stored but nothing sent to Hardcover yet.
	st.SetBook("hashPending", state.BookState{
		BookHash:        "hashPending",
		Title:           "Pending Book",
		HardcoverBookID: 500,
		HardcoverSlug:   "pending-book",
		EditionID:       100,
		EditionPages:    300,
		ReadestProgress: [2]int{150, 300},
		ReadestStatus:   "reading",
		// LastStatusSent and LastProgressSent are both 0
	})

	err := engine.Tick(context.Background())
	require.NoError(t, err)

	bs, ok := st.GetBook("hashPending")
	require.True(t, ok)
	assert.NotEqual(t, 0, bs.LastStatusSent, "status should have been sent to Hardcover")
	assert.NotEqual(t, 0, bs.LastProgressSent, "progress should have been sent to Hardcover")
}

// TestEngine_ProcessConfig_UnmatchedStoresProgress verifies that configs for
// unmatched books still store the progress in state.
func TestEngine_ProcessConfig_UnmatchedStoresProgress(t *testing.T) {
	puller := &mockReadestPuller{
		books: []readest.DBBook{},
		configs: []readest.DBBookConfig{
			{
				BookHash:  "hashUnmatched",
				Progress:  "[200, 500]",
				UpdatedAt: "2024-06-15T10:00:00Z",
			},
		},
	}
	finder := &mockBookFinder{}
	updater := &mockProgressUpdater{
		meResponse: &hardcover.MeResponse{ID: 1, AccountPrivacySettingID: 5},
	}

	engine, st := newTestEngine(t, puller, finder, updater)

	// Book exists in state but is unmatched.
	st.SetBook("hashUnmatched", state.BookState{
		BookHash:  "hashUnmatched",
		Title:     "Unmatched Book",
		Unmatched: true,
	})

	err := engine.Tick(context.Background())
	require.NoError(t, err)

	bs, ok := st.GetBook("hashUnmatched")
	require.True(t, ok)
	assert.Equal(t, [2]int{200, 500}, bs.ReadestProgress, "progress should be stored even for unmatched books")
	assert.Empty(t, updater.insertUserBookCalls, "should not write to Hardcover for unmatched books")
}

// TestEngine_WithOptions verifies that NewEngine accepts EngineOptions.
func TestEngine_WithOptions(t *testing.T) {
	puller := &mockReadestPuller{books: []readest.DBBook{}, configs: []readest.DBBookConfig{}}
	finder := &mockBookFinder{}
	updater := &mockProgressUpdater{
		meResponse: &hardcover.MeResponse{ID: 1, AccountPrivacySettingID: 5},
	}

	f, err := os.CreateTemp(t.TempDir(), "state-*.json")
	require.NoError(t, err)
	require.NoError(t, f.Close())
	st := state.New(f.Name())

	eb := NewEventBus(10)
	engine := NewEngine(puller, finder, updater, st, NewMatcher(finder, false), newNopLogger(), false, &EngineOptions{
		Events:    eb,
		CoversDir: t.TempDir(),
	})

	err = engine.Tick(context.Background())
	require.NoError(t, err)

	// Verify events were emitted.
	ch := eb.Subscribe()
	defer eb.Unsubscribe(ch)
	// History should contain sync_start and sync_complete.
	var events []SyncEvent
	for {
		select {
		case e := <-ch:
			events = append(events, e)
		default:
			goto done
		}
	}
done:
	require.GreaterOrEqual(t, len(events), 2)
	assert.Equal(t, "sync_start", events[0].Type)
	assert.Equal(t, "sync_complete", events[len(events)-1].Type)
}

// TestEngine_ManualSync_TickUsesDryRun: In manual mode, Tick uses dry-run (no real writes).
func TestEngine_ManualSync_TickUsesDryRun(t *testing.T) {
	isbn := "9781234567890"
	pages := 400
	editionID := 10
	edition := &hardcover.Edition{
		ID:     editionID,
		BookID: 100,
		Pages:  &pages,
		ISBN13: isbn,
	}

	puller := &mockReadestPuller{
		books: []readest.DBBook{
			{
				BookHash:  "hashM1",
				Title:     "Manual Book",
				Metadata:  metaWithISBN(isbn),
				UpdatedAt: "2024-01-01T00:00:00Z",
			},
		},
		configs: []readest.DBBookConfig{
			{
				BookHash:  "hashM1",
				Progress:  "[200,400]",
				UpdatedAt: "2024-01-01T00:01:00Z",
			},
		},
	}

	finder := &mockBookFinder{
		byISBN13: map[string]*hardcover.Edition{isbn: edition},
	}
	realUpdater := &mockProgressUpdater{
		meResponse: &hardcover.MeResponse{ID: 1, AccountPrivacySettingID: 5},
	}

	f, err := os.CreateTemp(t.TempDir(), "state-*.json")
	require.NoError(t, err)
	require.NoError(t, f.Close())
	st := state.New(f.Name())
	matcher := NewMatcher(finder, false)
	logger := newNopLogger()

	engine := NewEngine(puller, finder, realUpdater, st, matcher, logger, true, nil)

	err = engine.Tick(context.Background())
	require.NoError(t, err)

	// In manual mode Tick uses dry-run: read calls pass through, but writes do not.
	assert.Empty(t, realUpdater.insertUserBookCalls, "manual mode Tick must not call real InsertUserBook")
	assert.Empty(t, realUpdater.updateUserBookCalls, "manual mode Tick must not call real UpdateUserBook")
	assert.Empty(t, realUpdater.insertUserReadCalls, "manual mode Tick must not call real InsertUserBookRead")
	assert.Empty(t, realUpdater.updateUserReadCalls, "manual mode Tick must not call real UpdateUserBookRead")

	bs, ok := st.GetBook("hashM1")
	require.True(t, ok)
	assert.Equal(t, [2]int{200, 400}, bs.ReadestProgress)
	assert.Equal(t, hardcover.StatusNone, bs.LastStatusSent)
	assert.Equal(t, 0, bs.LastProgressSent)
	assert.Equal(t, 0, bs.UserBookID)
}

func TestEngine_ManualSync_CompletionNotificationUsesReconciledStatus(t *testing.T) {
	puller := &mockReadestPuller{
		books: []readest.DBBook{},
		configs: []readest.DBBookConfig{
			{
				BookHash:  "hashManualComplete",
				Progress:  "[400,400]",
				UpdatedAt: "2024-01-01T00:01:00Z",
			},
		},
	}
	finder := &mockBookFinder{}
	realUpdater := &mockProgressUpdater{
		meResponse: &hardcover.MeResponse{ID: 1, AccountPrivacySettingID: 5},
		userBook: &hardcover.UserBook{
			ID:       55,
			BookID:   100,
			StatusID: hardcover.StatusCurrentlyReading,
		},
	}
	notifier := &mockNotifier{}

	f, err := os.CreateTemp(t.TempDir(), "state-*.json")
	require.NoError(t, err)
	require.NoError(t, f.Close())
	st := state.New(f.Name())
	matcher := NewMatcher(finder, false)
	logger := newNopLogger()
	engine := NewEngine(puller, finder, realUpdater, st, matcher, logger, true, &EngineOptions{
		Notifier: notifier,
	})
	st.SetBook("hashManualComplete", state.BookState{
		BookHash:         "hashManualComplete",
		Title:            "Manual Complete",
		HardcoverBookID:  100,
		EditionID:        10,
		EditionPages:     400,
		UserBookID:       55,
		UserBookReadID:   99,
		LastStatusSent:   hardcover.StatusCurrentlyReading,
		LastProgressSent: 200,
	})

	require.NoError(t, engine.Tick(context.Background()))
	assert.Empty(t, notifier.completed, "dry-run polling should not notify completion")
	bs, ok := st.GetBook("hashManualComplete")
	require.True(t, ok)
	assert.Equal(t, hardcover.StatusRead, bs.LastStatusSent, "dry-run polling records the pending read status locally")

	require.NoError(t, engine.SyncNow(context.Background()))

	require.Len(t, realUpdater.updateUserBookCalls, 1)
	assert.Equal(t, hardcover.StatusRead, realUpdater.updateUserBookCalls[0].statusID)
	require.Len(t, notifier.completed, 1)
	assert.Equal(t, "Manual Complete", notifier.completed[0].Title)
}

// TestEngine_ConcurrentTick_SecondReturnsNil: Calling Tick while another is in
// progress should return nil immediately (skip).
func TestEngine_ConcurrentTick_SecondReturnsNil(t *testing.T) {
	// Use a slow puller to keep the first Tick busy.
	blocker := make(chan struct{})
	entered := make(chan struct{}, 1)
	slowPuller := &blockingPuller{ready: blocker, entered: entered}

	finder := &mockBookFinder{}
	updater := &mockProgressUpdater{
		meResponse: &hardcover.MeResponse{ID: 1, AccountPrivacySettingID: 5},
	}

	f, err := os.CreateTemp(t.TempDir(), "state-*.json")
	require.NoError(t, err)
	require.NoError(t, f.Close())
	st := state.New(f.Name())
	matcher := NewMatcher(finder, false)
	logger := newNopLogger()
	engine := NewEngine(slowPuller, finder, updater, st, matcher, logger, false, nil)

	// Start first Tick in background — it will block on PullBooks.
	firstDone := make(chan error, 1)
	go func() {
		firstDone <- engine.Tick(context.Background())
	}()

	// Wait for the first Tick to enter PullBooks (meaning it holds the lock).
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("first Tick did not enter PullBooks")
	}

	// Second Tick should return nil immediately (skipped).
	err = engine.Tick(context.Background())
	assert.NoError(t, err, "second concurrent Tick should return nil")

	// Unblock the first Tick.
	close(blocker)
	require.NoError(t, <-firstDone)
}

// blockingPuller blocks PullBooks until the ready channel is closed.
// It signals on entered when PullBooks is called (so callers know the lock is held).
type blockingPuller struct {
	ready   chan struct{}
	entered chan struct{}
}

func (b *blockingPuller) PullBooks(_ context.Context, _ int64) ([]readest.DBBook, error) {
	if b.entered != nil {
		select {
		case b.entered <- struct{}{}:
		default:
		}
	}
	<-b.ready
	return nil, nil
}

func (b *blockingPuller) PullConfigs(_ context.Context, _ int64) ([]readest.DBBookConfig, error) {
	return nil, nil
}

// TestEngine_DeletedBook_Skipped: A book with DeletedAt set should be skipped.
func TestEngine_DeletedBook_Skipped(t *testing.T) {
	deletedAt := "2024-01-01T00:00:00Z"
	puller := &mockReadestPuller{
		books: []readest.DBBook{
			{
				BookHash:  "hashDel",
				Title:     "Deleted Book",
				UpdatedAt: "2024-01-01T00:00:00Z",
				DeletedAt: &deletedAt,
			},
		},
		configs: []readest.DBBookConfig{},
	}
	finder := &mockBookFinder{}
	updater := &mockProgressUpdater{
		meResponse: &hardcover.MeResponse{ID: 1, AccountPrivacySettingID: 5},
	}

	engine, st := newTestEngine(t, puller, finder, updater)
	err := engine.Tick(context.Background())
	require.NoError(t, err)

	_, ok := st.GetBook("hashDel")
	assert.False(t, ok, "deleted book should not be added to state")
}

// TestEngine_DeletedConfig_Skipped: A config with DeletedAt set should be skipped.
func TestEngine_DeletedConfig_Skipped(t *testing.T) {
	deletedAt := "2024-01-01T00:00:00Z"
	puller := &mockReadestPuller{
		books: []readest.DBBook{},
		configs: []readest.DBBookConfig{
			{
				BookHash:  "hashDelCfg",
				Progress:  "[100,400]",
				UpdatedAt: "2024-01-01T00:00:00Z",
				DeletedAt: &deletedAt,
			},
		},
	}
	finder := &mockBookFinder{}
	updater := &mockProgressUpdater{
		meResponse: &hardcover.MeResponse{ID: 1, AccountPrivacySettingID: 5},
	}

	engine, _ := newTestEngine(t, puller, finder, updater)

	// Pre-populate a matched book so the config would be processed if not deleted.
	engine.state.SetBook("hashDelCfg", state.BookState{
		BookHash:        "hashDelCfg",
		Title:           "Some Book",
		HardcoverBookID: 500,
		EditionID:       50,
		EditionPages:    400,
		UserBookID:      9,
	})

	err := engine.Tick(context.Background())
	require.NoError(t, err)

	// No Hardcover write calls should have been made.
	assert.Empty(t, updater.insertUserBookCalls)
	assert.Empty(t, updater.updateUserBookCalls)
	assert.Empty(t, updater.insertUserReadCalls)
	assert.Empty(t, updater.updateUserReadCalls)
}

// TestEngine_ProcessBook_UpdatesReadestStatus: Book already in state; ReadingStatus
// changed — verify state is updated but no Hardcover calls made.
func TestEngine_ProcessBook_UpdatesReadestStatus(t *testing.T) {
	puller := &mockReadestPuller{
		books: []readest.DBBook{
			{
				BookHash:      "hashRS",
				Title:         "Status Book",
				UpdatedAt:     "2024-02-01T00:00:00Z",
				ReadingStatus: "finished",
			},
		},
		configs: []readest.DBBookConfig{},
	}
	finder := &mockBookFinder{}
	updater := &mockProgressUpdater{
		meResponse: &hardcover.MeResponse{ID: 1, AccountPrivacySettingID: 5},
	}

	engine, st := newTestEngine(t, puller, finder, updater)
	st.SetBook("hashRS", state.BookState{
		BookHash:        "hashRS",
		Title:           "Status Book",
		HardcoverBookID: 600,
		ReadestStatus:   "reading",
		LastStatusSent:  2, // already synced as "currently reading"
	})

	err := engine.Tick(context.Background())
	require.NoError(t, err)

	bs, ok := st.GetBook("hashRS")
	require.True(t, ok)
	assert.Equal(t, "finished", bs.ReadestStatus, "ReadestStatus should be updated in state")

	// processBook only updates state — no Hardcover writes.
	assert.Empty(t, updater.insertUserBookCalls)
	assert.Empty(t, updater.updateUserBookCalls)
}

// TestEngine_ParseTimestamp covers the various parseTimestamp branches.
func TestEngine_ParseTimestamp(t *testing.T) {
	tests := []struct {
		name  string
		input string
		check func(t *testing.T, got int64)
	}{
		{
			name:  "empty string returns 0",
			input: "",
			check: func(t *testing.T, got int64) { assert.Equal(t, int64(0), got) },
		},
		{
			name:  "RFC3339 string",
			input: "2024-06-15T10:00:00Z",
			check: func(t *testing.T, got int64) { assert.Greater(t, got, int64(0)) },
		},
		{
			name:  "non-TZ format",
			input: "2024-06-15T10:00:00",
			check: func(t *testing.T, got int64) { assert.Greater(t, got, int64(0)) },
		},
		{
			name:  "unparseable string returns 0",
			input: "not-a-timestamp",
			check: func(t *testing.T, got int64) { assert.Equal(t, int64(0), got) },
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseTimestamp(tt.input)
			tt.check(t, got)
		})
	}
}

// TestEngine_StatusName covers known and unknown status IDs.
func TestEngine_StatusName(t *testing.T) {
	engine := &Engine{
		statusNames: map[int]string{
			1: "Want to Read",
			2: "Currently Reading",
			3: "Read",
		},
	}
	assert.Equal(t, "Want to Read", engine.statusName(1))
	assert.Equal(t, "Currently Reading", engine.statusName(2))
	assert.Equal(t, "Read", engine.statusName(3))
	assert.Equal(t, "unknown(99)", engine.statusName(99))
}

// TestEngine_PullBooks_Error: PullBooks returns an error — Tick should propagate it.
func TestEngine_PullBooks_Error(t *testing.T) {
	puller := &mockReadestPuller{
		bookErr: errors.New("readest down"),
	}
	finder := &mockBookFinder{}
	updater := &mockProgressUpdater{
		meResponse: &hardcover.MeResponse{ID: 1, AccountPrivacySettingID: 5},
	}

	engine, _ := newTestEngine(t, puller, finder, updater)
	err := engine.Tick(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "readest down")
}

// TestEngine_PullConfigs_Error: PullConfigs returns an error — Tick should propagate it.
func TestEngine_PullConfigs_Error(t *testing.T) {
	puller := &mockReadestPuller{
		books:  []readest.DBBook{},
		cfgErr: errors.New("config fetch failed"),
	}
	finder := &mockBookFinder{}
	updater := &mockProgressUpdater{
		meResponse: &hardcover.MeResponse{ID: 1, AccountPrivacySettingID: 5},
	}

	engine, _ := newTestEngine(t, puller, finder, updater)
	err := engine.Tick(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "config fetch failed")
}

// TestEngine_GetMe_Error: GetMe returns an error — Tick should propagate it.
func TestEngine_GetMe_Error(t *testing.T) {
	puller := &mockReadestPuller{
		books:   []readest.DBBook{},
		configs: []readest.DBBookConfig{},
	}
	finder := &mockBookFinder{}
	updater := &mockProgressUpdater{
		meErr: errors.New("auth failed"),
	}

	engine, _ := newTestEngine(t, puller, finder, updater)
	err := engine.Tick(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "auth failed")
}

// TestEngine_ProcessBook_DownloadsCover: When coversDir is set and a book matches with a cover URL,
// the cover should be downloaded.
func TestEngine_ProcessBook_DownloadsCover(t *testing.T) {
	coverSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("fake-cover"))
	}))
	defer coverSrv.Close()

	pages := 300
	puller := &mockReadestPuller{
		books: []readest.DBBook{
			{
				BookHash:  "hashCover",
				Title:     "Cover Book",
				Author:    "Author C",
				UpdatedAt: "2024-06-15T10:00:00Z",
			},
		},
		configs: []readest.DBBookConfig{},
	}
	finder := &mockBookFinder{
		bySlug: map[string]*hardcover.Book{
			"cover-book": {
				ID:   100,
				Slug: "cover-book",
				CachedImage: &hardcover.CachedImage{
					URL: coverSrv.URL + "/cover.jpg",
				},
				DefaultEbookEdition: &hardcover.Edition{
					ID:    10,
					Pages: &pages,
				},
			},
		},
	}
	updater := &mockProgressUpdater{
		meResponse: &hardcover.MeResponse{ID: 1, AccountPrivacySettingID: 5},
	}

	coversDir := t.TempDir()
	engine, st := newTestEngine(t, puller, finder, updater)
	engine.coversDir = coversDir

	// Also set the metadata so identifier.Parse can extract slug.
	meta := `{"identifier":"test","altIdentifier":[{"scheme":"HARDCOVER","value":"cover-book"}]}`
	puller.books[0].Metadata = &meta

	err := engine.Tick(context.Background())
	require.NoError(t, err)

	bs, ok := st.GetBook("hashCover")
	require.True(t, ok)
	assert.NotEmpty(t, bs.CoverPath, "cover should have been downloaded")
	assert.Equal(t, coverSrv.URL+"/cover.jpg", bs.CoverURL)
}

// TestEngine_BackfillCovers: Matched books without covers get them on next tick.
func TestEngine_BackfillCovers(t *testing.T) {
	coverSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("fake-cover"))
	}))
	defer coverSrv.Close()

	pages := 300
	puller := &mockReadestPuller{books: []readest.DBBook{}, configs: []readest.DBBookConfig{}}
	finder := &mockBookFinder{
		bySlug: map[string]*hardcover.Book{
			"backfill-book": {
				ID:   200,
				Slug: "backfill-book",
				CachedImage: &hardcover.CachedImage{
					URL: coverSrv.URL + "/cover.jpg",
				},
				BookSeries: []hardcover.BookSeriesEntry{
					{Series: hardcover.SeriesInfo{Name: "Test Series"}, Position: 1},
				},
				DefaultEbookEdition: &hardcover.Edition{
					ID:    20,
					Pages: &pages,
				},
			},
		},
	}
	updater := &mockProgressUpdater{
		meResponse: &hardcover.MeResponse{ID: 1, AccountPrivacySettingID: 5},
	}

	coversDir := t.TempDir()
	engine, st := newTestEngine(t, puller, finder, updater)
	engine.coversDir = coversDir

	// Pre-populate a matched book WITHOUT a cover.
	st.SetBook("hashBackfill", state.BookState{
		BookHash:        "hashBackfill",
		Title:           "Backfill Book",
		HardcoverBookID: 200,
		HardcoverSlug:   "backfill-book",
		LastStatusSent:  2,
	})
	// Also add an unmatched book and one that already has both cover and series — should be skipped.
	st.SetBook("hashUnmatched", state.BookState{
		BookHash:  "hashUnmatched",
		Title:     "Unmatched",
		Unmatched: true,
	})
	st.SetBook("hashComplete", state.BookState{
		BookHash:        "hashComplete",
		Title:           "Complete Book",
		HardcoverBookID: 300,
		HardcoverSlug:   "complete-book",
		CoverPath:       "complete.jpg",
		CoverURL:        "https://assets.example.com/complete.jpg",
		Series:          "Some Series",
		LastStatusSent:  2,
	})
	st.SetBook("hashStandaloneComplete", state.BookState{
		BookHash:        "hashStandaloneComplete",
		Title:           "Standalone Complete Book",
		HardcoverBookID: 301,
		HardcoverSlug:   "standalone-complete-book",
		CoverPath:       "standalone-complete.jpg",
		CoverURL:        "https://assets.example.com/standalone-complete.jpg",
		LastStatusSent:  2,
	})

	err := engine.Tick(context.Background())
	require.NoError(t, err)

	bs, ok := st.GetBook("hashBackfill")
	require.True(t, ok)
	assert.NotEmpty(t, bs.CoverPath, "cover should have been backfilled")
	assert.Equal(t, coverSrv.URL+"/cover.jpg", bs.CoverURL)
	assert.NotContains(t, finder.bySlugCalls, "standalone-complete-book")
}

// TestEngine_BackfillCovers_NoCoverURL: Book without cover URL is skipped.
func TestEngine_BackfillCovers_NoCoverURL(t *testing.T) {
	puller := &mockReadestPuller{books: []readest.DBBook{}, configs: []readest.DBBookConfig{}}
	finder := &mockBookFinder{
		bySlug: map[string]*hardcover.Book{
			"no-cover-slug": {
				ID:   300,
				Slug: "no-cover-slug",
				// No CachedImage
			},
		},
	}
	updater := &mockProgressUpdater{
		meResponse: &hardcover.MeResponse{ID: 1, AccountPrivacySettingID: 5},
	}

	engine, st := newTestEngine(t, puller, finder, updater)
	engine.coversDir = t.TempDir()

	st.SetBook("hashNoCover", state.BookState{
		BookHash:        "hashNoCover",
		Title:           "No Cover Book",
		HardcoverBookID: 300,
		HardcoverSlug:   "no-cover-slug",
		LastStatusSent:  2,
	})
	// Book whose slug doesn't exist on Hardcover — FindBookBySlug returns nil.
	st.SetBook("hashNotFound", state.BookState{
		BookHash:        "hashNotFound",
		Title:           "Not Found Book",
		HardcoverBookID: 400,
		HardcoverSlug:   "slug-not-in-finder",
		LastStatusSent:  2,
	})

	err := engine.Tick(context.Background())
	require.NoError(t, err)

	bs, ok := st.GetBook("hashNoCover")
	require.True(t, ok)
	assert.Empty(t, bs.CoverPath, "should not have a cover path")
}

// TestEngine_BackfillCovers_FinderError: Finder error is logged, not fatal.
func TestEngine_BackfillCovers_FinderError(t *testing.T) {
	puller := &mockReadestPuller{books: []readest.DBBook{}, configs: []readest.DBBookConfig{}}
	finder := &mockBookFinder{bySlugErr: errors.New("lookup failed")}
	updater := &mockProgressUpdater{
		meResponse: &hardcover.MeResponse{ID: 1, AccountPrivacySettingID: 5},
	}

	engine, st := newTestEngine(t, puller, finder, updater)
	engine.coversDir = t.TempDir()

	st.SetBook("hashFE", state.BookState{
		BookHash:        "hashFE",
		Title:           "Finder Error Book",
		HardcoverBookID: 400,
		HardcoverSlug:   "finder-error",
		LastStatusSent:  2,
	})

	err := engine.Tick(context.Background())
	require.NoError(t, err) // Should not fail
}

// TestEngine_BackfillCovers_DownloadError: Cover download fails gracefully.
func TestEngine_BackfillCovers_DownloadError(t *testing.T) {
	// Server that returns 500 for cover requests.
	coverSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer coverSrv.Close()

	puller := &mockReadestPuller{books: []readest.DBBook{}, configs: []readest.DBBookConfig{}}
	finder := &mockBookFinder{
		bySlug: map[string]*hardcover.Book{
			"dl-error-book": {
				ID:   500,
				Slug: "dl-error-book",
				CachedImage: &hardcover.CachedImage{
					URL: coverSrv.URL + "/cover.jpg",
				},
			},
		},
	}
	updater := &mockProgressUpdater{
		meResponse: &hardcover.MeResponse{ID: 1, AccountPrivacySettingID: 5},
	}

	engine, st := newTestEngine(t, puller, finder, updater)
	engine.coversDir = t.TempDir()

	st.SetBook("hashDLErr", state.BookState{
		BookHash:        "hashDLErr",
		Title:           "DL Error Book",
		HardcoverBookID: 500,
		HardcoverSlug:   "dl-error-book",
		LastStatusSent:  2,
	})

	err := engine.Tick(context.Background())
	require.NoError(t, err) // Should not fail

	bs, ok := st.GetBook("hashDLErr")
	require.True(t, ok)
	assert.Empty(t, bs.CoverPath, "should not have cover after download error")
}

// TestEngine_ProcessConfig_NothingChanged: Config with same progress as already sent — skipped.
func TestEngine_ProcessConfig_NothingChanged(t *testing.T) {
	puller := &mockReadestPuller{
		books: []readest.DBBook{},
		configs: []readest.DBBookConfig{
			{
				BookHash:  "hashSame",
				Progress:  "[200, 400]",
				UpdatedAt: "2024-06-15T10:00:00Z",
			},
		},
	}
	finder := &mockBookFinder{}
	updater := &mockProgressUpdater{
		meResponse: &hardcover.MeResponse{ID: 1, AccountPrivacySettingID: 5},
	}

	engine, st := newTestEngine(t, puller, finder, updater)
	st.SetBook("hashSame", state.BookState{
		BookHash:         "hashSame",
		Title:            "Same Book",
		HardcoverBookID:  100,
		EditionID:        10,
		EditionPages:     400,
		LastStatusSent:   2,   // Already sent as "reading"
		LastProgressSent: 200, // Already sent this progress
		ReadestProgress:  [2]int{200, 400},
	})

	err := engine.Tick(context.Background())
	require.NoError(t, err)

	// No Hardcover writes should have happened.
	assert.Empty(t, updater.insertUserBookCalls)
	assert.Empty(t, updater.updateUserBookCalls)
}

// TestEngine_ProcessConfig_InvalidProgress: Malformed progress string — processConfig should error.
func TestEngine_ProcessConfig_InvalidProgress(t *testing.T) {
	puller := &mockReadestPuller{
		books: []readest.DBBook{},
		configs: []readest.DBBookConfig{
			{
				BookHash:  "hashBadProg",
				Progress:  "not-json",
				UpdatedAt: "2024-06-15T10:00:00Z",
			},
		},
	}
	finder := &mockBookFinder{}
	updater := &mockProgressUpdater{
		meResponse: &hardcover.MeResponse{ID: 1, AccountPrivacySettingID: 5},
	}

	engine, st := newTestEngine(t, puller, finder, updater)
	st.SetBook("hashBadProg", state.BookState{
		BookHash:        "hashBadProg",
		Title:           "Bad Progress",
		HardcoverBookID: 100,
	})

	// Tick should log error but not fail entirely.
	err := engine.Tick(context.Background())
	require.NoError(t, err) // Tick continues past individual config errors.
}

// TestEngine_ProcessConfig_BookNotInState: Config for unknown book is silently skipped.
func TestEngine_ProcessConfig_BookNotInState(t *testing.T) {
	puller := &mockReadestPuller{
		books: []readest.DBBook{},
		configs: []readest.DBBookConfig{
			{
				BookHash:  "hashUnknown",
				Progress:  "[10, 100]",
				UpdatedAt: "2024-06-15T10:00:00Z",
			},
		},
	}
	finder := &mockBookFinder{}
	updater := &mockProgressUpdater{
		meResponse: &hardcover.MeResponse{ID: 1, AccountPrivacySettingID: 5},
	}

	engine, _ := newTestEngine(t, puller, finder, updater)
	// Don't add "hashUnknown" to state.

	err := engine.Tick(context.Background())
	require.NoError(t, err)
}

// TestEngine_GetStatuses_Error: GetStatuses returns an error — Tick should propagate it.
func TestEngine_GetStatuses_Error(t *testing.T) {
	puller := &mockReadestPuller{
		books:   []readest.DBBook{},
		configs: []readest.DBBookConfig{},
	}
	finder := &mockBookFinder{}
	updater := &mockProgressUpdater{
		meResponse:  &hardcover.MeResponse{ID: 1, AccountPrivacySettingID: 5},
		statusesErr: errors.New("statuses unavailable"),
	}

	engine, _ := newTestEngine(t, puller, finder, updater)
	err := engine.Tick(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "statuses unavailable")
}

func TestEngine_CriticalErrorNotificationOncePerDay(t *testing.T) {
	puller := &mockReadestPuller{
		books:   []readest.DBBook{},
		bookErr: errors.New("readest unavailable"),
	}
	finder := &mockBookFinder{}
	updater := &mockProgressUpdater{
		meResponse: &hardcover.MeResponse{ID: 1, AccountPrivacySettingID: 5},
	}
	notifier := &mockNotifier{}

	engine, _ := newTestEngine(t, puller, finder, updater)
	engine.notifier = notifier

	require.Error(t, engine.Tick(context.Background()))
	require.Error(t, engine.Tick(context.Background()))

	require.Len(t, notifier.critical, 1)
	assert.Contains(t, notifier.critical[0].Error(), "readest unavailable")
}

func TestEngine_CriticalErrorNotificationRepeatsNextDay(t *testing.T) {
	notifier := &mockNotifier{}
	currentTime := time.Date(2026, 5, 13, 12, 0, 0, 0, time.UTC)
	engine := &Engine{
		notifier: notifier,
		logger:   newNopLogger(),
		now: func() time.Time {
			return currentTime
		},
	}
	err := errors.New("readest unavailable")

	engine.notifyCriticalError(context.Background(), err)
	engine.notifyCriticalError(context.Background(), err)
	currentTime = currentTime.Add(24 * time.Hour)
	engine.notifyCriticalError(context.Background(), err)

	require.Len(t, notifier.critical, 2)
	assert.Contains(t, notifier.critical[0].Error(), "readest unavailable")
	assert.Contains(t, notifier.critical[1].Error(), "readest unavailable")
}

func TestEngine_ContextCancellationDoesNotNotifyCriticalError(t *testing.T) {
	puller := &mockReadestPuller{
		books:   []readest.DBBook{},
		bookErr: context.Canceled,
	}
	finder := &mockBookFinder{}
	updater := &mockProgressUpdater{
		meResponse: &hardcover.MeResponse{ID: 1, AccountPrivacySettingID: 5},
	}
	notifier := &mockNotifier{}

	engine, _ := newTestEngine(t, puller, finder, updater)
	engine.notifier = notifier

	require.ErrorIs(t, engine.Tick(context.Background()), context.Canceled)

	assert.Empty(t, notifier.critical)
}

func TestEngine_ContextDeadlineDoesNotNotifyCriticalError(t *testing.T) {
	puller := &mockReadestPuller{
		books:   []readest.DBBook{},
		bookErr: context.DeadlineExceeded,
	}
	finder := &mockBookFinder{}
	updater := &mockProgressUpdater{
		meResponse: &hardcover.MeResponse{ID: 1, AccountPrivacySettingID: 5},
	}
	notifier := &mockNotifier{}

	engine, _ := newTestEngine(t, puller, finder, updater)
	engine.notifier = notifier

	require.ErrorIs(t, engine.SyncNow(context.Background()), context.DeadlineExceeded)

	assert.Empty(t, notifier.critical)
}

func TestEngine_CriticalErrorNotificationOncePerDayPerError(t *testing.T) {
	notifier := &mockNotifier{}
	engine := &Engine{
		notifier: notifier,
		logger:   newNopLogger(),
	}

	engine.notifyCriticalError(context.Background(), errors.New("readest unavailable"))
	engine.notifyCriticalError(context.Background(), errors.New("readest unavailable"))
	engine.notifyCriticalError(context.Background(), errors.New("hardcover unavailable"))

	require.Len(t, notifier.critical, 2)
	assert.Contains(t, notifier.critical[0].Error(), "readest unavailable")
	assert.Contains(t, notifier.critical[1].Error(), "hardcover unavailable")
}

func TestEngine_CriticalErrorNotificationFailureDoesNotConsumeThrottle(t *testing.T) {
	notifier := &mockNotifier{err: errors.New("slack unavailable")}
	engine := &Engine{
		notifier: notifier,
		logger:   newNopLogger(),
	}

	err := errors.New("readest unavailable")
	engine.notifyCriticalError(context.Background(), err)
	notifier.err = nil
	engine.notifyCriticalError(context.Background(), err)
	engine.notifyCriticalError(context.Background(), err)

	require.Len(t, notifier.critical, 2)
	assert.Contains(t, notifier.critical[0].Error(), "readest unavailable")
	assert.Contains(t, notifier.critical[1].Error(), "readest unavailable")
}

// TestEngine_ProcessConfig_NoEditionPages: EditionPages == 0 — should update status
// but skip progress update.
func TestEngine_ProcessConfig_NoEditionPages(t *testing.T) {
	puller := &mockReadestPuller{
		books: []readest.DBBook{},
		configs: []readest.DBBookConfig{
			{
				BookHash:  "hashNP",
				Progress:  "[200,400]",
				UpdatedAt: "2024-03-01T00:00:00Z",
			},
		},
	}
	finder := &mockBookFinder{}
	updater := &mockProgressUpdater{
		meResponse: &hardcover.MeResponse{ID: 1, AccountPrivacySettingID: 5},
	}

	engine, st := newTestEngine(t, puller, finder, updater)
	st.SetBook("hashNP", state.BookState{
		BookHash:        "hashNP",
		Title:           "No Pages Book",
		HardcoverBookID: 700,
		EditionID:       70,
		EditionPages:    0, // no pages known
	})

	err := engine.Tick(context.Background())
	require.NoError(t, err)

	// Status should be updated (InsertUserBook with status 2).
	require.Len(t, updater.insertUserBookCalls, 1)
	assert.Equal(t, 2, updater.insertUserBookCalls[0].statusID)

	// No progress read calls.
	assert.Empty(t, updater.insertUserReadCalls, "no InsertUserBookRead when EditionPages==0")
	assert.Empty(t, updater.updateUserReadCalls, "no UpdateUserBookRead when EditionPages==0")
}

func TestEngine_ProcessConfig_NoEditionPages_NotifiesCompleted(t *testing.T) {
	puller := &mockReadestPuller{
		books: []readest.DBBook{},
		configs: []readest.DBBookConfig{
			{
				BookHash:  "hashNPDone",
				Progress:  "[400,400]",
				UpdatedAt: "2024-03-01T00:00:00Z",
			},
		},
	}
	finder := &mockBookFinder{}
	updater := &mockProgressUpdater{
		meResponse: &hardcover.MeResponse{ID: 1, AccountPrivacySettingID: 5},
	}
	notifier := &mockNotifier{}

	engine, st := newTestEngine(t, puller, finder, updater)
	engine.notifier = notifier
	st.SetBook("hashNPDone", state.BookState{
		BookHash:        "hashNPDone",
		Title:           "No Pages Done",
		HardcoverBookID: 701,
		EditionID:       71,
		EditionPages:    0,
	})

	err := engine.Tick(context.Background())
	require.NoError(t, err)

	require.Len(t, updater.insertUserBookCalls, 1)
	assert.Equal(t, hardcover.StatusRead, updater.insertUserBookCalls[0].statusID)
	assert.Empty(t, updater.insertUserReadCalls)
	require.Len(t, notifier.completed, 1)
	assert.Equal(t, "No Pages Done", notifier.completed[0].Title)
}

func TestEngine_CompletedNotificationFailureDoesNotFailTick(t *testing.T) {
	puller := &mockReadestPuller{
		books: []readest.DBBook{},
		configs: []readest.DBBookConfig{
			{
				BookHash:  "hashCompleteNotifyFails",
				Progress:  "[400,400]",
				UpdatedAt: "2024-03-01T00:00:00Z",
			},
		},
	}
	finder := &mockBookFinder{}
	updater := &mockProgressUpdater{
		meResponse: &hardcover.MeResponse{ID: 1, AccountPrivacySettingID: 5},
	}
	notifier := &mockNotifier{err: errors.New("slack unavailable")}

	engine, st := newTestEngine(t, puller, finder, updater)
	engine.notifier = notifier
	st.SetBook("hashCompleteNotifyFails", state.BookState{
		BookHash:          "hashCompleteNotifyFails",
		Title:             "Complete Notify Fails",
		HardcoverBookID:   703,
		EditionID:         73,
		EditionPages:      400,
		UserBookID:        55,
		UserBookEditionID: 73,
		UserBookReadID:    99,
		LastStatusSent:    hardcover.StatusCurrentlyReading,
		LastProgressSent:  200,
	})

	require.NoError(t, engine.Tick(context.Background()))

	require.Len(t, notifier.completed, 1)
	assert.Empty(t, notifier.critical)
	bs, ok := st.GetBook("hashCompleteNotifyFails")
	require.True(t, ok)
	assert.Equal(t, hardcover.StatusRead, bs.LastStatusSent)
	assert.Equal(t, 400, bs.LastProgressSent)
}

func TestEngine_ProcessConfig_DoesNotNotifyCompletedWhenSaveFails(t *testing.T) {
	puller := &mockReadestPuller{
		books: []readest.DBBook{},
		configs: []readest.DBBookConfig{
			{
				BookHash:  "hashCompleteSaveFails",
				Progress:  "[400,400]",
				UpdatedAt: "2024-03-01T00:00:00Z",
			},
		},
	}
	finder := &mockBookFinder{}
	updater := &mockProgressUpdater{
		meResponse: &hardcover.MeResponse{ID: 1, AccountPrivacySettingID: 5},
	}
	notifier := &mockNotifier{}
	st := state.New(filepath.Join(t.TempDir(), "missing", "state.json"))
	engine := NewEngine(puller, finder, updater, st, NewMatcher(finder, false), newNopLogger(), false, &EngineOptions{
		Notifier: notifier,
	})
	st.SetBook("hashCompleteSaveFails", state.BookState{
		BookHash:        "hashCompleteSaveFails",
		Title:           "Complete Save Fails",
		HardcoverBookID: 702,
		EditionID:       72,
		EditionPages:    0,
	})

	err := engine.Tick(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "save sync state")

	require.Len(t, updater.insertUserBookCalls, 1)
	assert.Equal(t, hardcover.StatusRead, updater.insertUserBookCalls[0].statusID)
	assert.Empty(t, notifier.completed)
	require.Len(t, notifier.critical, 1)
	assert.Contains(t, notifier.critical[0].Error(), "save sync state")
}

// TestEngine_ProcessConfig_ExistingUserBook: GetUserBook returns an existing record —
// InsertUserBook should NOT be called.
func TestEngine_ProcessConfig_ExistingUserBook(t *testing.T) {
	existingUserBook := &hardcover.UserBook{ID: 55, BookID: 800, StatusID: 1}
	puller := &mockReadestPuller{
		books: []readest.DBBook{},
		configs: []readest.DBBookConfig{
			{
				BookHash:  "hashEUB",
				Progress:  "[100,400]",
				UpdatedAt: "2024-03-02T00:00:00Z",
			},
		},
	}
	finder := &mockBookFinder{}
	updater := &mockProgressUpdater{
		meResponse: &hardcover.MeResponse{ID: 1, AccountPrivacySettingID: 5},
		userBook:   existingUserBook,
	}

	engine, st := newTestEngine(t, puller, finder, updater)
	pages := 400
	st.SetBook("hashEUB", state.BookState{
		BookHash:        "hashEUB",
		Title:           "Existing UB Book",
		HardcoverBookID: 800,
		EditionID:       80,
		EditionPages:    pages,
	})

	err := engine.Tick(context.Background())
	require.NoError(t, err)

	assert.Empty(t, updater.insertUserBookCalls, "InsertUserBook must not be called when user book already exists")

	// The existing user book ID should be used for the read.
	require.Len(t, updater.insertUserReadCalls, 1)
	assert.Equal(t, 55, updater.insertUserReadCalls[0].userBookID)
}

func TestEngine_ProcessConfig_UsesExistingReadForLinkedEdition(t *testing.T) {
	editionID := 80
	existingProgress := 25
	existingUserBook := &hardcover.UserBook{
		ID:       55,
		BookID:   800,
		StatusID: hardcover.StatusWantToRead,
		UserBookReads: []hardcover.UserBookRead{
			{ID: 123, EditionID: &editionID, ProgressPages: &existingProgress},
		},
	}
	puller := &mockReadestPuller{
		books: []readest.DBBook{},
		configs: []readest.DBBookConfig{
			{
				BookHash:  "hashExistingRead",
				Progress:  "[100,400]",
				UpdatedAt: "2024-03-02T00:00:00Z",
			},
		},
	}
	finder := &mockBookFinder{}
	updater := &mockProgressUpdater{
		meResponse: &hardcover.MeResponse{ID: 1, AccountPrivacySettingID: 5},
		userBook:   existingUserBook,
	}

	engine, st := newTestEngine(t, puller, finder, updater)
	st.SetBook("hashExistingRead", state.BookState{
		BookHash:        "hashExistingRead",
		Title:           "Existing Read Book",
		HardcoverBookID: 800,
		EditionID:       editionID,
		EditionPages:    400,
	})

	err := engine.Tick(context.Background())
	require.NoError(t, err)

	assert.Empty(t, updater.insertUserReadCalls, "existing edition-specific read should be updated, not duplicated")
	require.Len(t, updater.updateUserReadCalls, 1)
	assert.Equal(t, 123, updater.updateUserReadCalls[0].id)
	assert.Equal(t, 100, updater.updateUserReadCalls[0].progressPages)

	require.Len(t, updater.updateUserBookCalls, 1)
	assert.Equal(t, hardcover.StatusCurrentlyReading, updater.updateUserBookCalls[0].statusID)
	require.NotNil(t, updater.updateUserBookCalls[0].editionID)
	assert.Equal(t, editionID, *updater.updateUserBookCalls[0].editionID)

	bs, ok := st.GetBook("hashExistingRead")
	require.True(t, ok)
	assert.Equal(t, 123, bs.UserBookReadID)
	assert.Equal(t, 100, bs.LastProgressSent)
}

func TestEngine_ProcessConfig_ReplacesStaleReadWithLinkedEditionRead(t *testing.T) {
	editionID := 80
	staleProgress := 64
	existingUserBook := &hardcover.UserBook{
		ID:        55,
		BookID:    800,
		StatusID:  hardcover.StatusCurrentlyReading,
		EditionID: &editionID,
		UserBookReads: []hardcover.UserBookRead{
			{ID: 5311406, ProgressPages: &staleProgress},
			{ID: 5311405, EditionID: &editionID},
		},
	}
	puller := &mockReadestPuller{
		books: []readest.DBBook{},
		configs: []readest.DBBookConfig{
			{
				BookHash:  "hashStaleRead",
				Progress:  "[80,400]",
				UpdatedAt: "2024-03-02T00:00:00Z",
			},
		},
	}
	finder := &mockBookFinder{}
	updater := &mockProgressUpdater{
		meResponse: &hardcover.MeResponse{ID: 1, AccountPrivacySettingID: 5},
		userBook:   existingUserBook,
	}

	engine, st := newTestEngine(t, puller, finder, updater)
	st.SetBook("hashStaleRead", state.BookState{
		BookHash:         "hashStaleRead",
		Title:            "Stale Read Book",
		HardcoverBookID:  800,
		EditionID:        editionID,
		EditionPages:     400,
		UserBookID:       55,
		UserBookReadID:   5311406,
		LastStatusSent:   hardcover.StatusCurrentlyReading,
		LastProgressSent: staleProgress,
	})

	err := engine.Tick(context.Background())
	require.NoError(t, err)

	assert.Empty(t, updater.insertUserReadCalls, "stale editionless read should not be duplicated")
	require.Len(t, updater.updateUserReadCalls, 1)
	assert.Equal(t, 5311405, updater.updateUserReadCalls[0].id)
	assert.Equal(t, 80, updater.updateUserReadCalls[0].progressPages)

	bs, ok := st.GetBook("hashStaleRead")
	require.True(t, ok)
	assert.Equal(t, 5311405, bs.UserBookReadID)
	assert.Equal(t, 80, bs.LastProgressSent)
}

func TestSelectUserBookRead(t *testing.T) {
	editionID := 80
	otherEditionID := 81
	progressPages := 10

	tests := []struct {
		name          string
		reads         []hardcover.UserBookRead
		editionID     int
		currentReadID int
		wantID        int
		wantOK        bool
	}{
		{
			name:      "matching edition wins",
			editionID: editionID,
			reads: []hardcover.UserBookRead{
				{ID: 1},
				{ID: 2, EditionID: &editionID},
			},
			wantID: 2,
			wantOK: true,
		},
		{
			name:          "current read used when no edition match",
			editionID:     editionID,
			currentReadID: 3,
			reads: []hardcover.UserBookRead{
				{ID: 3, EditionID: &otherEditionID},
				{ID: 4},
			},
			wantID: 3,
			wantOK: true,
		},
		{
			name:      "editionless read fallback",
			editionID: editionID,
			reads: []hardcover.UserBookRead{
				{ID: 5, EditionID: &otherEditionID},
				{ID: 6, ProgressPages: &progressPages},
			},
			wantID: 6,
			wantOK: true,
		},
		{
			name:      "any read fallback",
			editionID: editionID,
			reads: []hardcover.UserBookRead{
				{ID: 7, EditionID: &otherEditionID},
			},
			wantID: 7,
			wantOK: true,
		},
		{
			name:   "no valid read",
			reads:  []hardcover.UserBookRead{{ID: 0}},
			wantOK: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := selectUserBookRead(tt.reads, tt.editionID, tt.currentReadID)
			assert.Equal(t, tt.wantOK, ok)
			assert.Equal(t, tt.wantID, got.ID)
		})
	}
}

// TestEngine_ProcessConfig_GetUserBook_Error: GetUserBook returns error — processConfig
// should return error and Tick should log it (but not stop overall).
func TestEngine_ProcessConfig_GetUserBook_Error(t *testing.T) {
	puller := &mockReadestPuller{
		books: []readest.DBBook{},
		configs: []readest.DBBookConfig{
			{
				BookHash:  "hashGUBErr",
				Progress:  "[100,400]",
				UpdatedAt: "2024-03-03T00:00:00Z",
			},
		},
	}
	finder := &mockBookFinder{}
	updater := &mockProgressUpdater{
		meResponse: &hardcover.MeResponse{ID: 1, AccountPrivacySettingID: 5},
		getUserErr: errors.New("get user book failed"),
	}

	engine, st := newTestEngine(t, puller, finder, updater)
	st.SetBook("hashGUBErr", state.BookState{
		BookHash:        "hashGUBErr",
		Title:           "Error Book",
		HardcoverBookID: 900,
		EditionID:       90,
		EditionPages:    400,
	})

	// Tick should succeed overall (error is logged, not propagated).
	err := engine.Tick(context.Background())
	require.NoError(t, err)
}

// TestEngine_ProcessConfig_InsertUserBook_Error: InsertUserBook returns error —
// processConfig returns error, logged by Tick.
func TestEngine_ProcessConfig_InsertUserBook_Error(t *testing.T) {
	puller := &mockReadestPuller{
		books: []readest.DBBook{},
		configs: []readest.DBBookConfig{
			{
				BookHash:  "hashIUBErr",
				Progress:  "[100,400]",
				UpdatedAt: "2024-03-04T00:00:00Z",
			},
		},
	}
	finder := &mockBookFinder{}
	updater := &mockProgressUpdater{
		meResponse:        &hardcover.MeResponse{ID: 1, AccountPrivacySettingID: 5},
		insertUserBookErr: errors.New("insert user book failed"),
	}

	engine, st := newTestEngine(t, puller, finder, updater)
	st.SetBook("hashIUBErr", state.BookState{
		BookHash:        "hashIUBErr",
		Title:           "Insert Error Book",
		HardcoverBookID: 901,
		EditionID:       91,
		EditionPages:    400,
	})

	err := engine.Tick(context.Background())
	require.NoError(t, err) // error is logged, Tick itself succeeds
}

// TestEngine_ProcessConfig_UpdateUserBook_Error: UpdateUserBook returns error.
func TestEngine_ProcessConfig_UpdateUserBook_Error(t *testing.T) {
	puller := &mockReadestPuller{
		books: []readest.DBBook{},
		configs: []readest.DBBookConfig{
			{
				BookHash:  "hashUUBErr",
				Progress:  "[100,400]",
				UpdatedAt: "2024-03-05T00:00:00Z",
			},
		},
	}
	finder := &mockBookFinder{}
	updater := &mockProgressUpdater{
		meResponse:        &hardcover.MeResponse{ID: 1, AccountPrivacySettingID: 5},
		updateUserBookErr: errors.New("update user book failed"),
	}

	engine, st := newTestEngine(t, puller, finder, updater)
	st.SetBook("hashUUBErr", state.BookState{
		BookHash:        "hashUUBErr",
		Title:           "Update Error Book",
		HardcoverBookID: 902,
		EditionID:       92,
		EditionPages:    400,
		UserBookID:      20,
		LastStatusSent:  1, // different from what DeriveStatus will return (2)
	})

	err := engine.Tick(context.Background())
	require.NoError(t, err) // error is logged, Tick itself succeeds
}

func TestEngine_ProcessConfig_UpdateUserBookRead_Error(t *testing.T) {
	puller := &mockReadestPuller{
		books: []readest.DBBook{},
		configs: []readest.DBBookConfig{
			{
				BookHash:  "hashUUBRErr",
				Progress:  "[200,400]",
				UpdatedAt: "2024-03-05T00:00:00Z",
			},
		},
	}
	finder := &mockBookFinder{}
	updater := &mockProgressUpdater{
		meResponse:        &hardcover.MeResponse{ID: 1, AccountPrivacySettingID: 5},
		updateUserReadErr: errors.New("update user book read failed"),
	}

	engine, st := newTestEngine(t, puller, finder, updater)
	st.SetBook("hashUUBRErr", state.BookState{
		BookHash:          "hashUUBRErr",
		Title:             "Update Read Error Book",
		HardcoverBookID:   903,
		EditionID:         93,
		EditionPages:      400,
		UserBookID:        21,
		UserBookEditionID: 93,
		UserBookReadID:    22,
		LastStatusSent:    hardcover.StatusCurrentlyReading,
		LastProgressSent:  100,
	})

	err := engine.Tick(context.Background())
	require.NoError(t, err) // error is logged, Tick itself succeeds

	require.Len(t, updater.updateUserReadCalls, 1)
	assert.Equal(t, 22, updater.updateUserReadCalls[0].id)
	assert.Equal(t, 200, updater.updateUserReadCalls[0].progressPages)
	bs, ok := st.GetBook("hashUUBRErr")
	require.True(t, ok)
	assert.Equal(t, 100, bs.LastProgressSent, "failed read update must not be recorded as sent")
}

// TestEngine_TimestampFromRecords: PullBooks returns records with updated_at times.
// Verify LastBookSync set to max of those, not wall clock.
func TestEngine_TimestampFromRecords(t *testing.T) {
	ts1 := "2024-06-15T10:00:00Z"
	ts2 := "2024-06-15T12:30:00Z" // max

	puller := &mockReadestPuller{
		books: []readest.DBBook{
			{
				BookHash:  "hashX",
				Title:     "Book X",
				UpdatedAt: ts1,
			},
			{
				BookHash:  "hashY",
				Title:     "Book Y",
				UpdatedAt: ts2,
			},
		},
		configs: []readest.DBBookConfig{},
	}

	finder := &mockBookFinder{} // no matches, but that's fine for this test
	updater := &mockProgressUpdater{
		meResponse: &hardcover.MeResponse{ID: 1, AccountPrivacySettingID: 5},
	}

	engine, st := newTestEngine(t, puller, finder, updater)

	err := engine.Tick(context.Background())
	require.NoError(t, err)

	// LastBookSync should be the ms timestamp of ts2.
	expectedMs := parseTimestamp(ts2)
	assert.Equal(t, expectedMs, st.GetLastBookSync())

	// Should NOT be zero and should NOT be wall clock (much larger value).
	assert.Greater(t, st.GetLastBookSync(), int64(0))
	assert.Less(t, st.GetLastBookSync(), int64(2000000000000)) // not a wall clock time from ~2033+
}

// TestEngine_BlockedTransition_DNF: Book marked as DNF on Hardcover should not
// be overwritten by a reading status from Readest.
func TestEngine_BlockedTransition_DNF(t *testing.T) {
	puller := &mockReadestPuller{
		books: []readest.DBBook{},
		configs: []readest.DBBookConfig{
			{
				BookHash: "hashDNF",
				Progress: "[100,400]",
			},
		},
	}
	finder := &mockBookFinder{}
	updater := &mockProgressUpdater{
		meResponse: &hardcover.MeResponse{ID: 1, AccountPrivacySettingID: 5},
	}

	engine, st := newTestEngine(t, puller, finder, updater)
	st.SetBook("hashDNF", state.BookState{
		BookHash:        "hashDNF",
		Title:           "DNF Book",
		HardcoverBookID: 999,
		EditionID:       99,
		EditionPages:    400,
		UserBookID:      50,
		LastStatusSent:  hardcover.StatusDidNotFinish,
	})

	err := engine.Tick(context.Background())
	require.NoError(t, err)

	// Status should remain DNF — no update calls.
	bs, _ := st.GetBook("hashDNF")
	assert.Equal(t, hardcover.StatusDidNotFinish, bs.LastStatusSent, "DNF status should not be overwritten")
	assert.Empty(t, updater.updateUserBookCalls, "no UpdateUserBook calls expected")
	assert.Empty(t, updater.insertUserReadCalls, "no InsertUserBookRead calls expected")
}
