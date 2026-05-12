package hardcover

import (
	"fmt"
	"strings"
)

type Book struct {
	ID                     int               `json:"id"`
	Title                  string            `json:"title"`
	Slug                   string            `json:"slug"`
	CachedImage            *CachedImage      `json:"cached_image,omitempty"`
	Contributions          []Contribution    `json:"contributions,omitempty"`
	BookSeries             []BookSeriesEntry `json:"book_series,omitempty"`
	DefaultEbookEdition    *Edition          `json:"default_ebook_edition,omitempty"`
	DefaultPhysicalEdition *Edition          `json:"default_physical_edition,omitempty"`
}

// SeriesName returns "Series Name #N" or empty string.
func (b Book) SeriesName() string {
	if len(b.BookSeries) == 0 {
		return ""
	}
	s := b.BookSeries[0]
	if s.Position > 0 {
		return fmt.Sprintf("%s #%g", s.Series.Name, s.Position)
	}
	return s.Series.Name
}

// CoverURL returns the cover image URL or empty string.
func (b Book) CoverURL() string {
	if b.CachedImage != nil {
		return b.CachedImage.URL
	}
	return ""
}

// AuthorNames returns a comma-separated list of author names.
func (b Book) AuthorNames() string {
	names := make([]string, 0, len(b.Contributions))
	for _, c := range b.Contributions {
		if c.Author.Name != "" {
			names = append(names, c.Author.Name)
		}
	}
	return strings.Join(names, ", ")
}

type Contribution struct {
	Author Author `json:"author"`
}

type Author struct {
	Name string `json:"name"`
}

type Edition struct {
	ID              int          `json:"id"`
	BookID          int          `json:"book_id"`
	Title           string       `json:"title,omitempty"`
	Pages           *int         `json:"pages"`
	ISBN13          string       `json:"isbn_13"`
	ISBN10          string       `json:"isbn_10"`
	ASIN            string       `json:"asin"`
	EditionFormat   string       `json:"edition_format"`
	ReadingFormatID *int         `json:"reading_format_id"`
	CachedImage     *CachedImage `json:"cached_image,omitempty"`
	Publisher       *Publisher   `json:"publisher,omitempty"`
	Book            *Book        `json:"book,omitempty"`
}

type CachedImage struct {
	URL string `json:"url"`
}

type BookSeriesEntry struct {
	Series   SeriesInfo `json:"series"`
	Position float64    `json:"position"`
}

type SeriesInfo struct {
	Name string `json:"name"`
}

type Publisher struct {
	Name string `json:"name"`
}

type UserBook struct {
	ID            int            `json:"id"`
	BookID        int            `json:"book_id"`
	StatusID      int            `json:"status_id"`
	EditionID     *int           `json:"edition_id"`
	UserBookReads []UserBookRead `json:"user_book_reads,omitempty"`
}

type UserBookRead struct {
	ID            int    `json:"id"`
	ProgressPages *int   `json:"progress_pages"`
	StartedAt     string `json:"started_at,omitempty"`
	FinishedAt    string `json:"finished_at,omitempty"`
	EditionID     *int   `json:"edition_id"`
}

type MeResponse struct {
	ID                      int `json:"id"`
	AccountPrivacySettingID int `json:"account_privacy_setting_id"`
}

type SearchResult struct {
	IDs []int `json:"ids"`
}

type BookStatus struct {
	ID     int    `json:"id"`
	Status string `json:"status"`
}

// StatusNone indicates a book has no Hardcover status yet (not a real Hardcover
// status — used internally to represent the absence of a status).
const StatusNone = 0

// Book status IDs from Hardcover's user_book_statuses table.
const (
	StatusWantToRead       = 1
	StatusCurrentlyReading = 2
	StatusRead             = 3
	StatusPaused           = 4
	StatusDidNotFinish     = 5
	StatusIgnored          = 6
)

const ReadingFormatEBook = 4
