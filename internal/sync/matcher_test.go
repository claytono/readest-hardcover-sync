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
