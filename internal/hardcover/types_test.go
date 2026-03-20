package hardcover_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/claytono/readest-hardcover-sync/internal/hardcover"
)

func TestBook_SeriesName_NoSeries(t *testing.T) {
	b := hardcover.Book{}
	assert.Equal(t, "", b.SeriesName())
}

func TestBook_SeriesName_WithPosition(t *testing.T) {
	b := hardcover.Book{
		BookSeries: []hardcover.BookSeriesEntry{
			{Series: hardcover.SeriesInfo{Name: "The Series"}, Position: 3},
		},
	}
	assert.Equal(t, "The Series #3", b.SeriesName())
}

func TestBook_SeriesName_WithoutPosition(t *testing.T) {
	b := hardcover.Book{
		BookSeries: []hardcover.BookSeriesEntry{
			{Series: hardcover.SeriesInfo{Name: "Standalone Series"}, Position: 0},
		},
	}
	assert.Equal(t, "Standalone Series", b.SeriesName())
}

func TestBook_CoverURL_Nil(t *testing.T) {
	b := hardcover.Book{}
	assert.Equal(t, "", b.CoverURL())
}

func TestBook_CoverURL_WithImage(t *testing.T) {
	b := hardcover.Book{CachedImage: &hardcover.CachedImage{URL: "https://example.com/cover.jpg"}}
	assert.Equal(t, "https://example.com/cover.jpg", b.CoverURL())
}

func TestBook_AuthorNames_Empty(t *testing.T) {
	b := hardcover.Book{}
	assert.Equal(t, "", b.AuthorNames())
}

func TestBook_AuthorNames_Single(t *testing.T) {
	b := hardcover.Book{
		Contributions: []hardcover.Contribution{
			{Author: hardcover.Author{Name: "Alice"}},
		},
	}
	assert.Equal(t, "Alice", b.AuthorNames())
}

func TestBook_AuthorNames_Multiple(t *testing.T) {
	b := hardcover.Book{
		Contributions: []hardcover.Contribution{
			{Author: hardcover.Author{Name: "Alice"}},
			{Author: hardcover.Author{Name: "Bob"}},
		},
	}
	assert.Equal(t, "Alice, Bob", b.AuthorNames())
}

func TestBook_AuthorNames_FiltersEmpty(t *testing.T) {
	b := hardcover.Book{
		Contributions: []hardcover.Contribution{
			{Author: hardcover.Author{Name: "Alice"}},
			{Author: hardcover.Author{Name: ""}},
			{Author: hardcover.Author{Name: "Charlie"}},
		},
	}
	assert.Equal(t, "Alice, Charlie", b.AuthorNames())
}
