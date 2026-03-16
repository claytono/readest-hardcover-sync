package sync

import (
	"context"

	"github.com/claytono/readest-hardcover-sync/internal/hardcover"
	"github.com/claytono/readest-hardcover-sync/internal/readest"
)

// ReadestPuller fetches books and configs from the Readest sync API.
type ReadestPuller interface {
	PullBooks(ctx context.Context, since int64) ([]readest.DBBook, error)
	PullConfigs(ctx context.Context, since int64) ([]readest.DBBookConfig, error)
}

// BookFinder looks up Hardcover books and editions by various identifiers.
type BookFinder interface {
	FindBookBySlug(ctx context.Context, slug string) (*hardcover.Book, error)
	FindEditionByISBN13(ctx context.Context, isbn string) (*hardcover.Edition, error)
	FindEditionByISBN10(ctx context.Context, isbn string) (*hardcover.Edition, error)
	SearchBooks(ctx context.Context, query string) ([]hardcover.Book, error)
}

// ProgressUpdater manages user reading records on Hardcover.
type ProgressUpdater interface {
	GetMe(ctx context.Context) (*hardcover.MeResponse, error)
	GetStatuses(ctx context.Context) ([]hardcover.BookStatus, error)
	GetUserBook(ctx context.Context, bookID int) (*hardcover.UserBook, error)
	InsertUserBook(ctx context.Context, bookID, statusID, privacySettingID int, editionID *int) (*hardcover.UserBook, error)
	UpdateUserBook(ctx context.Context, id int, statusID int) (*hardcover.UserBook, error)
	InsertUserBookRead(ctx context.Context, userBookID, progressPages int, editionID *int, startedAt string, finishedAt *string) (*hardcover.UserBookRead, error)
	UpdateUserBookRead(ctx context.Context, id int, progressPages int, finishedAt *string) (*hardcover.UserBookRead, error)
}
