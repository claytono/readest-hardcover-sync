package web

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/claytono/readest-hardcover-sync/internal/hardcover"
	"github.com/claytono/readest-hardcover-sync/internal/readest"
	"github.com/claytono/readest-hardcover-sync/internal/state"
	syncsvc "github.com/claytono/readest-hardcover-sync/internal/sync"
)

// --- stub implementations ---

type stubFinder struct {
	results []hardcover.Book
}

func (s *stubFinder) FindBookBySlug(_ context.Context, _ string) (*hardcover.Book, error) {
	return nil, nil
}
func (s *stubFinder) FindEditionByISBN13(_ context.Context, _ string) (*hardcover.Edition, error) {
	return nil, nil
}
func (s *stubFinder) FindEditionByISBN10(_ context.Context, _ string) (*hardcover.Edition, error) {
	return nil, nil
}
func (s *stubFinder) SearchBooks(_ context.Context, _ string) ([]hardcover.Book, error) {
	return s.results, nil
}

type stubReadest struct{}

func (s *stubReadest) PullBooks(_ context.Context, _ int64) ([]readest.DBBook, error) {
	return nil, nil
}
func (s *stubReadest) PullConfigs(_ context.Context, _ int64) ([]readest.DBBookConfig, error) {
	return nil, nil
}

type stubUpdater struct{}

func (s *stubUpdater) GetMe(_ context.Context) (*hardcover.MeResponse, error) {
	return &hardcover.MeResponse{ID: 1, AccountPrivacySettingID: 1}, nil
}
func (s *stubUpdater) GetStatuses(_ context.Context) ([]hardcover.BookStatus, error) {
	return []hardcover.BookStatus{{ID: 2, Status: "Currently Reading"}}, nil
}
func (s *stubUpdater) GetUserBook(_ context.Context, _ int) (*hardcover.UserBook, error) {
	return nil, nil
}
func (s *stubUpdater) InsertUserBook(_ context.Context, bookID, statusID, privacySettingID int, editionID *int) (*hardcover.UserBook, error) {
	return &hardcover.UserBook{ID: 1, BookID: bookID, StatusID: statusID}, nil
}
func (s *stubUpdater) UpdateUserBook(_ context.Context, id int, statusID int) (*hardcover.UserBook, error) {
	return &hardcover.UserBook{ID: id, StatusID: statusID}, nil
}
func (s *stubUpdater) InsertUserBookRead(_ context.Context, userBookID, progressPages int, editionID *int, startedAt string, finishedAt *string) (*hardcover.UserBookRead, error) {
	return &hardcover.UserBookRead{ID: 1}, nil
}
func (s *stubUpdater) UpdateUserBookRead(_ context.Context, id int, progressPages int, finishedAt *string) (*hardcover.UserBookRead, error) {
	return &hardcover.UserBookRead{ID: id}, nil
}

// --- helpers ---

func makeState(t *testing.T) *state.State {
	t.Helper()
	st := state.New(t.TempDir() + "/state.json")
	return st
}

func newTestHandlers(st *state.State) *handlers {
	finder := &stubFinder{}
	updater := &stubUpdater{}
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	engine := syncsvc.NewEngine(
		&stubReadest{},
		finder,
		updater,
		st,
		syncsvc.NewMatcher(finder, false),
		logger,
		false,
	)
	statuses, _ := updater.GetStatuses(context.Background())
	statusNames := make(map[int]string, len(statuses))
	for _, s := range statuses {
		statusNames[s.ID] = s.Status
	}
	h := &handlers{
		state:       st,
		finder:      finder,
		updater:     updater,
		engine:      engine,
		statusNames: statusNames,
		logger:      logger,
	}
	h.loadTemplates()
	return h
}

// setPathValue injects path parameters via a temporary mux.
func setPathValue(r *http.Request, pattern string) *http.Request {
	mux := http.NewServeMux()
	var captured *http.Request
	mux.HandleFunc(pattern, func(w http.ResponseWriter, req *http.Request) {
		captured = req
	})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, r)
	if captured != nil {
		return captured
	}
	return r
}

// --- tests ---

// TestHandleBooks verifies that all books appear in the /books response.
func TestHandleBooks(t *testing.T) {
	st := makeState(t)

	st.SetBook("hash1", state.BookState{
		BookHash:        "hash1",
		Title:           "Alpha",
		Author:          "Author A",
		HardcoverBookID: 42,
		HardcoverSlug:   "alpha-book",
		MatchMethod:     "isbn13",
	})
	st.SetBook("hash2", state.BookState{
		BookHash:        "hash2",
		Title:           "Beta",
		Author:          "Author B",
		HardcoverBookID: 99,
		HardcoverSlug:   "beta-book",
		MatchMethod:     "manual",
	})
	st.SetBook("hash3", state.BookState{
		BookHash:  "hash3",
		Title:     "Gamma",
		Author:    "Author C",
		Unmatched: true,
	})

	h := newTestHandlers(st)

	req := httptest.NewRequest(http.MethodGet, "/books", nil)
	rr := httptest.NewRecorder()
	h.handleBooks(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}

	body := rr.Body.String()
	for _, title := range []string{"Alpha", "Beta", "Gamma"} {
		if !strings.Contains(body, title) {
			t.Errorf("expected body to contain %q", title)
		}
	}
}

// TestHandleLink verifies that POSTing link form values updates the state.
func TestHandleLink(t *testing.T) {
	st := makeState(t)
	st.SetBook("abc123", state.BookState{
		BookHash:  "abc123",
		Title:     "Test Book",
		Author:    "Test Author",
		Unmatched: true,
	})

	h := newTestHandlers(st)

	form := url.Values{
		"book_id":       {"555"},
		"slug":          {"test-book-slug"},
		"edition_id":    {"888"},
		"edition_pages": {"320"},
	}
	req := httptest.NewRequest(http.MethodPost, "/books/abc123/link",
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = setPathValue(req, "POST /books/{hash}/link")

	rr := httptest.NewRecorder()
	h.handleLink(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rr.Code, rr.Body.String())
	}

	book, ok := st.GetBook("abc123")
	if !ok {
		t.Fatal("book not found in state after link")
	}
	if book.HardcoverBookID != 555 {
		t.Errorf("expected HardcoverBookID=555, got %d", book.HardcoverBookID)
	}
	if book.HardcoverSlug != "test-book-slug" {
		t.Errorf("expected slug=test-book-slug, got %q", book.HardcoverSlug)
	}
	if book.EditionID != 888 {
		t.Errorf("expected EditionID=888, got %d", book.EditionID)
	}
	if book.EditionPages != 320 {
		t.Errorf("expected EditionPages=320, got %d", book.EditionPages)
	}
	if book.MatchMethod != "manual" {
		t.Errorf("expected MatchMethod=manual, got %q", book.MatchMethod)
	}
}

// TestHandleTriggerSync verifies that POST /sync redirects to /status.
func TestHandleTriggerSync(t *testing.T) {
	// Use os.MkdirTemp instead of t.TempDir() because the handler fires
	// engine.SyncNow in a goroutine that writes to the state file. With
	// t.TempDir(), Go cleans up the dir when the test returns, but the
	// goroutine may still be writing — causing a "directory not empty" error.
	tmpDir, err := os.MkdirTemp("", "trigger-sync-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	st := state.New(tmpDir + "/state.json")
	h := newTestHandlers(st)

	req := httptest.NewRequest(http.MethodPost, "/sync", nil)
	rr := httptest.NewRecorder()
	h.handleTriggerSync(rr, req)

	if rr.Code != http.StatusSeeOther {
		t.Errorf("expected redirect 303, got %d", rr.Code)
	}
	if loc := rr.Header().Get("Location"); loc != "/status" {
		t.Errorf("expected Location=/status, got %q", loc)
	}

	// Wait for the background goroutine to finish before cleanup.
	time.Sleep(100 * time.Millisecond)
}
