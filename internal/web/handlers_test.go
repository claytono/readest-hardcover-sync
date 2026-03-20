package web

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"runtime"
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

// TestHandleUnlink_Success verifies that a linked book becomes unlinked.
func TestHandleUnlink_Success(t *testing.T) {
	st := makeState(t)
	st.SetBook("linked1", state.BookState{
		BookHash:        "linked1",
		Title:           "Linked Book",
		Author:          "Author X",
		HardcoverBookID: 777,
		HardcoverSlug:   "linked-book",
		EditionID:       999,
		EditionPages:    300,
		MatchMethod:     "isbn13",
		UserBookID:      42,
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
}

// TestHandleStatus_Empty verifies the status page renders with zero counts.
func TestHandleStatus_Empty(t *testing.T) {
	st := makeState(t)
	h := newTestHandlers(st)

	req := httptest.NewRequest(http.MethodGet, "/status", nil)
	rr := httptest.NewRecorder()
	h.handleStatus(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	if !strings.Contains(body, "never") {
		t.Errorf("expected 'never' for zero timestamps, got: %s", body)
	}
}

// TestHandleStatus_WithBooks verifies the status page counts matched and unmatched books.
func TestHandleStatus_WithBooks(t *testing.T) {
	st := makeState(t)
	st.SetBook("m1", state.BookState{BookHash: "m1", Title: "Matched One", HardcoverBookID: 10})
	st.SetBook("m2", state.BookState{BookHash: "m2", Title: "Matched Two", HardcoverBookID: 20})
	st.SetBook("u1", state.BookState{BookHash: "u1", Title: "Unmatched One", Unmatched: true})

	// Set non-zero sync timestamps.
	st.LastBookSync = time.Now().UnixMilli()
	st.LastConfigSync = time.Now().UnixMilli()

	h := newTestHandlers(st)

	req := httptest.NewRequest(http.MethodGet, "/status", nil)
	rr := httptest.NewRecorder()
	h.handleStatus(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	if !strings.Contains(body, "<dd>2</dd>") {
		t.Errorf("expected matched count '<dd>2</dd>' in body")
	}
	if !strings.Contains(body, "<dd>1</dd>") {
		t.Errorf("expected unmatched count '<dd>1</dd>' in body")
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
	)

	srv := NewServer(st, finder, updater, engine, ":0", logger)
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
	if loc := rr.Header().Get("Location"); loc != "/status" {
		t.Errorf("expected Location=/status, got %q", loc)
	}

	// Wait for the background goroutine to finish by polling for the state
	// file it writes. With stub implementations this completes in microseconds.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(stateFile); err == nil {
			return
		}
		runtime.Gosched()
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
	if !strings.Contains(body, "Sync Status") {
		t.Error("expected body to contain 'Sync Status'")
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
	if loc := rr.Header().Get("Location"); loc != "/status" {
		t.Errorf("expected Location=/status, got %q", loc)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(stateFile); err == nil {
			return
		}
		runtime.Gosched()
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
	if !strings.Contains(body, "Sync Status") {
		t.Error("expected body to contain 'Sync Status'")
	}
	if strings.Contains(body, "<!DOCTYPE html>") {
		t.Error("htmx response should not contain full HTML document")
	}
}

// TestHandleStatus_HtmxReturnsPartial verifies that an htmx GET /status
// returns just the status_content partial, not the full page.
func TestHandleStatus_HtmxReturnsPartial(t *testing.T) {
	st := makeState(t)
	h := newTestHandlers(st)

	req := httptest.NewRequest(http.MethodGet, "/status", nil)
	req.Header.Set("HX-Request", "true")
	rr := httptest.NewRecorder()
	h.handleStatus(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "Sync Status") {
		t.Error("expected body to contain 'Sync Status'")
	}
	if strings.Contains(body, "<!DOCTYPE html>") {
		t.Error("htmx response should not contain full HTML document")
	}
}
