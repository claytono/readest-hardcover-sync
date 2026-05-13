package state

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestState_LoadNonexistent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")

	s := New(path)
	err := s.Load()

	require.NoError(t, err)
	assert.NotNil(t, s.Books)
	assert.Empty(t, s.Books)
	assert.Equal(t, int64(0), s.GetLastBookSync())
	assert.Equal(t, int64(0), s.GetLastConfigSync())
}

func TestState_SaveAndReload(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")

	s := New(path)
	s.SetLastBookSync(1234567890)
	s.SetLastConfigSync(9876543210)
	s.SetBook("hash1", BookState{
		BookHash:          "hash1",
		Title:             "Test Book",
		Author:            "Test Author",
		HardcoverBookID:   42,
		HardcoverSlug:     "test-book",
		EditionID:         99,
		EditionPages:      300,
		ReadingFormatID:   1,
		MatchMethod:       "isbn",
		UserBookID:        10,
		UserBookEditionID: 99,
		UserBookReadID:    20,
		LastStatusSent:    2,
		LastProgressSent:  150,
		ReadestProgress:   [2]int{150, 300},
		ReadestStatus:     "reading",
		Unmatched:         false,
		LastError:         "",
	})
	s.SetBook("hash2", BookState{
		BookHash:  "hash2",
		Title:     "Another Book",
		Author:    "Another Author",
		Unmatched: true,
		LastError: "no match found",
	})

	err := s.Save()
	require.NoError(t, err)

	s2 := New(path)
	err = s2.Load()
	require.NoError(t, err)

	assert.Equal(t, int64(1234567890), s2.GetLastBookSync())
	assert.Equal(t, int64(9876543210), s2.GetLastConfigSync())

	b1, ok := s2.GetBook("hash1")
	require.True(t, ok)
	assert.Equal(t, "hash1", b1.BookHash)
	assert.Equal(t, "Test Book", b1.Title)
	assert.Equal(t, "Test Author", b1.Author)
	assert.Equal(t, 42, b1.HardcoverBookID)
	assert.Equal(t, "test-book", b1.HardcoverSlug)
	assert.Equal(t, 99, b1.EditionID)
	assert.Equal(t, 300, b1.EditionPages)
	assert.Equal(t, 1, b1.ReadingFormatID)
	assert.Equal(t, "isbn", b1.MatchMethod)
	assert.Equal(t, 10, b1.UserBookID)
	assert.Equal(t, 99, b1.UserBookEditionID)
	assert.Equal(t, 20, b1.UserBookReadID)
	assert.Equal(t, 2, b1.LastStatusSent)
	assert.Equal(t, 150, b1.LastProgressSent)
	assert.Equal(t, [2]int{150, 300}, b1.ReadestProgress)
	assert.Equal(t, "reading", b1.ReadestStatus)
	assert.False(t, b1.Unmatched)

	b2, ok := s2.GetBook("hash2")
	require.True(t, ok)
	assert.Equal(t, "Another Book", b2.Title)
	assert.True(t, b2.Unmatched)
	assert.Equal(t, "no match found", b2.LastError)
}

func TestState_AtomicWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")

	s := New(path)
	s.SetBook("hash1", BookState{BookHash: "hash1", Title: "Book"})

	err := s.Save()
	require.NoError(t, err)

	// The .tmp file should not exist after a successful save
	tmpPath := path + ".tmp"
	_, err = os.Stat(tmpPath)
	assert.True(t, os.IsNotExist(err), "tmp file should not exist after save")

	// The real file should exist
	_, err = os.Stat(path)
	require.NoError(t, err)
}

func TestState_GetSetBook(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")

	s := New(path)

	_, ok := s.GetBook("missing")
	assert.False(t, ok)

	book := BookState{
		BookHash:        "abc123",
		Title:           "My Book",
		Author:          "My Author",
		HardcoverBookID: 7,
		EditionID:       8,
		MatchMethod:     "title",
	}
	s.SetBook("abc123", book)

	got, ok := s.GetBook("abc123")
	require.True(t, ok)
	assert.Equal(t, "abc123", got.BookHash)
	assert.Equal(t, "My Book", got.Title)
	assert.Equal(t, "My Author", got.Author)
	assert.Equal(t, 7, got.HardcoverBookID)
	assert.Equal(t, 8, got.EditionID)
	assert.Equal(t, "title", got.MatchMethod)
}

func TestState_SetManualLink(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")

	s := New(path)
	s.SetBook("hash1", BookState{
		BookHash:  "hash1",
		Title:     "Some Book",
		Unmatched: true,
		Series:    "Old Series #1",
		CoverURL:  "https://assets.example.com/old-cover.jpg",
		CoverPath: "old-cover.jpg",
	})

	s.SetManualLink("hash1", 55, "some-book", 200, 400)

	b, ok := s.GetBook("hash1")
	require.True(t, ok)
	assert.Equal(t, 55, b.HardcoverBookID)
	assert.Equal(t, "some-book", b.HardcoverSlug)
	assert.Equal(t, 200, b.EditionID)
	assert.Equal(t, 400, b.EditionPages)
	assert.Equal(t, "manual", b.MatchMethod)
	assert.False(t, b.Unmatched)
	assert.Empty(t, b.Series)
	assert.Empty(t, b.CoverURL)
	assert.Empty(t, b.CoverPath)
	// Title should be preserved
	assert.Equal(t, "Some Book", b.Title)
}

func TestState_ListBooks(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")

	s := New(path)

	assert.Empty(t, s.ListBooks())

	s.SetBook("h1", BookState{BookHash: "h1", Title: "Book 1"})
	s.SetBook("h2", BookState{BookHash: "h2", Title: "Book 2"})
	s.SetBook("h3", BookState{BookHash: "h3", Title: "Book 3"})

	books := s.ListBooks()
	assert.Len(t, books, 3)

	titles := make(map[string]bool)
	for _, b := range books {
		titles[b.Title] = true
	}
	assert.True(t, titles["Book 1"])
	assert.True(t, titles["Book 2"])
	assert.True(t, titles["Book 3"])
}

func TestState_IsMatched(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")

	s := New(path)

	assert.False(t, s.IsMatched("nonexistent"))

	s.SetBook("unmatched", BookState{BookHash: "unmatched", Title: "Unmatched Book"})
	assert.False(t, s.IsMatched("unmatched"))

	s.SetBook("matched", BookState{BookHash: "matched", Title: "Matched Book", HardcoverBookID: 5})
	assert.True(t, s.IsMatched("matched"))
}

// TestState_Load_InvalidJSON verifies that Load returns an error when the state
// file contains malformed JSON.
func TestState_Load_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")

	err := os.WriteFile(path, []byte("{not valid json"), 0o644)
	require.NoError(t, err)

	s := New(path)
	err = s.Load()
	require.Error(t, err)
}

// TestState_Load_NullBooks verifies that Load initialises Books to an empty
// map when the JSON contains `"books": null`.
func TestState_Load_NullBooks(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")

	err := os.WriteFile(path, []byte(`{"last_book_sync":0,"last_config_sync":0,"books":null}`), 0o644)
	require.NoError(t, err)

	s := New(path)
	err = s.Load()
	require.NoError(t, err)
	assert.NotNil(t, s.Books)
	assert.Empty(t, s.Books)
}

// TestState_Save_WriteError verifies that Save returns an error when the
// destination directory is read-only (WriteFile on the .tmp path fails).
func TestState_Save_WriteError(t *testing.T) {
	dir := t.TempDir()
	// Make the directory read-only so WriteFile fails.
	require.NoError(t, os.Chmod(dir, 0o555))
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })

	path := filepath.Join(dir, "state.json")
	s := New(path)
	s.SetBook("h1", BookState{BookHash: "h1", Title: "Book"})

	err := s.Save()
	require.Error(t, err)
}

func TestState_UpdateBook(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	s := New(path)

	s.SetBook("h1", BookState{BookHash: "h1", Title: "Original", Author: "Author"})

	// Mutation succeeds for existing book.
	ok := s.UpdateBook("h1", func(b *BookState) {
		b.Title = "Updated"
	})
	require.True(t, ok)

	got, _ := s.GetBook("h1")
	assert.Equal(t, "Updated", got.Title)
	assert.Equal(t, "Author", got.Author) // other fields preserved

	// Returns false for missing book.
	ok = s.UpdateBook("missing", func(b *BookState) {
		b.Title = "nope"
	})
	assert.False(t, ok)
}

func TestState_SaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")

	s := New(path)
	s.SetLastBookSync(111)
	s.SetLastConfigSync(222)
	s.SetBook("h1", BookState{BookHash: "h1", Title: "Book One"})

	require.NoError(t, s.Save())

	// Verify the JSON file contains the expected keys for backward compatibility.
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	json := string(data)
	assert.Contains(t, json, `"last_book_sync"`)
	assert.Contains(t, json, `"last_config_sync"`)
	assert.Contains(t, json, `"books"`)

	// Load into a fresh state.
	s2 := New(path)
	require.NoError(t, s2.Load())
	assert.Equal(t, int64(111), s2.GetLastBookSync())
	assert.Equal(t, int64(222), s2.GetLastConfigSync())
	b, ok := s2.GetBook("h1")
	require.True(t, ok)
	assert.Equal(t, "Book One", b.Title)
}

func TestState_EmptyPath_LoadSaveNoop(t *testing.T) {
	s := New("")
	require.NoError(t, s.Load())
	s.SetBook("h1", BookState{BookHash: "h1", Title: "In-Memory"})
	require.NoError(t, s.Save())

	b, ok := s.GetBook("h1")
	require.True(t, ok)
	assert.Equal(t, "In-Memory", b.Title)
}

func TestState_ResetSyncTimestamps(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")

	s := New(path)
	s.SetLastBookSync(100)
	s.SetLastConfigSync(200)

	s.ResetSyncTimestamps()

	assert.Equal(t, int64(0), s.GetLastBookSync())
	assert.Equal(t, int64(0), s.GetLastConfigSync())
}
