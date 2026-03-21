package web

import (
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/claytono/readest-hardcover-sync/internal/identifier"
	"github.com/claytono/readest-hardcover-sync/internal/state"
	syncsvc "github.com/claytono/readest-hardcover-sync/internal/sync"
)

type handlers struct {
	state       *state.State
	finder      syncsvc.BookFinder
	updater     syncsvc.ProgressUpdater
	engine      *syncsvc.Engine
	events      *syncsvc.EventBus
	ctx         context.Context // app lifecycle context for background work
	coversDir   string
	tmpl        *template.Template
	statusNames map[int]string
	logger      *slog.Logger
}

func (h *handlers) loadTemplates() {
	h.tmpl = template.Must(template.New("").ParseFS(templateFS, "templates/*.html"))
}

// handleRoot redirects to /books.
func (h *handlers) handleRoot(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/books", http.StatusFound)
}

// bookCardData extends BookState with computed fields for the card template.
type bookCardData struct {
	state.BookState
	ProgressPct int
	IsFinished  bool
	CoverURL    string // /covers/{file} or empty
}

type booksPageData struct {
	Books          []bookCardData
	Status         sidebarStatusData
	MatchedCount   int
	UnmatchedCount int
	TotalCount     int
}

func (h *handlers) buildBookCards() []bookCardData {
	all := h.state.ListBooks()

	// Sort by LastActivityAt descending, then alphabetically.
	sort.Slice(all, func(i, j int) bool {
		if all[i].LastActivityAt != all[j].LastActivityAt {
			return all[i].LastActivityAt > all[j].LastActivityAt
		}
		return strings.ToLower(all[i].Title) < strings.ToLower(all[j].Title)
	})

	cards := make([]bookCardData, len(all))
	for i, b := range all {
		pct := 0
		if b.ReadestProgress[1] > 0 {
			pct = b.ReadestProgress[0] * 100 / b.ReadestProgress[1]
		}
		finished := b.ReadestStatus == "finished" ||
			(b.ReadestProgress[1] > 0 && b.ReadestProgress[0] >= b.ReadestProgress[1])

		coverURL := ""
		if b.CoverPath != "" {
			coverURL = "/covers/" + b.CoverPath
		}

		cards[i] = bookCardData{
			BookState:   b,
			ProgressPct: pct,
			IsFinished:  finished,
			CoverURL:    coverURL,
		}
	}
	return cards
}

// handleBooks renders the main single-page layout.
func (h *handlers) handleBooks(w http.ResponseWriter, r *http.Request) {
	cards := h.buildBookCards()

	var matched, unmatched int
	for _, c := range cards {
		if c.HardcoverBookID != 0 {
			matched++
		} else {
			unmatched++
		}
	}

	data := booksPageData{
		Books:          cards,
		Status:         h.buildSidebarStatus(),
		MatchedCount:   matched,
		UnmatchedCount: unmatched,
		TotalCount:     len(cards),
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.tmpl.ExecuteTemplate(w, "books.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// handleBookDetail returns the detail modal content for a book.
func (h *handlers) handleBookDetail(w http.ResponseWriter, r *http.Request) {
	hash := r.PathValue("hash")

	book, ok := h.state.GetBook(hash)
	if !ok {
		http.Error(w, "book not found", http.StatusNotFound)
		return
	}

	var ids *identifier.ParsedIdentifiers
	if book.Metadata != nil {
		parsed := identifier.Parse(book.Metadata, book.Title, book.Author)
		// Only set ids if there are actual identifiers to show.
		if len(parsed.HardcoverSlugs) > 0 || len(parsed.ISBN13s) > 0 || len(parsed.ISBN10s) > 0 || len(parsed.ASINs) > 0 {
			ids = &parsed
		}
	}

	// Resolve status ID to name.
	lastStatusName := ""
	if book.LastStatusSent != 0 {
		if name, ok := h.statusNames[book.LastStatusSent]; ok {
			lastStatusName = name
		}
	}

	type detailData struct {
		Book           state.BookState
		Identifiers    *identifier.ParsedIdentifiers
		CoverURL       string
		LastStatusName string
	}

	coverURL := ""
	if book.CoverPath != "" {
		coverURL = "/covers/" + book.CoverPath
	}

	data := detailData{
		Book:           book,
		Identifiers:    ids,
		CoverURL:       coverURL,
		LastStatusName: lastStatusName,
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.tmpl.ExecuteTemplate(w, "book_detail.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

type linkModalData struct {
	Book        state.BookState
	Series      string
	Identifiers *identifier.ParsedIdentifiers
	CoverURL    string
}

// handleLinkModal returns the link modal partial for the given book hash.
func (h *handlers) handleLinkModal(w http.ResponseWriter, r *http.Request) {
	hash := r.PathValue("hash")

	book, ok := h.state.GetBook(hash)
	if !ok {
		http.Error(w, "book not found", http.StatusNotFound)
		return
	}
	h.logger.Info("opening link modal", "title", book.Title, "author", book.Author, "hash", hash)

	var ids *identifier.ParsedIdentifiers
	var series string
	if book.Metadata != nil {
		parsed := identifier.Parse(book.Metadata, book.Title, book.Author)
		ids = &parsed
		if parsed.Series != "" {
			if parsed.SeriesIndex > 0 {
				series = fmt.Sprintf("%s #%g", parsed.Series, parsed.SeriesIndex)
			} else {
				series = parsed.Series
			}
		}
	}

	coverURL := ""
	if book.CoverPath != "" {
		coverURL = "/covers/" + book.CoverPath
	}

	data := linkModalData{
		Book:        book,
		Series:      series,
		Identifiers: ids,
		CoverURL:    coverURL,
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.tmpl.ExecuteTemplate(w, "link_modal.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

type enrichedSearchResult struct {
	BookID          int
	Title           string
	Author          string
	CoverURL        string
	Slug            string
	Series          string
	UserStatus      string
	EbookEdition    *editionInfo
	PhysicalEdition *editionInfo
	EditionID       int
	EditionPages    string
	score           int // for sorting, not exposed to template
}

type editionInfo struct {
	Format    string
	Pages     string
	ISBN13    string
	ISBN10    string
	ASIN      string
	Publisher string
}

type searchData struct {
	Hash    string
	Results []enrichedSearchResult
}

// handleSearch performs a Hardcover book search and returns a partial HTML response.
func (h *handlers) handleSearch(w http.ResponseWriter, r *http.Request) {
	hash := r.PathValue("hash")
	q := r.URL.Query().Get("q")

	if q == "" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		return
	}

	h.logger.Info("searching hardcover", "hash", hash, "query", q)
	books, err := h.finder.SearchBooks(r.Context(), q)
	if err != nil {
		h.logger.Error("search failed", "query", q, "error", err)
		http.Error(w, fmt.Sprintf("search error: %v", err), http.StatusInternalServerError)
		return
	}
	h.logger.Info("search results", "query", q, "count", len(books))

	// Fetch user status for each book concurrently.
	type userStatusResult struct {
		idx      int
		statusID int
		err      error
	}
	userStatuses := make([]int, len(books))
	var wg sync.WaitGroup
	ch := make(chan userStatusResult, len(books))

	for i, b := range books {
		wg.Add(1)
		go func(idx, bookID int) {
			defer wg.Done()
			ub, err := h.updater.GetUserBook(r.Context(), bookID)
			if err != nil {
				ch <- userStatusResult{idx: idx, err: err}
				return
			}
			statusID := 0
			if ub != nil {
				statusID = ub.StatusID
			}
			ch <- userStatusResult{idx: idx, statusID: statusID}
		}(i, b.ID)
	}

	wg.Wait()
	close(ch)
	for res := range ch {
		if res.err == nil {
			userStatuses[res.idx] = res.statusID
		}
	}

	var results []enrichedSearchResult
	for i, b := range books {
		er := enrichedSearchResult{
			BookID:   b.ID,
			Title:    b.Title,
			Author:   b.AuthorNames(),
			CoverURL: b.CoverURL(),
			Slug:     b.Slug,
			Series:   b.SeriesName(),
		}

		if statusID := userStatuses[i]; statusID != 0 {
			if name, ok := h.statusNames[statusID]; ok {
				er.UserStatus = name
			}
		}

		if b.DefaultEbookEdition != nil {
			ed := b.DefaultEbookEdition
			ei := &editionInfo{
				Format: ed.EditionFormat,
				ISBN13: ed.ISBN13,
				ASIN:   ed.ASIN,
			}
			if ed.Pages != nil {
				ei.Pages = strconv.Itoa(*ed.Pages)
			}
			if ed.Publisher != nil {
				ei.Publisher = ed.Publisher.Name
			}
			er.EbookEdition = ei
			er.EditionID = ed.ID
			if ed.Pages != nil {
				er.EditionPages = strconv.Itoa(*ed.Pages)
			}
		}

		if b.DefaultPhysicalEdition != nil {
			ed := b.DefaultPhysicalEdition
			ei := &editionInfo{
				Format: ed.EditionFormat,
				ISBN13: ed.ISBN13,
				ISBN10: ed.ISBN10,
			}
			if ed.Pages != nil {
				ei.Pages = strconv.Itoa(*ed.Pages)
			}
			if ed.Publisher != nil {
				ei.Publisher = ed.Publisher.Name
			}
			er.PhysicalEdition = ei
			// Only use physical edition for linking if no ebook edition.
			if b.DefaultEbookEdition == nil {
				er.EditionID = ed.ID
				if ed.Pages != nil {
					er.EditionPages = strconv.Itoa(*ed.Pages)
				}
			}
		}

		if er.EditionPages == "" {
			er.EditionPages = "0"
		}

		results = append(results, er)
	}

	// Score and sort results by relevance to the Readest book.
	if book, ok := h.state.GetBook(hash); ok {
		var ids identifier.ParsedIdentifiers
		if book.Metadata != nil {
			ids = identifier.Parse(book.Metadata, book.Title, book.Author)
		}
		scoreSearchResults(results, book, ids)
		sort.Slice(results, func(i, j int) bool {
			return results[i].score > results[j].score
		})
	}

	data := searchData{
		Hash:    hash,
		Results: results,
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.tmpl.ExecuteTemplate(w, "link_search_results.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// handleLink links a book to a Hardcover book/edition based on form values.
func (h *handlers) handleLink(w http.ResponseWriter, r *http.Request) {
	hash := r.PathValue("hash")

	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form data", http.StatusBadRequest)
		return
	}

	bookID, err := strconv.Atoi(r.FormValue("book_id"))
	if err != nil || bookID == 0 {
		http.Error(w, "invalid book_id", http.StatusBadRequest)
		return
	}

	slug := r.FormValue("slug")

	editionID, _ := strconv.Atoi(r.FormValue("edition_id"))
	editionPages, _ := strconv.Atoi(r.FormValue("edition_pages"))

	if b, ok := h.state.GetBook(hash); ok {
		h.logger.Info("linking book", "title", b.Title, "author", b.Author, "hash", hash, "slug", slug, "book_id", bookID, "edition_id", editionID)
	}
	h.state.SetManualLink(hash, bookID, slug, editionID, editionPages)

	// Download cover image by looking up the book server-side.
	if h.coversDir != "" && slug != "" {
		if book, err := h.finder.FindBookBySlug(r.Context(), slug); err != nil {
			h.logger.Error("failed to look up book for cover", "slug", slug, "error", err)
		} else if book != nil && book.CoverURL() != "" {
			if coverPath, err := syncsvc.DownloadCover(h.coversDir, hash, book.CoverURL()); err != nil {
				h.logger.Error("failed to download cover", "hash", hash, "error", err)
			} else if coverPath != "" {
				if b, ok := h.state.GetBook(hash); ok {
					b.CoverPath = coverPath
					b.Series = book.SeriesName()
					h.state.SetBook(hash, b)
				}
			}
		}
	}

	if err := h.state.Save(); err != nil {
		http.Error(w, fmt.Sprintf("failed to save state: %v", err), http.StatusInternalServerError)
		return
	}

	book, ok := h.state.GetBook(hash)
	if !ok {
		http.Error(w, "book not found", http.StatusNotFound)
		return
	}

	pct := 0
	if book.ReadestProgress[1] > 0 {
		pct = book.ReadestProgress[0] * 100 / book.ReadestProgress[1]
	}
	finished := book.ReadestStatus == "finished" ||
		(book.ReadestProgress[1] > 0 && book.ReadestProgress[0] >= book.ReadestProgress[1])
	cardCoverURL := ""
	if book.CoverPath != "" {
		// Cache-bust with timestamp so relinked covers update immediately.
		cardCoverURL = fmt.Sprintf("/covers/%s?t=%d", book.CoverPath, time.Now().UnixMilli())
	}

	card := bookCardData{
		BookState:   book,
		ProgressPct: pct,
		IsFinished:  finished,
		CoverURL:    cardCoverURL,
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("HX-Trigger", "closeModal")
	if err := h.tmpl.ExecuteTemplate(w, "book_card.html", card); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// handleUnlink clears a book's Hardcover mapping.
func (h *handlers) handleUnlink(w http.ResponseWriter, r *http.Request) {
	hash := r.PathValue("hash")

	book, ok := h.state.GetBook(hash)
	if !ok {
		http.Error(w, "book not found", http.StatusNotFound)
		return
	}

	h.logger.Info("unlinking book", "hash", hash, "title", book.Title, "was_slug", book.HardcoverSlug)
	book.HardcoverBookID = 0
	book.HardcoverSlug = ""
	book.EditionID = 0
	book.EditionPages = 0
	book.MatchMethod = ""
	book.UserBookID = 0
	book.Unmatched = true

	h.state.SetBook(hash, book)
	if err := h.state.Save(); err != nil {
		http.Error(w, fmt.Sprintf("failed to save state: %v", err), http.StatusInternalServerError)
		return
	}

	book, _ = h.state.GetBook(hash)
	card := bookCardData{
		BookState: book,
		CoverURL:  "",
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("HX-Trigger", "closeModal")
	if err := h.tmpl.ExecuteTemplate(w, "book_card.html", card); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

type sidebarStatusData struct {
	LastSync       string
	MatchedCount   int
	UnmatchedCount int
}

func (h *handlers) buildSidebarStatus() sidebarStatusData {
	books := h.state.ListBooks()
	var matched, unmatched int
	for _, b := range books {
		if b.HardcoverBookID != 0 {
			matched++
		} else {
			unmatched++
		}
	}

	lastSync := "never"
	ts := h.state.LastBookSync
	if h.state.LastConfigSync > ts {
		ts = h.state.LastConfigSync
	}
	if ts > 0 {
		ago := time.Since(time.UnixMilli(ts))
		switch {
		case ago < time.Minute:
			lastSync = "just now"
		case ago < time.Hour:
			lastSync = fmt.Sprintf("%d min ago", int(ago.Minutes()))
		case ago < 24*time.Hour:
			lastSync = fmt.Sprintf("%d hours ago", int(ago.Hours()))
		default:
			lastSync = time.UnixMilli(ts).UTC().Format("Jan 2, 15:04 UTC")
		}
	}

	return sidebarStatusData{
		LastSync:       lastSync,
		MatchedCount:   matched,
		UnmatchedCount: unmatched,
	}
}

// handleSidebarStatus returns the sidebar status partial for htmx refresh.
func (h *handlers) handleSidebarStatus(w http.ResponseWriter, r *http.Request) {
	data := h.buildSidebarStatus()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.tmpl.ExecuteTemplate(w, "sidebar_status", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// handleTriggerSync triggers a sync in the background and returns updated status.
func (h *handlers) handleTriggerSync(w http.ResponseWriter, r *http.Request) {
	h.logger.Info("manual sync triggered")
	go func() {
		_ = h.engine.SyncNow(h.ctx)
	}()

	if r.Header.Get("HX-Request") != "" {
		h.handleSidebarStatus(w, r)
		return
	}
	http.Redirect(w, r, "/books", http.StatusSeeOther)
}

// handleFullSync resets sync timestamps and re-pulls everything from Readest.
func (h *handlers) handleFullSync(w http.ResponseWriter, r *http.Request) {
	h.logger.Info("full sync triggered")
	go func() {
		_ = h.engine.FullSync(h.ctx)
	}()

	if r.Header.Get("HX-Request") != "" {
		h.handleSidebarStatus(w, r)
		return
	}
	http.Redirect(w, r, "/books", http.StatusSeeOther)
}

// handleSSE streams sync events as Server-Sent Events.
func (h *handlers) handleSSE(w http.ResponseWriter, r *http.Request) {
	if h.events == nil {
		http.Error(w, "event streaming not configured", http.StatusServiceUnavailable)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	ch := h.events.Subscribe()
	if ch == nil {
		http.Error(w, "too many connections", http.StatusServiceUnavailable)
		return
	}
	defer h.events.Unsubscribe(ch)

	for {
		select {
		case <-r.Context().Done():
			return
		case evt, ok := <-ch:
			if !ok {
				return
			}
			data, _ := json.Marshal(evt)
			if _, err := fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

// scoreSearchResults assigns a relevance score to each result based on how
// well it matches the Readest book's metadata. Higher = better match.
func scoreSearchResults(results []enrichedSearchResult, book state.BookState, ids identifier.ParsedIdentifiers) {
	readestTitle := strings.ToLower(strings.TrimSpace(book.Title))
	readestAuthor := strings.ToLower(strings.TrimSpace(book.Author))

	isbnSet := make(map[string]bool)
	for _, isbn := range ids.ISBN13s {
		isbnSet[isbn] = true
	}
	for _, isbn := range ids.ISBN10s {
		isbnSet[isbn] = true
	}

	for i := range results {
		r := &results[i]
		score := 0

		// Exact title match (case-insensitive): +10
		if strings.ToLower(r.Title) == readestTitle {
			score += 10
		}

		// Author match (case-insensitive, either contains the other): +5
		if readestAuthor != "" && r.Author != "" {
			ra := strings.ToLower(r.Author)
			if ra == readestAuthor || strings.Contains(ra, readestAuthor) || strings.Contains(readestAuthor, ra) {
				score += 5
			}
		}

		// ISBN match on either edition: +8
		if r.EbookEdition != nil {
			if isbnSet[r.EbookEdition.ISBN13] || isbnSet[r.EbookEdition.ISBN10] {
				score += 8
			}
		}
		if r.PhysicalEdition != nil {
			if isbnSet[r.PhysicalEdition.ISBN13] || isbnSet[r.PhysicalEdition.ISBN10] {
				score += 8
			}
		}

		// Already on shelf: +2
		if r.UserStatus != "" {
			score += 2
		}

		r.score = score
	}
}
