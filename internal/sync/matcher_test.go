package sync

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/claytono/readest-hardcover-sync/internal/hardcover"
	"github.com/claytono/readest-hardcover-sync/internal/identifier"
)

// mockFinder is a test double for the BookFinder interface.
type mockFinder struct {
	booksBySlug   map[string]*hardcover.Book
	editionISBN13 map[string]*hardcover.Edition
	editionISBN10 map[string]*hardcover.Edition
	searchResults []hardcover.Book
}

func (m *mockFinder) FindBookBySlug(_ context.Context, slug string) (*hardcover.Book, error) {
	return m.booksBySlug[slug], nil
}

func (m *mockFinder) FindEditionByISBN13(_ context.Context, isbn string) (*hardcover.Edition, error) {
	return m.editionISBN13[isbn], nil
}

func (m *mockFinder) FindEditionByISBN10(_ context.Context, isbn string) (*hardcover.Edition, error) {
	return m.editionISBN10[isbn], nil
}

func (m *mockFinder) SearchBooks(_ context.Context, _ string) ([]hardcover.Book, error) {
	return m.searchResults, nil
}

func TestMatcher_HardcoverSlug(t *testing.T) {
	pages := 300
	formatID := hardcover.ReadingFormatEBook
	book := &hardcover.Book{
		ID:   42,
		Slug: "some-book",
		DefaultEbookEdition: &hardcover.Edition{
			ID:              10,
			BookID:          42,
			Pages:           &pages,
			ReadingFormatID: &formatID,
		},
	}
	finder := &mockFinder{
		booksBySlug: map[string]*hardcover.Book{"some-book": book},
	}
	matcher := NewMatcher(finder, false)

	ids := identifier.ParsedIdentifiers{HardcoverSlugs: []string{"some-book"}}
	result, err := matcher.Match(context.Background(), ids)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, "slug", result.MatchMethod)
	assert.Equal(t, 42, result.BookID)
	assert.Equal(t, "some-book", result.Slug)
	assert.Equal(t, 10, result.EditionID)
	assert.Equal(t, 300, result.EditionPages)
	assert.Equal(t, hardcover.ReadingFormatEBook, result.ReadingFormatID)
}

func TestMatcher_ISBN13(t *testing.T) {
	pages := 250
	edition := &hardcover.Edition{
		ID:     20,
		BookID: 55,
		Pages:  &pages,
		Book:   &hardcover.Book{ID: 55, Slug: "isbn13-book"},
	}
	finder := &mockFinder{
		booksBySlug:   map[string]*hardcover.Book{},
		editionISBN13: map[string]*hardcover.Edition{"9781234567890": edition},
	}
	matcher := NewMatcher(finder, false)

	ids := identifier.ParsedIdentifiers{ISBN13s: []string{"9781234567890"}}
	result, err := matcher.Match(context.Background(), ids)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, "isbn13", result.MatchMethod)
	assert.Equal(t, 55, result.BookID)
	assert.Equal(t, "isbn13-book", result.Slug)
	assert.Equal(t, 20, result.EditionID)
	assert.Equal(t, 250, result.EditionPages)
}

func TestMatcher_FallsThrough(t *testing.T) {
	pages := 180
	edition := &hardcover.Edition{
		ID:     30,
		BookID: 77,
		Pages:  &pages,
		Book:   &hardcover.Book{ID: 77, Slug: "isbn10-book"},
	}
	finder := &mockFinder{
		booksBySlug:   map[string]*hardcover.Book{},
		editionISBN13: map[string]*hardcover.Edition{},
		editionISBN10: map[string]*hardcover.Edition{"0123456789": edition},
	}
	matcher := NewMatcher(finder, false)

	// ISBN13 returns nothing, ISBN10 matches.
	ids := identifier.ParsedIdentifiers{
		ISBN13s: []string{"9780000000000"},
		ISBN10s: []string{"0123456789"},
	}
	result, err := matcher.Match(context.Background(), ids)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, "isbn10", result.MatchMethod)
	assert.Equal(t, 77, result.BookID)
}

func TestMatcher_TitleDisabled(t *testing.T) {
	finder := &mockFinder{
		booksBySlug:   map[string]*hardcover.Book{},
		editionISBN13: map[string]*hardcover.Edition{},
		editionISBN10: map[string]*hardcover.Edition{},
	}
	matcher := NewMatcher(finder, false)

	ids := identifier.ParsedIdentifiers{Title: "Some Book", Author: "Some Author"}
	result, err := matcher.Match(context.Background(), ids)

	require.NoError(t, err)
	assert.Nil(t, result)
}

func TestMatcher_TitleEnabled(t *testing.T) {
	pages := 400
	book := hardcover.Book{
		ID:   99,
		Slug: "title-book",
		DefaultPhysicalEdition: &hardcover.Edition{
			ID:     50,
			BookID: 99,
			Pages:  &pages,
		},
	}
	finder := &mockFinder{
		booksBySlug:   map[string]*hardcover.Book{},
		editionISBN13: map[string]*hardcover.Edition{},
		editionISBN10: map[string]*hardcover.Edition{},
		searchResults: []hardcover.Book{book},
	}
	matcher := NewMatcher(finder, true)

	ids := identifier.ParsedIdentifiers{Title: "Some Book", Author: "Some Author"}
	result, err := matcher.Match(context.Background(), ids)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, "title", result.MatchMethod)
	assert.Equal(t, 99, result.BookID)
	assert.Equal(t, "title-book", result.Slug)
	assert.Equal(t, 50, result.EditionID)
	assert.Equal(t, 400, result.EditionPages)
}

func TestMatcher_NothingMatches(t *testing.T) {
	finder := &mockFinder{
		booksBySlug:   map[string]*hardcover.Book{},
		editionISBN13: map[string]*hardcover.Edition{},
		editionISBN10: map[string]*hardcover.Edition{},
		searchResults: []hardcover.Book{},
	}
	matcher := NewMatcher(finder, true)

	ids := identifier.ParsedIdentifiers{
		HardcoverSlugs: []string{"unknown-slug"},
		ISBN13s:        []string{"9780000000000"},
		ISBN10s:        []string{"0000000000"},
		Title:          "Unknown Title",
		Author:         "Unknown Author",
	}
	result, err := matcher.Match(context.Background(), ids)

	require.NoError(t, err)
	assert.Nil(t, result)
}

// errorFinder is a BookFinder that returns errors for specific lookup types.
type errorFinder struct {
	slugErr   error
	isbn13Err error
	isbn10Err error
	searchErr error
}

func (e *errorFinder) FindBookBySlug(_ context.Context, _ string) (*hardcover.Book, error) {
	return nil, e.slugErr
}

func (e *errorFinder) FindEditionByISBN13(_ context.Context, _ string) (*hardcover.Edition, error) {
	return nil, e.isbn13Err
}

func (e *errorFinder) FindEditionByISBN10(_ context.Context, _ string) (*hardcover.Edition, error) {
	return nil, e.isbn10Err
}

func (e *errorFinder) SearchBooks(_ context.Context, _ string) ([]hardcover.Book, error) {
	return nil, e.searchErr
}

// TestMatcher_SlugFallback_PhysicalEdition: Slug match with no ebook edition, falls
// back to physical edition.
func TestMatcher_SlugFallback_PhysicalEdition(t *testing.T) {
	pages := 320
	book := &hardcover.Book{
		ID:   77,
		Slug: "physical-only",
		DefaultPhysicalEdition: &hardcover.Edition{
			ID:     15,
			BookID: 77,
			Pages:  &pages,
		},
	}
	finder := &mockFinder{
		booksBySlug: map[string]*hardcover.Book{"physical-only": book},
	}
	matcher := NewMatcher(finder, false)

	ids := identifier.ParsedIdentifiers{HardcoverSlugs: []string{"physical-only"}}
	result, err := matcher.Match(context.Background(), ids)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, "slug", result.MatchMethod)
	assert.Equal(t, 77, result.BookID)
	assert.Equal(t, 15, result.EditionID)
	assert.Equal(t, 320, result.EditionPages)
	assert.Equal(t, hardcover.ReadingFormatEBook, result.ReadingFormatID, "should default to ebook format")
}

// TestMatcher_SlugFallback_NoEdition: Slug match with no edition at all.
func TestMatcher_SlugFallback_NoEdition(t *testing.T) {
	book := &hardcover.Book{
		ID:   88,
		Slug: "no-edition",
	}
	finder := &mockFinder{
		booksBySlug: map[string]*hardcover.Book{"no-edition": book},
	}
	matcher := NewMatcher(finder, false)

	ids := identifier.ParsedIdentifiers{HardcoverSlugs: []string{"no-edition"}}
	result, err := matcher.Match(context.Background(), ids)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, "slug", result.MatchMethod)
	assert.Equal(t, 88, result.BookID)
	assert.Equal(t, 0, result.EditionID)
	assert.Equal(t, hardcover.ReadingFormatEBook, result.ReadingFormatID, "should default to ebook format when no edition")
}

// TestMatcher_SlugNoReadingFormatID: Slug match with edition that has nil ReadingFormatID —
// defaults to ReadingFormatEBook.
func TestMatcher_SlugNoReadingFormatID(t *testing.T) {
	pages := 200
	book := &hardcover.Book{
		ID:   66,
		Slug: "no-format",
		DefaultEbookEdition: &hardcover.Edition{
			ID:              25,
			BookID:          66,
			Pages:           &pages,
			ReadingFormatID: nil, // explicitly nil
		},
	}
	finder := &mockFinder{
		booksBySlug: map[string]*hardcover.Book{"no-format": book},
	}
	matcher := NewMatcher(finder, false)

	ids := identifier.ParsedIdentifiers{HardcoverSlugs: []string{"no-format"}}
	result, err := matcher.Match(context.Background(), ids)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, hardcover.ReadingFormatEBook, result.ReadingFormatID, "nil ReadingFormatID should default to ebook")
}

// TestMatcher_FindBookBySlug_Error: FindBookBySlug error propagates.
func TestMatcher_FindBookBySlug_Error(t *testing.T) {
	finder := &errorFinder{slugErr: assert.AnError}
	matcher := NewMatcher(finder, false)

	ids := identifier.ParsedIdentifiers{HardcoverSlugs: []string{"some-slug"}}
	_, err := matcher.Match(context.Background(), ids)
	assert.ErrorIs(t, err, assert.AnError)
}

// TestMatcher_FindEditionByISBN13_Error: FindEditionByISBN13 error propagates.
func TestMatcher_FindEditionByISBN13_Error(t *testing.T) {
	finder := &errorFinder{isbn13Err: assert.AnError}
	matcher := NewMatcher(finder, false)

	ids := identifier.ParsedIdentifiers{ISBN13s: []string{"9780000000001"}}
	_, err := matcher.Match(context.Background(), ids)
	assert.ErrorIs(t, err, assert.AnError)
}

// TestMatcher_FindEditionByISBN10_Error: FindEditionByISBN10 error propagates.
func TestMatcher_FindEditionByISBN10_Error(t *testing.T) {
	finder := &errorFinder{isbn10Err: assert.AnError}
	matcher := NewMatcher(finder, false)

	ids := identifier.ParsedIdentifiers{ISBN10s: []string{"0000000001"}}
	_, err := matcher.Match(context.Background(), ids)
	assert.ErrorIs(t, err, assert.AnError)
}

// TestMatcher_SearchBooks_Error: SearchBooks error propagates.
func TestMatcher_SearchBooks_Error(t *testing.T) {
	finder := &errorFinder{searchErr: assert.AnError}
	matcher := NewMatcher(finder, true)

	ids := identifier.ParsedIdentifiers{Title: "Some Title"}
	_, err := matcher.Match(context.Background(), ids)
	assert.ErrorIs(t, err, assert.AnError)
}

// TestMatcher_TitleOnly: Title match with no author.
func TestMatcher_TitleOnly(t *testing.T) {
	book := hardcover.Book{
		ID:   55,
		Slug: "title-only-book",
	}
	finder := &mockFinder{
		booksBySlug:   map[string]*hardcover.Book{},
		editionISBN13: map[string]*hardcover.Edition{},
		editionISBN10: map[string]*hardcover.Edition{},
		searchResults: []hardcover.Book{book},
	}
	matcher := NewMatcher(finder, true)

	ids := identifier.ParsedIdentifiers{Title: "Some Title"} // no Author
	result, err := matcher.Match(context.Background(), ids)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, "title", result.MatchMethod)
	assert.Equal(t, 55, result.BookID)
}

// TestMatcher_Title_EbookEdition: Title match falls back to ebook edition when no physical.
func TestMatcher_Title_EbookEdition(t *testing.T) {
	pages := 180
	formatID := hardcover.ReadingFormatEBook
	book := hardcover.Book{
		ID:   44,
		Slug: "ebook-title-book",
		DefaultEbookEdition: &hardcover.Edition{
			ID:              35,
			BookID:          44,
			Pages:           &pages,
			ReadingFormatID: &formatID,
		},
	}
	finder := &mockFinder{
		booksBySlug:   map[string]*hardcover.Book{},
		editionISBN13: map[string]*hardcover.Edition{},
		editionISBN10: map[string]*hardcover.Edition{},
		searchResults: []hardcover.Book{book},
	}
	matcher := NewMatcher(finder, true)

	ids := identifier.ParsedIdentifiers{Title: "Ebook Title", Author: "Author"}
	result, err := matcher.Match(context.Background(), ids)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, "title", result.MatchMethod)
	assert.Equal(t, 35, result.EditionID)
	assert.Equal(t, 180, result.EditionPages)
}

// TestEditionToResult_NilBook: editionToResult with nil Book field — slug is empty.
func TestEditionToResult_NilBook(t *testing.T) {
	pages := 100
	ed := &hardcover.Edition{
		ID:     50,
		BookID: 200,
		Pages:  &pages,
		Book:   nil,
	}
	result := editionToResult(ed, "isbn13")
	assert.Equal(t, "", result.Slug)
	assert.Equal(t, 200, result.BookID)
}

// TestEditionToResult_NilPages: editionToResult with nil Pages — EditionPages stays 0.
func TestEditionToResult_NilPages(t *testing.T) {
	ed := &hardcover.Edition{
		ID:     51,
		BookID: 201,
		Pages:  nil,
		Book:   &hardcover.Book{ID: 201, Slug: "some-slug"},
	}
	result := editionToResult(ed, "isbn13")
	assert.Equal(t, 0, result.EditionPages)
}

// TestEditionToResult_NilReadingFormatID: editionToResult with nil ReadingFormatID.
func TestEditionToResult_NilReadingFormatID(t *testing.T) {
	pages := 150
	ed := &hardcover.Edition{
		ID:              52,
		BookID:          202,
		Pages:           &pages,
		ReadingFormatID: nil,
	}
	result := editionToResult(ed, "isbn10")
	assert.Equal(t, hardcover.ReadingFormatEBook, result.ReadingFormatID, "nil ReadingFormatID should default to ebook")
}
