package demo

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStartServer(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	server, baseURL, err := StartServer(ctx, logger, ":0", t.TempDir())
	require.NoError(t, err)
	defer server.Close() //nolint:errcheck

	// Verify /books renders all demo books.
	resp, err := http.Get(baseURL + "/books")
	require.NoError(t, err)
	defer resp.Body.Close() //nolint:errcheck
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	html := string(body)

	// Check matched books render with titles and authors.
	assert.Contains(t, html, "Pride and Prejudice")
	assert.Contains(t, html, "Jane Austen")
	assert.Contains(t, html, "Moby Dick")
	assert.Contains(t, html, "Herman Melville")

	// Check unmatched books render.
	assert.Contains(t, html, "The War of the Worlds")
	assert.Contains(t, html, "The Adventures of Sherlock Holmes")

	// Check counts.
	assert.Contains(t, html, "All (10)")
	assert.Contains(t, html, "Matched (8)")
	assert.Contains(t, html, "Unmatched (2)")

	// Check sidebar status rendered.
	assert.Contains(t, html, "just now")
}

func TestStartServer_Search(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	server, baseURL, err := StartServer(ctx, logger, ":0", t.TempDir())
	require.NoError(t, err)
	defer server.Close() //nolint:errcheck

	// Pick any book hash to test search.
	hash := bookHash("Pride and Prejudice")
	resp, err := http.Get(baseURL + "/books/" + hash + "/search?q=pride")
	require.NoError(t, err)
	defer resp.Body.Close() //nolint:errcheck
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.True(t, strings.Contains(string(body), "Pride and Prejudice"))
}

func TestStartServer_SyncNilEngine(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	server, baseURL, err := StartServer(ctx, logger, ":0", t.TempDir())
	require.NoError(t, err)
	defer server.Close() //nolint:errcheck

	// POST /sync with nil engine should not crash.
	resp, err := http.Post(baseURL+"/sync", "", nil)
	require.NoError(t, err)
	defer resp.Body.Close() //nolint:errcheck
	// Non-htmx request redirects (303 -> follows to 200).
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestDemoFinder_SearchBooks(t *testing.T) {
	f := newDemoFinder()
	ctx := context.Background()

	results, err := f.SearchBooks(ctx, "pride")
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "Pride and Prejudice", results[0].Title)

	results, err = f.SearchBooks(ctx, "doyle")
	require.NoError(t, err)
	assert.Len(t, results, 1)
	assert.Equal(t, "A Study in Scarlet", results[0].Title)

	results, err = f.SearchBooks(ctx, "nonexistent")
	require.NoError(t, err)
	assert.Empty(t, results)
}

func TestDemoFinder_FindBookBySlug(t *testing.T) {
	f := newDemoFinder()
	ctx := context.Background()

	book, err := f.FindBookBySlug(ctx, "moby-dick")
	require.NoError(t, err)
	require.NotNil(t, book)
	assert.Equal(t, "Moby Dick", book.Title)

	book, err = f.FindBookBySlug(ctx, "nonexistent")
	require.NoError(t, err)
	assert.Nil(t, book)
}

func TestDemoFinder_ISBNReturnsNil(t *testing.T) {
	f := newDemoFinder()
	ctx := context.Background()

	ed, err := f.FindEditionByISBN13(ctx, "9780000000000")
	require.NoError(t, err)
	assert.Nil(t, ed)

	ed, err = f.FindEditionByISBN10(ctx, "0000000000")
	require.NoError(t, err)
	assert.Nil(t, ed)
}

func TestParseSeriesEntry(t *testing.T) {
	// Normal case: "Series Name #1"
	entries := parseSeriesEntry("Sherlock Holmes #1")
	require.Len(t, entries, 1)
	assert.Equal(t, "Sherlock Holmes", entries[0].Series.Name)
	assert.Equal(t, float64(1), entries[0].Position)

	// No number suffix
	entries = parseSeriesEntry("Just A Series")
	require.Len(t, entries, 1)
	assert.Equal(t, "Just A Series", entries[0].Series.Name)

	// Invalid number after #
	entries = parseSeriesEntry("Series #abc")
	require.Len(t, entries, 1)
	assert.Equal(t, "Series #abc", entries[0].Series.Name)
}

func TestDemoUpdater(t *testing.T) {
	u := &demoUpdater{}
	ctx := context.Background()

	me, err := u.GetMe(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, me.ID)

	statuses, err := u.GetStatuses(ctx)
	require.NoError(t, err)
	assert.Len(t, statuses, 6)

	ub, err := u.GetUserBook(ctx, 1)
	require.NoError(t, err)
	assert.Nil(t, ub)

	inserted, err := u.InsertUserBook(ctx, 1, 2, 1, nil)
	require.NoError(t, err)
	assert.Equal(t, 9999, inserted.ID)

	updated, err := u.UpdateUserBook(ctx, 1, 3, nil)
	require.NoError(t, err)
	assert.Equal(t, 3, updated.StatusID)

	read, err := u.InsertUserBookRead(ctx, 1, 50, nil, "2024-01-01", nil)
	require.NoError(t, err)
	assert.Equal(t, 9999, read.ID)

	readUpdated, err := u.UpdateUserBookRead(ctx, 1, 100, nil)
	require.NoError(t, err)
	assert.Equal(t, 100, *readUpdated.ProgressPages)
}
