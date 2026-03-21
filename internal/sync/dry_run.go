package sync

import (
	"context"
	"log/slog"

	"github.com/claytono/readest-hardcover-sync/internal/hardcover"
)

type dryRunUpdater struct {
	real   ProgressUpdater
	logger *slog.Logger
}

// NewDryRunUpdater wraps a ProgressUpdater so that read methods pass through
// to the real implementation and write methods are no-ops that return plausible
// fake responses. The engine's own logging provides the human-readable output.
func NewDryRunUpdater(real ProgressUpdater, logger *slog.Logger) ProgressUpdater {
	return &dryRunUpdater{real: real, logger: logger}
}

// GetMe delegates to the real implementation.
func (d *dryRunUpdater) GetMe(ctx context.Context) (*hardcover.MeResponse, error) {
	return d.real.GetMe(ctx)
}

// GetStatuses delegates to the real implementation.
func (d *dryRunUpdater) GetStatuses(ctx context.Context) ([]hardcover.BookStatus, error) {
	return d.real.GetStatuses(ctx)
}

// GetUserBook delegates to the real implementation.
func (d *dryRunUpdater) GetUserBook(ctx context.Context, bookID int) (*hardcover.UserBook, error) {
	return d.real.GetUserBook(ctx, bookID)
}

// InsertUserBook returns a fake UserBook without calling the real implementation.
func (d *dryRunUpdater) InsertUserBook(_ context.Context, bookID, statusID, _ int, editionID *int) (*hardcover.UserBook, error) {
	return &hardcover.UserBook{
		ID:        0,
		BookID:    bookID,
		StatusID:  statusID,
		EditionID: editionID,
	}, nil
}

// UpdateUserBook returns a fake UserBook without calling the real implementation.
func (d *dryRunUpdater) UpdateUserBook(_ context.Context, id int, statusID int) (*hardcover.UserBook, error) {
	return &hardcover.UserBook{
		ID:       id,
		StatusID: statusID,
	}, nil
}

// InsertUserBookRead returns a fake UserBookRead without calling the real implementation.
func (d *dryRunUpdater) InsertUserBookRead(_ context.Context, _ int, progressPages int, _ *int, _ string, _ *string) (*hardcover.UserBookRead, error) {
	return &hardcover.UserBookRead{
		ID:            0,
		ProgressPages: &progressPages,
	}, nil
}

// UpdateUserBookRead returns a fake UserBookRead without calling the real implementation.
func (d *dryRunUpdater) UpdateUserBookRead(_ context.Context, id int, progressPages int, _ *string) (*hardcover.UserBookRead, error) {
	return &hardcover.UserBookRead{
		ID:            id,
		ProgressPages: &progressPages,
	}, nil
}
