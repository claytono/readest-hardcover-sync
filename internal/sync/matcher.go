package sync

import (
	"context"
	"strings"

	"github.com/claytono/readest-hardcover-sync/internal/hardcover"
	"github.com/claytono/readest-hardcover-sync/internal/identifier"
)

// MatchResult holds the resolved book and edition details from Hardcover.
type MatchResult struct {
	BookID          int
	Slug            string
	EditionID       int
	EditionPages    int
	ReadingFormatID int
	MatchMethod     string // "slug", "isbn13", "isbn10", "title", "manual"
	CoverURL        string // from Hardcover's cached_image
	Series          string // e.g., "Dungeon Crawler Carl #3"
}

// Matcher resolves a ParsedIdentifiers set to a Hardcover book/edition.
type Matcher struct {
	finder           BookFinder
	enableTitleMatch bool
}

// NewMatcher constructs a Matcher with the given finder and title-match flag.
func NewMatcher(finder BookFinder, enableTitleMatch bool) *Matcher {
	return &Matcher{finder: finder, enableTitleMatch: enableTitleMatch}
}

// Match walks the fallback chain and returns the first successful MatchResult,
// or nil, nil when nothing is found.
func (m *Matcher) Match(ctx context.Context, ids identifier.ParsedIdentifiers) (*MatchResult, error) {
	// 1. Try each HardcoverSlug.
	for _, slug := range ids.HardcoverSlugs {
		book, err := m.finder.FindBookBySlug(ctx, slug)
		if err != nil {
			return nil, err
		}
		if book == nil {
			continue
		}

		// Prefer the ebook edition, fall back to physical.
		ed := book.DefaultEbookEdition
		if ed == nil {
			ed = book.DefaultPhysicalEdition
		}

		result := &MatchResult{
			BookID:      book.ID,
			Slug:        book.Slug,
			CoverURL:    book.CoverURL(),
			Series:      book.SeriesName(),
			MatchMethod: "slug",
		}
		if ed != nil {
			result.EditionID = ed.ID
			if ed.Pages != nil {
				result.EditionPages = *ed.Pages
			}
			if ed.ReadingFormatID != nil {
				result.ReadingFormatID = *ed.ReadingFormatID
			}
		}
		if result.ReadingFormatID == 0 {
			result.ReadingFormatID = hardcover.ReadingFormatEBook
		}
		return result, nil
	}

	// 2. Try each ISBN13.
	for _, isbn := range ids.ISBN13s {
		ed, err := m.finder.FindEditionByISBN13(ctx, isbn)
		if err != nil {
			return nil, err
		}
		if ed != nil {
			return editionToResult(ed, "isbn13"), nil
		}
	}

	// 3. Try each ISBN10.
	for _, isbn := range ids.ISBN10s {
		ed, err := m.finder.FindEditionByISBN10(ctx, isbn)
		if err != nil {
			return nil, err
		}
		if ed != nil {
			return editionToResult(ed, "isbn10"), nil
		}
	}

	// 4. Title search (optional).
	if m.enableTitleMatch && ids.Title != "" {
		query := ids.Title
		if ids.Author != "" {
			query = query + " " + ids.Author
		}
		books, err := m.finder.SearchBooks(ctx, strings.TrimSpace(query))
		if err != nil {
			return nil, err
		}
		if len(books) > 0 {
			book := &books[0]
			result := &MatchResult{
				BookID:      book.ID,
				Slug:        book.Slug,
				CoverURL:    book.CoverURL(),
				Series:      book.SeriesName(),
				MatchMethod: "title",
			}
			ed := book.DefaultPhysicalEdition
			if ed == nil {
				ed = book.DefaultEbookEdition
			}
			if ed != nil {
				result.EditionID = ed.ID
				if ed.Pages != nil {
					result.EditionPages = *ed.Pages
				}
				if ed.ReadingFormatID != nil {
					result.ReadingFormatID = *ed.ReadingFormatID
				}
			}
			return result, nil
		}
	}

	return nil, nil
}

// editionToResult builds a MatchResult from an Edition and the match method label.
func editionToResult(ed *hardcover.Edition, method string) *MatchResult {
	result := &MatchResult{
		BookID:      ed.BookID,
		EditionID:   ed.ID,
		MatchMethod: method,
	}
	if ed.Book != nil {
		result.Slug = ed.Book.Slug
		result.CoverURL = ed.Book.CoverURL()
		result.Series = ed.Book.SeriesName()
	}
	if ed.Pages != nil {
		result.EditionPages = *ed.Pages
	}
	if ed.ReadingFormatID != nil {
		result.ReadingFormatID = *ed.ReadingFormatID
	}
	if result.ReadingFormatID == 0 {
		result.ReadingFormatID = hardcover.ReadingFormatEBook
	}
	return result
}
