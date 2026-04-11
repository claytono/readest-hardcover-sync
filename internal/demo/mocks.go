package demo

import (
	"context"
	"fmt"
	"strings"

	"github.com/claytono/readest-hardcover-sync/internal/hardcover"
)

// demoFinder implements syncsvc.BookFinder using the demo book data.
type demoFinder struct {
	books []hardcover.Book
}

func newDemoFinder() *demoFinder {
	var books []hardcover.Book
	for _, db := range demoBooks() {
		if db.state.HardcoverBookID == 0 {
			continue
		}
		b := hardcover.Book{
			ID:            db.state.HardcoverBookID,
			Title:         db.state.Title,
			Slug:          db.state.HardcoverSlug,
			Contributions: []hardcover.Contribution{{Author: hardcover.Author{Name: db.state.Author}}},
		}
		if db.state.Series != "" {
			b.BookSeries = parseSeriesEntry(db.state.Series)
		}
		pages := db.state.EditionPages
		rfid := 4
		b.DefaultEbookEdition = &hardcover.Edition{
			ID:              db.state.EditionID,
			Pages:           &pages,
			ReadingFormatID: &rfid,
		}
		books = append(books, b)
	}
	return &demoFinder{books: books}
}

func parseSeriesEntry(series string) []hardcover.BookSeriesEntry {
	// Format: "Series Name #N"
	idx := strings.LastIndex(series, " #")
	if idx < 0 {
		return []hardcover.BookSeriesEntry{{Series: hardcover.SeriesInfo{Name: series}}}
	}
	name := series[:idx]
	var pos float64
	if _, err := fmt.Sscanf(series[idx+2:], "%f", &pos); err == nil {
		return []hardcover.BookSeriesEntry{{Series: hardcover.SeriesInfo{Name: name}, Position: pos}}
	}
	return []hardcover.BookSeriesEntry{{Series: hardcover.SeriesInfo{Name: series}}}
}

func (f *demoFinder) SearchBooks(_ context.Context, query string) ([]hardcover.Book, error) {
	q := strings.ToLower(query)
	var results []hardcover.Book
	for _, b := range f.books {
		if strings.Contains(strings.ToLower(b.Title), q) || strings.Contains(strings.ToLower(b.AuthorNames()), q) {
			results = append(results, b)
		}
	}
	return results, nil
}

func (f *demoFinder) FindBookBySlug(_ context.Context, slug string) (*hardcover.Book, error) {
	for _, b := range f.books {
		if b.Slug == slug {
			return &b, nil
		}
	}
	return nil, nil
}

func (f *demoFinder) FindEditionByISBN13(_ context.Context, _ string) (*hardcover.Edition, error) {
	return nil, nil
}

func (f *demoFinder) FindEditionByISBN10(_ context.Context, _ string) (*hardcover.Edition, error) {
	return nil, nil
}

// demoUpdater implements syncsvc.ProgressUpdater with no-op writes.
type demoUpdater struct{}

func (d *demoUpdater) GetMe(_ context.Context) (*hardcover.MeResponse, error) {
	return &hardcover.MeResponse{ID: 1, AccountPrivacySettingID: 1}, nil
}

func (d *demoUpdater) GetStatuses(_ context.Context) ([]hardcover.BookStatus, error) {
	return []hardcover.BookStatus{
		{ID: 1, Status: "Want to Read"},
		{ID: 2, Status: "Currently Reading"},
		{ID: 3, Status: "Read"},
		{ID: 4, Status: "Paused"},
		{ID: 5, Status: "Did Not Finish"},
		{ID: 6, Status: "Ignored"},
	}, nil
}

func (d *demoUpdater) GetUserBook(_ context.Context, _ int) (*hardcover.UserBook, error) {
	return nil, nil
}

func (d *demoUpdater) InsertUserBook(_ context.Context, bookID, statusID, _ int, editionID *int) (*hardcover.UserBook, error) {
	return &hardcover.UserBook{ID: 9999, BookID: bookID, StatusID: statusID, EditionID: editionID}, nil
}

func (d *demoUpdater) UpdateUserBook(_ context.Context, id int, statusID int) (*hardcover.UserBook, error) {
	return &hardcover.UserBook{ID: id, StatusID: statusID}, nil
}

func (d *demoUpdater) InsertUserBookRead(_ context.Context, _ int, progressPages int, editionID *int, _ string, _ *string) (*hardcover.UserBookRead, error) {
	return &hardcover.UserBookRead{ID: 9999, ProgressPages: &progressPages, EditionID: editionID}, nil
}

func (d *demoUpdater) UpdateUserBookRead(_ context.Context, id int, progressPages int, _ *string) (*hardcover.UserBookRead, error) {
	return &hardcover.UserBookRead{ID: id, ProgressPages: &progressPages}, nil
}
