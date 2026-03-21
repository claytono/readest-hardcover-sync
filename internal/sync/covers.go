package sync

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

const coverMaxBytes = 10 << 20 // 10 MB

var coverHTTPClient = &http.Client{Timeout: 30 * time.Second}

// DownloadCover fetches a cover image from url and saves it to coversDir/{bookHash}.jpg.
// Returns the filename suitable for serving via HTTP, or empty string if url is empty.
func DownloadCover(coversDir, bookHash, url string) (string, error) {
	if url == "" {
		return "", nil
	}

	if err := os.MkdirAll(coversDir, 0o755); err != nil {
		return "", fmt.Errorf("creating covers dir: %w", err)
	}

	resp, err := coverHTTPClient.Get(url) //nolint:gosec // URL comes from Hardcover API
	if err != nil {
		return "", fmt.Errorf("fetching cover: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("cover fetch returned %d", resp.StatusCode)
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, coverMaxBytes))
	if err != nil {
		return "", fmt.Errorf("reading cover: %w", err)
	}

	filename := bookHash + ".jpg"
	if err := os.WriteFile(filepath.Join(coversDir, filename), data, 0o644); err != nil {
		return "", fmt.Errorf("writing cover: %w", err)
	}

	return filename, nil
}
