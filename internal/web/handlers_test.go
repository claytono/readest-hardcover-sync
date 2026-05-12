package web

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/claytono/readest-hardcover-sync/internal/hardcover"
	"github.com/claytono/readest-hardcover-sync/internal/identifier"
	"github.com/claytono/readest-hardcover-sync/internal/readest"
	"github.com/claytono/readest-hardcover-sync/internal/state"
	syncsvc "github.com/claytono/readest-hardcover-sync/internal/sync"
)

// --- stub implementations ---

type stubFinder struct {
	results  []hardcover.Book
	slugBook *hardcover.Book
}

func (s *stubFinder) FindBookBySlug(_ context.Context, _ string) (*hardcover.Book, error) {
	return s.slugBook, nil
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
func (s *stubUpdater) UpdateUserBook(_ context.Context, id int, statusID int, editionID *int) (*hardcover.UserBook, error) {
	return &hardcover.UserBook{ID: id, StatusID: statusID, EditionID: editionID}, nil
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
		nil,
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
		ctx:         context.Background(),
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
	tmpDir, err := os.MkdirTemp("", "link-sync-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	st := state.New(tmpDir + "/state.json")
	st.SetBook("abc123", state.BookState{
		BookHash:        "abc123",
		Title:           "Test Book",
		Author:          "Test Author",
		ReadestProgress: [2]int{100, 200},
		ReadestStatus:   "reading",
		Unmatched:       true,
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

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		book, _ = st.GetBook("abc123")
		if book.LastStatusSent != 0 && book.LastProgressSent != 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	book, _ = st.GetBook("abc123")
	if book.LastStatusSent == 0 {
		t.Error("expected scheduled sync to send status after link")
	}
	if book.LastProgressSent != 160 {
		t.Errorf("expected scheduled sync to send 160 Hardcover pages, got %d", book.LastProgressSent)
	}
}

// TestHandleRoot verifies that GET / redirects to /books.
func TestHandleRoot(t *testing.T) {
	st := makeState(t)
	h := newTestHandlers(st)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()
	h.handleRoot(rr, req)

	if rr.Code != http.StatusFound {
		t.Fatalf("expected 302, got %d", rr.Code)
	}
	if loc := rr.Header().Get("Location"); loc != "/books" {
		t.Errorf("expected Location=/books, got %q", loc)
	}
}

// TestHandleLinkModal_NotFound verifies that an unknown hash returns 404.
func TestHandleLinkModal_NotFound(t *testing.T) {
	st := makeState(t)
	h := newTestHandlers(st)

	req := httptest.NewRequest(http.MethodGet, "/books/nonexistent/link-modal", nil)
	req = setPathValue(req, "GET /books/{hash}/link-modal")
	rr := httptest.NewRecorder()
	h.handleLinkModal(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rr.Code)
	}
}

// TestHandleLinkModal_NoMetadata verifies the modal renders for a book without metadata.
func TestHandleLinkModal_NoMetadata(t *testing.T) {
	st := makeState(t)
	st.SetBook("hash1", state.BookState{
		BookHash: "hash1",
		Title:    "Plain Book",
		Author:   "Some Author",
	})
	h := newTestHandlers(st)

	req := httptest.NewRequest(http.MethodGet, "/books/hash1/link-modal", nil)
	req = setPathValue(req, "GET /books/{hash}/link-modal")
	rr := httptest.NewRecorder()
	h.handleLinkModal(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	if !strings.Contains(body, "Plain Book") {
		t.Errorf("expected body to contain book title")
	}
}

// TestHandleLinkModal_WithMetadataAndSeries verifies the modal includes series info when metadata is present.
func TestHandleLinkModal_WithMetadataAndSeries(t *testing.T) {
	st := makeState(t)
	meta := `{"series":"Wheel of Time","seriesIndex":1,"identifier":"9780765370044"}`
	st.SetBook("hash2", state.BookState{
		BookHash: "hash2",
		Title:    "The Eye of the World",
		Author:   "Robert Jordan",
		Metadata: &meta,
	})
	h := newTestHandlers(st)

	req := httptest.NewRequest(http.MethodGet, "/books/hash2/link-modal", nil)
	req = setPathValue(req, "GET /books/{hash}/link-modal")
	rr := httptest.NewRecorder()
	h.handleLinkModal(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	if !strings.Contains(body, "Wheel of Time") {
		t.Errorf("expected body to contain series name, got: %s", body)
	}
}

// TestHandleLinkModal_WithMetadataSeriesNoIndex verifies series without an index renders correctly.
func TestHandleLinkModal_WithMetadataSeriesNoIndex(t *testing.T) {
	st := makeState(t)
	meta := `{"series":"Some Series","seriesIndex":0}`
	st.SetBook("hash3", state.BookState{
		BookHash: "hash3",
		Title:    "A Book",
		Author:   "An Author",
		Metadata: &meta,
	})
	h := newTestHandlers(st)

	req := httptest.NewRequest(http.MethodGet, "/books/hash3/link-modal", nil)
	req = setPathValue(req, "GET /books/{hash}/link-modal")
	rr := httptest.NewRecorder()
	h.handleLinkModal(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	if !strings.Contains(body, "Some Series") {
		t.Errorf("expected body to contain series name without index, got: %s", body)
	}
}

// TestHandleSearch_EmptyQuery verifies that an empty query returns 200 with no body.
func TestHandleSearch_EmptyQuery(t *testing.T) {
	st := makeState(t)
	h := newTestHandlers(st)

	req := httptest.NewRequest(http.MethodGet, "/books/hash1/search", nil)
	req = setPathValue(req, "GET /books/{hash}/search")
	rr := httptest.NewRecorder()
	h.handleSearch(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	if rr.Body.Len() != 0 {
		t.Errorf("expected empty body for empty query, got: %s", rr.Body.String())
	}
}

// stubFinderWithResults is a stubFinder that returns predefined books.
type stubFinderWithResults struct {
	results []hardcover.Book
}

func (s *stubFinderWithResults) FindBookBySlug(_ context.Context, _ string) (*hardcover.Book, error) {
	return nil, nil
}
func (s *stubFinderWithResults) FindEditionByISBN13(_ context.Context, _ string) (*hardcover.Edition, error) {
	return nil, nil
}
func (s *stubFinderWithResults) FindEditionByISBN10(_ context.Context, _ string) (*hardcover.Edition, error) {
	return nil, nil
}
func (s *stubFinderWithResults) SearchBooks(_ context.Context, _ string) ([]hardcover.Book, error) {
	return s.results, nil
}

// newTestHandlersWithFinder creates handlers with a custom finder.
func newTestHandlersWithFinder(st *state.State, finder syncsvc.BookFinder) *handlers {
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
		nil,
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
		ctx:         context.Background(),
		statusNames: statusNames,
		logger:      logger,
	}
	h.loadTemplates()
	return h
}

// TestHandleSearch_WithResults verifies that search results are rendered.
func TestHandleSearch_WithResults(t *testing.T) {
	st := makeState(t)
	st.SetBook("searchhash", state.BookState{
		BookHash: "searchhash",
		Title:    "Dune",
		Author:   "Frank Herbert",
	})

	pages := 412
	isbn := "9780441013593"
	book := hardcover.Book{
		ID:    101,
		Title: "Dune",
		Slug:  "dune",
		Contributions: []hardcover.Contribution{
			{Author: hardcover.Author{Name: "Frank Herbert"}},
		},
		DefaultEbookEdition: &hardcover.Edition{
			ID:            201,
			EditionFormat: "ebook",
			ISBN13:        isbn,
			Pages:         &pages,
			Publisher:     &hardcover.Publisher{Name: "Ace Books"},
		},
	}

	finder := &stubFinderWithResults{results: []hardcover.Book{book}}
	h := newTestHandlersWithFinder(st, finder)

	req := httptest.NewRequest(http.MethodGet, "/books/searchhash/search?q=Dune", nil)
	req = setPathValue(req, "GET /books/{hash}/search")
	rr := httptest.NewRecorder()
	h.handleSearch(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	if !strings.Contains(body, "Dune") {
		t.Errorf("expected body to contain book title")
	}
	if !strings.Contains(body, "Frank Herbert") {
		t.Errorf("expected body to contain author name")
	}
}

// TestHandleSearch_WithPhysicalEditionOnly verifies the physical edition fallback for EditionID.
func TestHandleSearch_WithPhysicalEditionOnly(t *testing.T) {
	st := makeState(t)
	st.SetBook("physhash", state.BookState{
		BookHash: "physhash",
		Title:    "Foundation",
		Author:   "Isaac Asimov",
	})

	pages := 244
	book := hardcover.Book{
		ID:    102,
		Title: "Foundation",
		Slug:  "foundation",
		Contributions: []hardcover.Contribution{
			{Author: hardcover.Author{Name: "Isaac Asimov"}},
		},
		DefaultPhysicalEdition: &hardcover.Edition{
			ID:            301,
			EditionFormat: "Hardcover",
			ISBN13:        "9780553293357",
			ISBN10:        "0553293354",
			Pages:         &pages,
			Publisher:     &hardcover.Publisher{Name: "Bantam"},
		},
	}

	finder := &stubFinderWithResults{results: []hardcover.Book{book}}
	h := newTestHandlersWithFinder(st, finder)

	req := httptest.NewRequest(http.MethodGet, "/books/physhash/search?q=Foundation", nil)
	req = setPathValue(req, "GET /books/{hash}/search")
	rr := httptest.NewRecorder()
	h.handleSearch(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	if !strings.Contains(body, "Foundation") {
		t.Errorf("expected body to contain book title")
	}
}

// TestHandleSearch_WithSeries verifies that series info appears in search results.
func TestHandleSearch_WithSeries(t *testing.T) {
	st := makeState(t)
	st.SetBook("serieshash", state.BookState{
		BookHash: "serieshash",
		Title:    "A Game of Thrones",
		Author:   "George R.R. Martin",
	})

	book := hardcover.Book{
		ID:    103,
		Title: "A Game of Thrones",
		Slug:  "a-game-of-thrones",
		Contributions: []hardcover.Contribution{
			{Author: hardcover.Author{Name: "George R.R. Martin"}},
		},
		BookSeries: []hardcover.BookSeriesEntry{
			{Series: hardcover.SeriesInfo{Name: "A Song of Ice and Fire"}, Position: 1},
		},
		CachedImage: &hardcover.CachedImage{URL: "https://example.com/cover.jpg"},
	}

	finder := &stubFinderWithResults{results: []hardcover.Book{book}}
	h := newTestHandlersWithFinder(st, finder)

	req := httptest.NewRequest(http.MethodGet, "/books/serieshash/search?q=Game+of+Thrones", nil)
	req = setPathValue(req, "GET /books/{hash}/search")
	rr := httptest.NewRecorder()
	h.handleSearch(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	if !strings.Contains(body, "A Song of Ice and Fire") {
		t.Errorf("expected body to contain series name, got: %s", body)
	}
}

// stubUpdaterWithStatus returns a UserBook with a known status for book ID 101.
type stubUpdaterWithStatus struct {
	stubUpdater
}

func (s *stubUpdaterWithStatus) GetUserBook(_ context.Context, bookID int) (*hardcover.UserBook, error) {
	if bookID == 101 {
		return &hardcover.UserBook{ID: 1, BookID: bookID, StatusID: 2}, nil
	}
	return nil, nil
}

// newTestHandlersWithFinderAndUpdater creates handlers with custom finder and updater.
func newTestHandlersWithFinderAndUpdater(st *state.State, finder syncsvc.BookFinder, updater syncsvc.ProgressUpdater) *handlers {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	engine := syncsvc.NewEngine(
		&stubReadest{},
		finder,
		updater,
		st,
		syncsvc.NewMatcher(finder, false),
		logger,
		false,
		nil,
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
		ctx:         context.Background(),
		statusNames: statusNames,
		logger:      logger,
	}
	h.loadTemplates()
	return h
}

// TestHandleSearch_UserStatus verifies that user status is shown in search results.
func TestHandleSearch_UserStatus(t *testing.T) {
	st := makeState(t)
	st.SetBook("statushash", state.BookState{
		BookHash: "statushash",
		Title:    "Dune",
		Author:   "Frank Herbert",
	})

	book := hardcover.Book{
		ID:    101,
		Title: "Dune",
		Slug:  "dune",
		Contributions: []hardcover.Contribution{
			{Author: hardcover.Author{Name: "Frank Herbert"}},
		},
	}

	finder := &stubFinderWithResults{results: []hardcover.Book{book}}
	updater := &stubUpdaterWithStatus{}
	h := newTestHandlersWithFinderAndUpdater(st, finder, updater)

	req := httptest.NewRequest(http.MethodGet, "/books/statushash/search?q=Dune", nil)
	req = setPathValue(req, "GET /books/{hash}/search")
	rr := httptest.NewRecorder()
	h.handleSearch(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	if !strings.Contains(body, "Currently Reading") {
		t.Errorf("expected body to contain user status, got: %s", body)
	}
}

// TestHandleUnlink_NotFound verifies that unlinking a missing book returns 404.
func TestHandleUnlink_NotFound(t *testing.T) {
	st := makeState(t)
	h := newTestHandlers(st)

	req := httptest.NewRequest(http.MethodPost, "/books/missing/unlink", nil)
	req = setPathValue(req, "POST /books/{hash}/unlink")
	rr := httptest.NewRecorder()
	h.handleUnlink(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rr.Code)
	}
}

// TestHandleUnlink_Success verifies that a linked book becomes unlinked and all
// Hardcover-related fields are cleared.
func TestHandleUnlink_Success(t *testing.T) {
	st := makeState(t)
	st.SetBook("linked1", state.BookState{
		BookHash:         "linked1",
		Title:            "Linked Book",
		Author:           "Author X",
		HardcoverBookID:  777,
		HardcoverSlug:    "linked-book",
		EditionID:        999,
		EditionPages:     300,
		ReadingFormatID:  1,
		MatchMethod:      "isbn13",
		UserBookID:       42,
		UserBookReadID:   55,
		LastStatusSent:   2,
		LastProgressSent: 150,
	})
	h := newTestHandlers(st)

	req := httptest.NewRequest(http.MethodPost, "/books/linked1/unlink", nil)
	req = setPathValue(req, "POST /books/{hash}/unlink")
	rr := httptest.NewRecorder()
	h.handleUnlink(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rr.Code, rr.Body.String())
	}

	book, ok := st.GetBook("linked1")
	if !ok {
		t.Fatal("book not found in state after unlink")
	}
	if book.HardcoverBookID != 0 {
		t.Errorf("expected HardcoverBookID=0 after unlink, got %d", book.HardcoverBookID)
	}
	if book.HardcoverSlug != "" {
		t.Errorf("expected empty slug after unlink, got %q", book.HardcoverSlug)
	}
	if book.EditionID != 0 {
		t.Errorf("expected EditionID=0 after unlink, got %d", book.EditionID)
	}
	if book.MatchMethod != "" {
		t.Errorf("expected empty MatchMethod after unlink, got %q", book.MatchMethod)
	}
	if !book.Unmatched {
		t.Errorf("expected Unmatched=true after unlink")
	}
	// Verify newly-cleared fields.
	if book.UserBookReadID != 0 {
		t.Errorf("expected UserBookReadID=0 after unlink, got %d", book.UserBookReadID)
	}
	if book.LastStatusSent != 0 {
		t.Errorf("expected LastStatusSent=0 after unlink, got %d", book.LastStatusSent)
	}
	if book.LastProgressSent != 0 {
		t.Errorf("expected LastProgressSent=0 after unlink, got %d", book.LastProgressSent)
	}
	if book.ReadingFormatID != 0 {
		t.Errorf("expected ReadingFormatID=0 after unlink, got %d", book.ReadingFormatID)
	}
	// Preserved fields.
	if book.Title != "Linked Book" {
		t.Errorf("expected Title preserved, got %q", book.Title)
	}
	if book.Author != "Author X" {
		t.Errorf("expected Author preserved, got %q", book.Author)
	}
}

// TestHandleSidebarStatus_Empty verifies the sidebar status renders with zero counts.
func TestHandleSidebarStatus_Empty(t *testing.T) {
	st := makeState(t)
	h := newTestHandlers(st)

	req := httptest.NewRequest(http.MethodGet, "/sidebar-status", nil)
	rr := httptest.NewRecorder()
	h.handleSidebarStatus(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	if !strings.Contains(body, "never") {
		t.Errorf("expected 'never' for zero timestamps, got: %s", body)
	}
}

// TestHandleSidebarStatus_WithBooks verifies the sidebar status counts matched and unmatched books.
func TestHandleSidebarStatus_WithBooks(t *testing.T) {
	st := makeState(t)
	st.SetBook("m1", state.BookState{BookHash: "m1", Title: "Matched One", HardcoverBookID: 10})
	st.SetBook("m2", state.BookState{BookHash: "m2", Title: "Matched Two", HardcoverBookID: 20})
	st.SetBook("u1", state.BookState{BookHash: "u1", Title: "Unmatched One", Unmatched: true})

	// Set non-zero sync timestamps.
	st.SetLastBookSync(time.Now().UnixMilli())
	st.SetLastConfigSync(time.Now().UnixMilli())
	st.SetLastSyncRanAt(time.Now())

	h := newTestHandlers(st)

	req := httptest.NewRequest(http.MethodGet, "/sidebar-status", nil)
	rr := httptest.NewRecorder()
	h.handleSidebarStatus(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	if !strings.Contains(body, "2 books") {
		t.Errorf("expected matched count '2 books' in body, got: %s", body)
	}
	if !strings.Contains(body, "1 book") {
		t.Errorf("expected unmatched count '1 book' in body, got: %s", body)
	}
	if strings.Contains(body, "never") {
		t.Errorf("non-zero timestamps should not show 'never'")
	}
}

// TestScoreSearchResults_TitleMatch verifies exact title match scoring.
func TestScoreSearchResults_TitleMatch(t *testing.T) {
	book := state.BookState{Title: "Dune", Author: "Frank Herbert"}
	results := []enrichedSearchResult{
		{Title: "Dune", Author: "Frank Herbert"},
		{Title: "Dune Messiah", Author: "Frank Herbert"},
	}
	scoreSearchResults(results, book, identifier.ParsedIdentifiers{})

	if results[0].score <= results[1].score {
		t.Errorf("exact title match should score higher: got %d vs %d", results[0].score, results[1].score)
	}
}

// TestScoreSearchResults_AuthorMatch verifies author match scoring.
func TestScoreSearchResults_AuthorMatch(t *testing.T) {
	book := state.BookState{Title: "Some Book", Author: "Frank Herbert"}
	results := []enrichedSearchResult{
		{Title: "Some Book", Author: "Frank Herbert"},
		{Title: "Some Book", Author: "Someone Else"},
	}
	scoreSearchResults(results, book, identifier.ParsedIdentifiers{})

	if results[0].score <= results[1].score {
		t.Errorf("author match should score higher: got %d vs %d", results[0].score, results[1].score)
	}
}

// TestScoreSearchResults_ISBNMatch verifies ISBN match scoring.
func TestScoreSearchResults_ISBNMatch(t *testing.T) {
	isbn := "9780441013593"
	book := state.BookState{Title: "Dune", Author: "Frank Herbert"}
	ids := identifier.ParsedIdentifiers{ISBN13s: []string{isbn}}

	results := []enrichedSearchResult{
		{
			Title:  "Dune",
			Author: "Frank Herbert",
			EbookEdition: &editionInfo{
				ISBN13: isbn,
			},
		},
		{
			Title:  "Dune",
			Author: "Frank Herbert",
		},
	}
	scoreSearchResults(results, book, ids)

	if results[0].score <= results[1].score {
		t.Errorf("ISBN match should score higher: got %d vs %d", results[0].score, results[1].score)
	}
}

// TestScoreSearchResults_ISBN10Match verifies ISBN-10 match scoring on physical edition.
func TestScoreSearchResults_ISBN10Match(t *testing.T) {
	isbn10 := "0441013597"
	book := state.BookState{Title: "Dune", Author: "Frank Herbert"}
	ids := identifier.ParsedIdentifiers{ISBN10s: []string{isbn10}}

	results := []enrichedSearchResult{
		{
			Title:  "Dune",
			Author: "Frank Herbert",
			PhysicalEdition: &editionInfo{
				ISBN10: isbn10,
			},
		},
		{
			Title:  "Dune",
			Author: "Frank Herbert",
		},
	}
	scoreSearchResults(results, book, ids)

	if results[0].score <= results[1].score {
		t.Errorf("ISBN-10 match on physical edition should score higher: got %d vs %d", results[0].score, results[1].score)
	}
}

// TestScoreSearchResults_UserStatusBonus verifies user-status bonus scoring.
func TestScoreSearchResults_UserStatusBonus(t *testing.T) {
	book := state.BookState{Title: "Some Book", Author: "An Author"}
	results := []enrichedSearchResult{
		{Title: "Some Book", Author: "An Author", UserStatus: "Currently Reading"},
		{Title: "Some Book", Author: "An Author"},
	}
	scoreSearchResults(results, book, identifier.ParsedIdentifiers{})

	if results[0].score <= results[1].score {
		t.Errorf("user status bonus should score higher: got %d vs %d", results[0].score, results[1].score)
	}
}

// TestScoreSearchResults_AuthorContainsSubstring verifies partial author matching.
func TestScoreSearchResults_AuthorContainsSubstring(t *testing.T) {
	book := state.BookState{Title: "A Book", Author: "Herbert"}
	results := []enrichedSearchResult{
		{Title: "A Book", Author: "Frank Herbert"},
		{Title: "A Book", Author: "John Smith"},
	}
	scoreSearchResults(results, book, identifier.ParsedIdentifiers{})

	if results[0].score <= results[1].score {
		t.Errorf("partial author match should score higher: got %d vs %d", results[0].score, results[1].score)
	}
}

// TestNewServer verifies that NewServer returns a non-nil server.
func TestNewServer(t *testing.T) {
	st := makeState(t)
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
		nil,
	)

	srv := NewServer(context.Background(), st, finder, updater, engine, nil, ":0", "", logger)
	if srv == nil {
		t.Fatal("expected non-nil server")
	}
}

// TestHandleLink_InvalidBookID verifies that a missing or zero book_id returns 400.
func TestHandleLink_InvalidBookID(t *testing.T) {
	st := makeState(t)
	st.SetBook("abc", state.BookState{BookHash: "abc", Title: "T", Author: "A"})
	h := newTestHandlers(st)

	for _, tc := range []struct {
		name string
		form url.Values
	}{
		{"missing", url.Values{}},
		{"zero", url.Values{"book_id": {"0"}}},
		{"nan", url.Values{"book_id": {"notanumber"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/books/abc/link",
				strings.NewReader(tc.form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req = setPathValue(req, "POST /books/{hash}/link")
			rr := httptest.NewRecorder()
			h.handleLink(rr, req)
			if rr.Code != http.StatusBadRequest {
				t.Errorf("expected 400, got %d", rr.Code)
			}
		})
	}
}

// TestHandleLink_NewBookCreated verifies that linking a hash not already in state still succeeds.
// SetManualLink creates the entry from zero value, so the handler returns 200.
func TestHandleLink_NewBookCreated(t *testing.T) {
	st := makeState(t)
	h := newTestHandlers(st)
	h.engine = nil

	form := url.Values{"book_id": {"99"}, "slug": {"new-book"}}
	req := httptest.NewRequest(http.MethodPost, "/books/newbook/link",
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = setPathValue(req, "POST /books/{hash}/link")
	rr := httptest.NewRecorder()
	h.handleLink(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rr.Code, rr.Body.String())
	}

	book, ok := st.GetBook("newbook")
	if !ok {
		t.Fatal("book should exist in state after link")
	}
	if book.HardcoverBookID != 99 {
		t.Errorf("expected HardcoverBookID=99, got %d", book.HardcoverBookID)
	}
}

// stubFinderError returns an error from SearchBooks.
type stubFinderError struct{}

func (s *stubFinderError) FindBookBySlug(_ context.Context, _ string) (*hardcover.Book, error) {
	return nil, nil
}
func (s *stubFinderError) FindEditionByISBN13(_ context.Context, _ string) (*hardcover.Edition, error) {
	return nil, nil
}
func (s *stubFinderError) FindEditionByISBN10(_ context.Context, _ string) (*hardcover.Edition, error) {
	return nil, nil
}
func (s *stubFinderError) SearchBooks(_ context.Context, _ string) ([]hardcover.Book, error) {
	return nil, fmt.Errorf("search backend unavailable")
}

// TestHandleSearch_FinderError verifies that a search backend error returns 500.
func TestHandleSearch_FinderError(t *testing.T) {
	st := makeState(t)
	h := newTestHandlersWithFinder(st, &stubFinderError{})

	req := httptest.NewRequest(http.MethodGet, "/books/anyhash/search?q=Dune", nil)
	req = setPathValue(req, "GET /books/{hash}/search")
	rr := httptest.NewRecorder()
	h.handleSearch(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", rr.Code)
	}
}

// TestHandleSearch_ScoringWithMetadata verifies that results are scored and sorted when
// the book in state has metadata (exercises the metadata scoring branch).
func TestHandleSearch_ScoringWithMetadata(t *testing.T) {
	st := makeState(t)
	meta := `{"identifier":"9780441013593"}`
	st.SetBook("metahash", state.BookState{
		BookHash: "metahash",
		Title:    "Dune",
		Author:   "Frank Herbert",
		Metadata: &meta,
	})

	isbn := "9780441013593"
	pages1, pages2 := 412, 300
	books := []hardcover.Book{
		{
			ID:    200,
			Title: "Dune Messiah",
			Slug:  "dune-messiah",
			Contributions: []hardcover.Contribution{
				{Author: hardcover.Author{Name: "Frank Herbert"}},
			},
			DefaultPhysicalEdition: &hardcover.Edition{
				ID:     301,
				ISBN13: "9780441015221",
				Pages:  &pages2,
			},
		},
		{
			ID:    101,
			Title: "Dune",
			Slug:  "dune",
			Contributions: []hardcover.Contribution{
				{Author: hardcover.Author{Name: "Frank Herbert"}},
			},
			DefaultEbookEdition: &hardcover.Edition{
				ID:     201,
				ISBN13: isbn,
				Pages:  &pages1,
			},
		},
	}

	finder := &stubFinderWithResults{results: books}
	h := newTestHandlersWithFinder(st, finder)

	req := httptest.NewRequest(http.MethodGet, "/books/metahash/search?q=Dune", nil)
	req = setPathValue(req, "GET /books/{hash}/search")
	rr := httptest.NewRecorder()
	h.handleSearch(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	// "Dune" (with ISBN match) should appear before "Dune Messiah" in results.
	duneIdx := strings.Index(body, ">Dune<")
	duneMessiahIdx := strings.Index(body, "Dune Messiah")
	if duneIdx < 0 || duneMessiahIdx < 0 {
		t.Fatalf("expected both titles in body, got: %s", body)
	}
	if duneIdx > duneMessiahIdx {
		t.Errorf("expected 'Dune' to appear before 'Dune Messiah' after scoring")
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

	stateFile := tmpDir + "/state.json"
	st := state.New(stateFile)
	h := newTestHandlers(st)

	req := httptest.NewRequest(http.MethodPost, "/sync", nil)
	rr := httptest.NewRecorder()
	h.handleTriggerSync(rr, req)

	if rr.Code != http.StatusSeeOther {
		t.Errorf("expected redirect 303, got %d", rr.Code)
	}
	if loc := rr.Header().Get("Location"); loc != "/books" {
		t.Errorf("expected Location=/books, got %q", loc)
	}

	// Wait for the background goroutine to finish by polling for the state
	// file it writes. With stub implementations this completes in microseconds.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(stateFile); err == nil {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("background SyncNow goroutine did not complete within timeout")
}

// TestHandleTriggerSync_HtmxReturnsPartial verifies that an htmx POST /sync
// returns the status_content partial instead of a redirect.
func TestHandleTriggerSync_HtmxReturnsPartial(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "trigger-sync-htmx-*")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	st := state.New(tmpDir + "/state.json")
	h := newTestHandlers(st)

	req := httptest.NewRequest(http.MethodPost, "/sync", nil)
	req.Header.Set("HX-Request", "true")
	rr := httptest.NewRecorder()
	h.handleTriggerSync(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "sidebar-status") {
		t.Error("expected body to contain 'sidebar-status'")
	}
	if strings.Contains(body, "<!DOCTYPE html>") {
		t.Error("htmx response should not contain full HTML document")
	}
}

// TestHandleFullSync verifies that POST /full-sync redirects to /status.
func TestHandleFullSync(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "full-sync-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	stateFile := tmpDir + "/state.json"
	st := state.New(stateFile)
	h := newTestHandlers(st)

	req := httptest.NewRequest(http.MethodPost, "/full-sync", nil)
	rr := httptest.NewRecorder()
	h.handleFullSync(rr, req)

	if rr.Code != http.StatusSeeOther {
		t.Errorf("expected redirect 303, got %d", rr.Code)
	}
	if loc := rr.Header().Get("Location"); loc != "/books" {
		t.Errorf("expected Location=/books, got %q", loc)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(stateFile); err == nil {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("background FullSync goroutine did not complete within timeout")
}

// TestHandleFullSync_HtmxReturnsPartial verifies that an htmx POST /full-sync
// returns the status_content partial instead of a redirect.
func TestHandleFullSync_HtmxReturnsPartial(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "full-sync-htmx-*")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	st := state.New(tmpDir + "/state.json")
	h := newTestHandlers(st)

	req := httptest.NewRequest(http.MethodPost, "/full-sync", nil)
	req.Header.Set("HX-Request", "true")
	rr := httptest.NewRecorder()
	h.handleFullSync(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "sidebar-status") {
		t.Error("expected body to contain 'sidebar-status'")
	}
	if strings.Contains(body, "<!DOCTYPE html>") {
		t.Error("htmx response should not contain full HTML document")
	}
}

// TestHandleSidebarStatus_ReturnsPartial verifies that GET /sidebar-status
// returns just the sidebar status partial.
func TestHandleSidebarStatus_ReturnsPartial(t *testing.T) {
	st := makeState(t)
	h := newTestHandlers(st)

	req := httptest.NewRequest(http.MethodGet, "/sidebar-status", nil)
	rr := httptest.NewRecorder()
	h.handleSidebarStatus(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "sidebar-status") {
		t.Error("expected body to contain 'sidebar-status'")
	}
	if strings.Contains(body, "<!DOCTYPE html>") {
		t.Error("response should not contain full HTML document")
	}
}

// TestHandleBookDetail_Unmatched verifies the detail modal for an unmatched book.
func TestHandleBookDetail_Unmatched(t *testing.T) {
	st := makeState(t)
	st.SetBook("unmhash", state.BookState{
		BookHash:  "unmhash",
		Title:     "Unmatched Detail",
		Author:    "Author U",
		Unmatched: true,
	})
	h := newTestHandlers(st)

	req := httptest.NewRequest(http.MethodGet, "/books/unmhash/detail", nil)
	req = setPathValue(req, "GET /books/{hash}/detail")
	rr := httptest.NewRecorder()
	h.handleBookDetail(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "Link to Hardcover") {
		t.Error("expected Link button for unmatched book")
	}
}

// TestHandleTriggerSync_NonHtmx verifies redirect for non-htmx sync request.
func TestHandleTriggerSync_NonHtmx(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "sync-nonhtmx-*")
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
		t.Errorf("expected 303, got %d", rr.Code)
	}
}

// TestHandleFullSync_NonHtmx verifies redirect for non-htmx full sync request.
func TestHandleFullSync_NonHtmx(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "fullsync-nonhtmx-*")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	st := state.New(tmpDir + "/state.json")
	h := newTestHandlers(st)

	req := httptest.NewRequest(http.MethodPost, "/full-sync", nil)
	rr := httptest.NewRecorder()
	h.handleFullSync(rr, req)

	if rr.Code != http.StatusSeeOther {
		t.Errorf("expected 303, got %d", rr.Code)
	}
}

// TestHandleLinkModal_WithCover verifies the link modal renders with cover URL.
func TestHandleLinkModal_WithCover(t *testing.T) {
	meta := `{"identifier":"test","altIdentifier":[{"scheme":"HARDCOVER","value":"test-slug"}]}`
	st := makeState(t)
	st.SetBook("lmhash", state.BookState{
		BookHash:        "lmhash",
		Title:           "Link Modal Book",
		Author:          "Author LM",
		CoverPath:       "lmhash.jpg",
		HardcoverBookID: 10,
		HardcoverSlug:   "lm-book",
		MatchMethod:     "slug",
		Metadata:        &meta,
	})
	h := newTestHandlers(st)

	req := httptest.NewRequest(http.MethodGet, "/books/lmhash/link-modal", nil)
	req = setPathValue(req, "GET /books/{hash}/link-modal")
	rr := httptest.NewRecorder()
	h.handleLinkModal(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	if !strings.Contains(body, "Link Modal Book") {
		t.Error("expected title in modal")
	}
}

// TestHandleBooks_SortByActivity verifies books are sorted by LastActivityAt.
func TestHandleBooks_SortByActivity(t *testing.T) {
	st := makeState(t)
	st.SetBook("old", state.BookState{
		BookHash:        "old",
		Title:           "Old Book",
		Author:          "Author O",
		HardcoverBookID: 1,
		MatchMethod:     "slug",
		LastActivityAt:  1000,
	})
	st.SetBook("new", state.BookState{
		BookHash:        "new",
		Title:           "New Book",
		Author:          "Author N",
		HardcoverBookID: 2,
		MatchMethod:     "slug",
		LastActivityAt:  2000,
	})
	h := newTestHandlers(st)

	req := httptest.NewRequest(http.MethodGet, "/books", nil)
	rr := httptest.NewRecorder()
	h.handleBooks(rr, req)

	body := rr.Body.String()
	newIdx := strings.Index(body, "New Book")
	oldIdx := strings.Index(body, "Old Book")
	if newIdx == -1 || oldIdx == -1 {
		t.Fatal("expected both books in body")
	}
	if newIdx > oldIdx {
		t.Error("expected New Book (higher activity) to appear before Old Book")
	}
}

// TestHandleSSE_NilEventBus returns 503 when events are nil.
func TestHandleSSE_NilEventBus(t *testing.T) {
	st := makeState(t)
	h := newTestHandlers(st) // events is nil by default

	req := httptest.NewRequest(http.MethodGet, "/events", nil)
	rr := httptest.NewRecorder()
	h.handleSSE(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d", rr.Code)
	}
}

// TestHandleSSE_TooManySubscribers returns 503 when subscriber limit is reached.
func TestHandleSSE_TooManySubscribers(t *testing.T) {
	st := makeState(t)
	h := newTestHandlers(st)
	eb := syncsvc.NewEventBus(10)
	h.events = eb

	// Exhaust subscriber slots.
	var channels []chan syncsvc.SyncEvent
	for i := 0; i < 10; i++ {
		ch := eb.Subscribe()
		if ch != nil {
			channels = append(channels, ch)
		}
	}
	defer func() {
		for _, ch := range channels {
			eb.Unsubscribe(ch)
		}
	}()

	req := httptest.NewRequest(http.MethodGet, "/events", nil)
	rr := httptest.NewRecorder()
	h.handleSSE(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d", rr.Code)
	}
}

// TestHandleSSE_StreamsEvents verifies SSE handler streams events.
func TestHandleSSE_StreamsEvents(t *testing.T) {
	st := makeState(t)
	h := newTestHandlers(st)
	eb := syncsvc.NewEventBus(10)
	h.events = eb

	// Use a context we can cancel to end the SSE stream.
	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodGet, "/events", nil).WithContext(ctx)
	rr := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		h.handleSSE(rr, req)
		close(done)
	}()

	// Publish an event.
	eb.Publish(syncsvc.SyncEvent{Type: "test", Title: "hello"})

	// Give the handler a moment to write.
	time.Sleep(50 * time.Millisecond)
	cancel()
	<-done

	if ct := rr.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("expected Content-Type text/event-stream, got %q", ct)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "data:") {
		t.Errorf("expected SSE data line in body, got: %s", body)
	}
	if !strings.Contains(body, "hello") {
		t.Errorf("expected event data to contain 'hello', got: %s", body)
	}
}

// TestHandleBookDetail verifies the detail modal renders for a known book.
func TestHandleBookDetail(t *testing.T) {
	st := makeState(t)
	st.SetBook("detailhash", state.BookState{
		BookHash:        "detailhash",
		Title:           "Detail Book",
		Author:          "Author D",
		HardcoverBookID: 42,
		HardcoverSlug:   "detail-book",
		MatchMethod:     "slug",
	})
	h := newTestHandlers(st)

	req := httptest.NewRequest(http.MethodGet, "/books/detailhash/detail", nil)
	req = setPathValue(req, "GET /books/{hash}/detail")
	rr := httptest.NewRecorder()
	h.handleBookDetail(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	if !strings.Contains(body, "Detail Book") {
		t.Error("expected body to contain book title")
	}
	if !strings.Contains(body, "detail-book") {
		t.Error("expected body to contain slug")
	}
}

// TestHandleBookDetail_NotFound verifies 404 for unknown hash.
func TestHandleBookDetail_NotFound(t *testing.T) {
	st := makeState(t)
	h := newTestHandlers(st)

	req := httptest.NewRequest(http.MethodGet, "/books/unknown/detail", nil)
	req = setPathValue(req, "GET /books/{hash}/detail")
	rr := httptest.NewRecorder()
	h.handleBookDetail(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rr.Code)
	}
}

// TestHandleBooks_RendersCards verifies the books page renders card elements.
func TestHandleBooks_RendersCards(t *testing.T) {
	st := makeState(t)
	st.SetBook("card1", state.BookState{
		BookHash:        "card1",
		Title:           "Card Book",
		Author:          "Card Author",
		HardcoverBookID: 1,
		HardcoverSlug:   "card-book",
		MatchMethod:     "slug",
		ReadestProgress: [2]int{50, 100},
	})
	st.SetBook("card2", state.BookState{
		BookHash:  "card2",
		Title:     "Unmatched Card",
		Author:    "Author U",
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
	if !strings.Contains(body, "book-card") {
		t.Error("expected body to contain book-card elements")
	}
	if !strings.Contains(body, "Card Book") {
		t.Error("expected body to contain 'Card Book'")
	}
	if !strings.Contains(body, "Unmatched Card") {
		t.Error("expected body to contain 'Unmatched Card'")
	}
	if !strings.Contains(body, "50%") {
		t.Error("expected body to contain progress percentage")
	}
}

// TestHandleBooks_Empty verifies the books page renders with no books.
func TestHandleBooks_Empty(t *testing.T) {
	st := makeState(t)
	h := newTestHandlers(st)

	req := httptest.NewRequest(http.MethodGet, "/books", nil)
	rr := httptest.NewRecorder()
	h.handleBooks(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "No books found") {
		t.Error("expected empty state message")
	}
}

// TestHandleBooks_WithFinishedBook verifies finished book rendering.
func TestHandleBooks_WithFinishedBook(t *testing.T) {
	st := makeState(t)
	st.SetBook("fin1", state.BookState{
		BookHash:        "fin1",
		Title:           "Finished Book",
		Author:          "Author F",
		HardcoverBookID: 1,
		HardcoverSlug:   "finished-book",
		MatchMethod:     "slug",
		ReadestProgress: [2]int{100, 100},
		ReadestStatus:   "finished",
		CoverPath:       "fin1.jpg",
	})
	h := newTestHandlers(st)

	req := httptest.NewRequest(http.MethodGet, "/books", nil)
	rr := httptest.NewRecorder()
	h.handleBooks(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "Read") {
		t.Error("expected finished status in body")
	}
	if !strings.Contains(body, "/covers/fin1.jpg") {
		t.Error("expected cover URL in body")
	}
}

// TestHandleBookDetail_WithMetadata verifies detail modal renders identifiers and status name.
func TestHandleBookDetail_WithMetadata(t *testing.T) {
	meta := `{"identifier":"urn:isbn:9781234567890","altIdentifier":["mobi-asin:B012345678"]}`
	st := makeState(t)
	st.SetBook("metahash", state.BookState{
		BookHash:        "metahash",
		Title:           "Meta Book",
		Author:          "Author M",
		HardcoverBookID: 42,
		HardcoverSlug:   "meta-book",
		MatchMethod:     "isbn13",
		Metadata:        &meta,
		CoverPath:       "metahash.jpg",
		LastStatusSent:  2,
	})
	h := newTestHandlers(st)

	req := httptest.NewRequest(http.MethodGet, "/books/metahash/detail", nil)
	req = setPathValue(req, "GET /books/{hash}/detail")
	rr := httptest.NewRecorder()
	h.handleBookDetail(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	if !strings.Contains(body, "Meta Book") {
		t.Error("expected title in detail")
	}
	if !strings.Contains(body, "/covers/metahash.jpg") {
		t.Error("expected cover URL in detail")
	}
	if !strings.Contains(body, "Currently Reading") {
		t.Error("expected resolved status name 'Currently Reading' in detail")
	}
	if !strings.Contains(body, "ASIN") {
		t.Error("expected ASIN identifier in detail")
	}
}

// TestHandleSidebarStatus_RelativeTime verifies relative time formatting.
func TestHandleSidebarStatus_RelativeTime(t *testing.T) {
	tests := []struct {
		name     string
		ago      time.Duration
		contains string
	}{
		{"just now", 30 * time.Second, "just now"},
		{"minutes ago", 5 * time.Minute, "min ago"},
		{"1 hour ago", 1*time.Hour + 30*time.Minute, "1 hour ago"},
		{"hours ago", 3 * time.Hour, "hours ago"},
		{"yesterday", 30 * time.Hour, "yesterday"},
		{"days ago", 4 * 24 * time.Hour, "days ago"},
		{"old date", 8 * 24 * time.Hour, "UTC"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st := makeState(t)
			st.SetLastBookSync(time.Now().Add(-tc.ago).UnixMilli())
			h := newTestHandlers(st)

			req := httptest.NewRequest(http.MethodGet, "/sidebar-status", nil)
			rr := httptest.NewRecorder()
			h.handleSidebarStatus(rr, req)

			if rr.Code != http.StatusOK {
				t.Fatalf("expected 200, got %d", rr.Code)
			}
			body := rr.Body.String()
			if !strings.Contains(body, tc.contains) {
				t.Errorf("expected %q in body, got: %s", tc.contains, body)
			}
		})
	}
}

// TestHandleUnlink_ReturnsCard verifies unlink returns a card partial.
func TestHandleUnlink_ReturnsCard(t *testing.T) {
	st := makeState(t)
	st.SetBook("ulhash", state.BookState{
		BookHash:        "ulhash",
		Title:           "Unlink Me",
		Author:          "Author U",
		HardcoverBookID: 99,
		HardcoverSlug:   "unlink-me",
		MatchMethod:     "slug",
	})
	h := newTestHandlers(st)

	req := httptest.NewRequest(http.MethodPost, "/books/ulhash/unlink", nil)
	req = setPathValue(req, "POST /books/{hash}/unlink")
	rr := httptest.NewRecorder()
	h.handleUnlink(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	if !strings.Contains(body, "book-card") {
		t.Error("expected book-card in response")
	}
	if !strings.Contains(body, "Unmatched") {
		t.Error("expected Unmatched status after unlink")
	}
}

// TestHandleLink_WithCoverDownload verifies that linking downloads a cover server-side.
func TestHandleLink_WithCoverDownload(t *testing.T) {
	coverSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("fake-cover"))
	}))
	defer coverSrv.Close()

	st := makeState(t)
	st.SetBook("coverhash", state.BookState{BookHash: "coverhash", Title: "Cover Test", Author: "Author C"})

	finder := &stubFinder{
		slugBook: &hardcover.Book{
			ID:          55,
			Slug:        "cover-test",
			CachedImage: &hardcover.CachedImage{URL: coverSrv.URL + "/cover.jpg"},
			BookSeries:  []hardcover.BookSeriesEntry{{Series: hardcover.SeriesInfo{Name: "Test Series"}, Position: 1}},
		},
	}
	updater := &stubUpdater{}
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	statuses, _ := updater.GetStatuses(context.Background())
	statusNames := make(map[int]string, len(statuses))
	for _, s := range statuses {
		statusNames[s.ID] = s.Status
	}
	h := &handlers{
		state:       st,
		finder:      finder,
		updater:     updater,
		engine:      nil,
		ctx:         context.Background(),
		coversDir:   t.TempDir(),
		statusNames: statusNames,
		logger:      logger,
	}
	h.loadTemplates()

	form := url.Values{
		"book_id":       {"55"},
		"slug":          {"cover-test"},
		"edition_id":    {"100"},
		"edition_pages": {"200"},
	}
	req := httptest.NewRequest(http.MethodPost, "/books/coverhash/link",
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = setPathValue(req, "POST /books/{hash}/link")
	rr := httptest.NewRecorder()
	h.handleLink(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rr.Code, rr.Body.String())
	}

	book, ok := st.GetBook("coverhash")
	if !ok {
		t.Fatal("book should exist")
	}
	if book.HardcoverBookID != 55 {
		t.Errorf("expected HardcoverBookID=55, got %d", book.HardcoverBookID)
	}
	if book.CoverPath == "" {
		t.Error("expected cover to be downloaded")
	}
	if book.Series == "" {
		t.Error("expected series to be set")
	}
}

// TestHandleTriggerSync_NilEngine verifies sync gracefully handles nil engine (demo mode).
func TestHandleTriggerSync_NilEngine(t *testing.T) {
	st := makeState(t)
	h := newTestHandlers(st)
	h.engine = nil

	t.Run("htmx returns sidebar status", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/sync", nil)
		req.Header.Set("HX-Request", "true")
		rr := httptest.NewRecorder()
		h.handleTriggerSync(rr, req)

		if rr.Code != http.StatusOK {
			t.Errorf("expected 200, got %d", rr.Code)
		}
	})

	t.Run("non-htmx redirects", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/sync", nil)
		rr := httptest.NewRecorder()
		h.handleTriggerSync(rr, req)

		if rr.Code != http.StatusSeeOther {
			t.Errorf("expected 303, got %d", rr.Code)
		}
	})
}

// TestHandleFullSync_NilEngine verifies full sync gracefully handles nil engine (demo mode).
func TestHandleFullSync_NilEngine(t *testing.T) {
	st := makeState(t)
	h := newTestHandlers(st)
	h.engine = nil

	t.Run("htmx returns sidebar status", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/full-sync", nil)
		req.Header.Set("HX-Request", "true")
		rr := httptest.NewRecorder()
		h.handleFullSync(rr, req)

		if rr.Code != http.StatusOK {
			t.Errorf("expected 200, got %d", rr.Code)
		}
	})

	t.Run("non-htmx redirects", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/full-sync", nil)
		rr := httptest.NewRecorder()
		h.handleFullSync(rr, req)

		if rr.Code != http.StatusSeeOther {
			t.Errorf("expected 303, got %d", rr.Code)
		}
	})
}
