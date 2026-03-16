package sync

import (
	"context"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/claytono/readest-hardcover-sync/internal/hardcover"
)

func newDryRunWithMock() (*dryRunUpdater, *mockProgressUpdater) {
	mock := &mockProgressUpdater{
		meResponse: &hardcover.MeResponse{ID: 7, AccountPrivacySettingID: 3},
		userBook:   &hardcover.UserBook{ID: 42, BookID: 100, StatusID: 2},
	}
	logger := slog.New(slog.NewTextHandler(nil, nil))
	dru := NewDryRunUpdater(mock, logger).(*dryRunUpdater)
	return dru, mock
}

// --- Read method pass-through tests ---

func TestDryRun_GetMe_PassThrough(t *testing.T) {
	dru, mock := newDryRunWithMock()

	got, err := dru.GetMe(context.Background())
	require.NoError(t, err)
	assert.Equal(t, mock.meResponse, got)
}

func TestDryRun_GetMe_PropagatesError(t *testing.T) {
	mock := &mockProgressUpdater{
		meErr: assert.AnError,
	}
	logger := slog.New(slog.NewTextHandler(nil, nil))
	dru := NewDryRunUpdater(mock, logger)

	_, err := dru.GetMe(context.Background())
	assert.ErrorIs(t, err, assert.AnError)
}

func TestDryRun_GetUserBook_PassThrough(t *testing.T) {
	dru, mock := newDryRunWithMock()

	got, err := dru.GetUserBook(context.Background(), 100)
	require.NoError(t, err)
	assert.Equal(t, mock.userBook, got)
}

func TestDryRun_GetUserBook_PropagatesError(t *testing.T) {
	mock := &mockProgressUpdater{
		getUserErr: assert.AnError,
	}
	logger := slog.New(slog.NewTextHandler(nil, nil))
	dru := NewDryRunUpdater(mock, logger)

	_, err := dru.GetUserBook(context.Background(), 1)
	assert.ErrorIs(t, err, assert.AnError)
}

// --- Write method: do not call wrapped implementation ---

func TestDryRun_InsertUserBook_DoesNotCallReal(t *testing.T) {
	dru, mock := newDryRunWithMock()

	_, err := dru.InsertUserBook(context.Background(), 1, 2, 3, nil)
	require.NoError(t, err)
	assert.Empty(t, mock.insertUserBookCalls, "real InsertUserBook must not be called")
}

func TestDryRun_UpdateUserBook_DoesNotCallReal(t *testing.T) {
	dru, mock := newDryRunWithMock()

	_, err := dru.UpdateUserBook(context.Background(), 10, 3)
	require.NoError(t, err)
	assert.Empty(t, mock.updateUserBookCalls, "real UpdateUserBook must not be called")
}

func TestDryRun_InsertUserBookRead_DoesNotCallReal(t *testing.T) {
	dru, mock := newDryRunWithMock()

	_, err := dru.InsertUserBookRead(context.Background(), 5, 200, nil, "2024-01-01", nil)
	require.NoError(t, err)
	assert.Empty(t, mock.insertUserReadCalls, "real InsertUserBookRead must not be called")
}

func TestDryRun_UpdateUserBookRead_DoesNotCallReal(t *testing.T) {
	dru, mock := newDryRunWithMock()

	_, err := dru.UpdateUserBookRead(context.Background(), 20, 300, nil)
	require.NoError(t, err)
	assert.Empty(t, mock.updateUserReadCalls, "real UpdateUserBookRead must not be called")
}

// --- Write method: plausible fake responses ---

func TestDryRun_InsertUserBook_FakeResponse(t *testing.T) {
	dru, _ := newDryRunWithMock()

	bookID := 55
	statusID := 2
	got, err := dru.InsertUserBook(context.Background(), bookID, statusID, 3, nil)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, -1, got.ID)
	assert.Equal(t, bookID, got.BookID)
	assert.Equal(t, statusID, got.StatusID)
}

func TestDryRun_UpdateUserBook_FakeResponse(t *testing.T) {
	dru, _ := newDryRunWithMock()

	id := 10
	statusID := 3
	got, err := dru.UpdateUserBook(context.Background(), id, statusID)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, id, got.ID)
	assert.Equal(t, statusID, got.StatusID)
}

func TestDryRun_InsertUserBookRead_FakeResponse(t *testing.T) {
	dru, _ := newDryRunWithMock()

	progressPages := 150
	got, err := dru.InsertUserBookRead(context.Background(), 5, progressPages, nil, "2024-01-01", nil)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, -1, got.ID)
	require.NotNil(t, got.ProgressPages)
	assert.Equal(t, progressPages, *got.ProgressPages)
}

func TestDryRun_UpdateUserBookRead_FakeResponse(t *testing.T) {
	dru, _ := newDryRunWithMock()

	id := 20
	progressPages := 300
	got, err := dru.UpdateUserBookRead(context.Background(), id, progressPages, nil)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, id, got.ID)
	require.NotNil(t, got.ProgressPages)
	assert.Equal(t, progressPages, *got.ProgressPages)
}

// TestDryRun_GetStatuses_PassThrough: GetStatuses delegates to the real implementation.
func TestDryRun_GetStatuses_PassThrough(t *testing.T) {
	dru, _ := newDryRunWithMock()

	got, err := dru.GetStatuses(context.Background())
	require.NoError(t, err)
	require.NotEmpty(t, got, "GetStatuses should return statuses from the real implementation")
	// The mock returns 3 statuses.
	assert.Len(t, got, 3)
}
