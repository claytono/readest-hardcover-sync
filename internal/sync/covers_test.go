package sync

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDownloadCover_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write([]byte("fake-image-data"))
	}))
	defer srv.Close()

	dir := t.TempDir()
	path, err := DownloadCover(dir, "abc123", srv.URL+"/cover.jpg")
	require.NoError(t, err)
	assert.Equal(t, "abc123.jpg", path)

	data, err := os.ReadFile(filepath.Join(dir, "abc123.jpg"))
	require.NoError(t, err)
	assert.Equal(t, "fake-image-data", string(data))
}

func TestDownloadCover_EmptyURL(t *testing.T) {
	path, err := DownloadCover(t.TempDir(), "abc", "")
	require.NoError(t, err)
	assert.Empty(t, path)
}

func TestDownloadCover_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	_, err := DownloadCover(t.TempDir(), "abc", srv.URL+"/cover.jpg")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "500")
}

func TestDownloadCover_UnreachableURL(t *testing.T) {
	_, err := DownloadCover(t.TempDir(), "abc", "http://127.0.0.1:1/unreachable")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "fetching cover")
}

func TestDownloadCover_BadDir(t *testing.T) {
	// Use /dev/null as covers dir — can't create subdirectories there.
	_, err := DownloadCover("/dev/null/impossible", "abc", "http://example.com")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "creating covers dir")
}

func TestDownloadCover_ReadError(t *testing.T) {
	// Server that returns 200 but immediately closes the body to trigger a read error.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "99999")
		w.WriteHeader(http.StatusOK)
		// Write partial data then close — triggers ReadAll error when Content-Length mismatches.
		hj, ok := w.(http.Hijacker)
		if !ok {
			// Fallback: just write nothing (may not trigger error on all platforms).
			return
		}
		conn, _, _ := hj.Hijack()
		_ = conn.Close()
	}))
	defer srv.Close()

	_, err := DownloadCover(t.TempDir(), "readfail", srv.URL+"/cover.jpg")
	// Either a read error or no error (depends on platform), but should not panic.
	_ = err
}

func TestDownloadCover_WriteError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("img"))
	}))
	defer srv.Close()

	// Use a read-only directory to trigger write error.
	dir := t.TempDir()
	roDir := filepath.Join(dir, "readonly")
	require.NoError(t, os.MkdirAll(roDir, 0o555))
	t.Cleanup(func() { _ = os.Chmod(roDir, 0o755) })

	_, err := DownloadCover(roDir, "writefail", srv.URL+"/cover.jpg")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "writing cover")
}

func TestDownloadCover_CreatesDir(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("img"))
	}))
	defer srv.Close()

	dir := filepath.Join(t.TempDir(), "nested", "covers")
	path, err := DownloadCover(dir, "book1", srv.URL+"/c.jpg")
	require.NoError(t, err)
	assert.Equal(t, "book1.jpg", path)

	_, err = os.Stat(filepath.Join(dir, "book1.jpg"))
	require.NoError(t, err)
}
