package sync

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"testing"

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
	bySlug   map[string]*hardcover.Book
	byISBN13 map[string]*hardcover.Edition
	byISBN10 map[string]*hardcover.Edition
	search   []hardcover.Book
}

func (m *mockBookFinder) FindBookBySlug(_ context.Context, slug string) (*hardcover.Book, error) {
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
	id       int
	statusID int
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

	userBook   *hardcover.UserBook
	getUserErr error

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

func (m *mockProgressUpdater) GetMe(_ context.Context) (*hardcover.MeResponse, error) {
	return m.meResponse, m.meErr
}

func (m *mockProgressUpdater) GetStatuses(_ context.Context) ([]hardcover.BookStatus, error) {
	return []hardcover.BookStatus{
		{ID: 1, Status: "Want to Read"},
		{ID: 2, Status: "Currently Reading"},
		{ID: 3, Status: "Read"},
	}, nil
}

func (m *mockProgressUpdater) GetUserBook(_ context.Context, _ int) (*hardcover.UserBook, error) {
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

func (m *mockProgressUpdater) UpdateUserBook(_ context.Context, id, statusID int) (*hardcover.UserBook, error) {
	m.updateUserBookCalls = append(m.updateUserBookCalls, updateUserBookCall{id: id, statusID: statusID})
	if m.updateUserBookErr != nil {
		return nil, m.updateUserBookErr
	}
	if m.updatedUserBook != nil {
		return m.updatedUserBook, nil
	}
	return &hardcover.UserBook{ID: id, StatusID: statusID}, nil
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

	e := NewEngine(puller, finder, updater, st, matcher, logger, false)
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
		BookHash:         "hash2",
		Title:            "Existing Book",
		HardcoverBookID:  200,
		HardcoverSlug:    "existing-book",
		EditionID:        20,
		EditionPages:     600,
		UserBookID:       1,
		UserBookReadID:   1,
		LastStatusSent:   2,
		LastProgressSent: 150,
	})

	err := engine.Tick(context.Background())
	require.NoError(t, err)

	// Verify UpdateUserBookRead was called, not InsertUserBookRead.
	assert.Empty(t, updater.insertUserReadCalls, "InsertUserBookRead should not be called")
	require.Len(t, updater.updateUserReadCalls, 1)
	updateCall := updater.updateUserReadCalls[0]
	assert.Equal(t, 1, updateCall.id)
	// ConvertProgress(300, 600, 600) = 300
	assert.Equal(t, 300, updateCall.progressPages)
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
		BookHash:         "hash5",
		Title:            "Same Progress Book",
		HardcoverBookID:  500,
		EditionID:        50,
		EditionPages:     400,
		UserBookID:       10,
		UserBookReadID:   10,
		LastStatusSent:   2,
		LastProgressSent: 200, // same as what ConvertProgress would produce
	})

	err := engine.Tick(context.Background())
	require.NoError(t, err)

	// No Hardcover calls should be made.
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
	assert.Equal(t, expectedMs, st.LastBookSync)

	// Should NOT be zero and should NOT be wall clock (much larger value).
	assert.Greater(t, st.LastBookSync, int64(0))
	assert.Less(t, st.LastBookSync, int64(2000000000000)) // not a wall clock time from ~2033+
}
