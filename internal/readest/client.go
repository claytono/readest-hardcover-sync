package readest

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

const defaultBaseURL = "https://web.readest.com/api"

// Client calls the Readest sync API on behalf of an authenticated user.
type Client struct {
	auth    *Auth
	baseURL string
	http    *http.Client
}

// NewClient constructs a Client using the given Auth for token management.
func NewClient(auth *Auth) *Client {
	return &Client{
		auth:    auth,
		baseURL: defaultBaseURL,
		http:    &http.Client{Timeout: 15 * time.Second},
	}
}

// SetBaseURL overrides the API base URL (for testing).
func (c *Client) SetBaseURL(url string) {
	c.baseURL = url
}

// PullBooks fetches books updated since the given Unix millisecond timestamp.
// Dummy books (placeholder entries with a zeroed hash) are filtered out.
func (c *Client) PullBooks(ctx context.Context, since int64) ([]DBBook, error) {
	result, err := c.pull(ctx, "books", since)
	if err != nil {
		return nil, err
	}

	books := result.Books[:0]
	for i := range result.Books {
		if !result.Books[i].IsDummyBook() {
			books = append(books, result.Books[i])
		}
	}
	return books, nil
}

// PullConfigs fetches book configs updated since the given Unix millisecond timestamp.
func (c *Client) PullConfigs(ctx context.Context, since int64) ([]DBBookConfig, error) {
	result, err := c.pull(ctx, "configs", since)
	if err != nil {
		return nil, err
	}
	return result.Configs, nil
}

// pull fetches a SyncResult from GET /api/sync?since={ms}&type={dataType}.
func (c *Client) pull(ctx context.Context, dataType string, since int64) (*SyncResult, error) {
	token, err := c.auth.Token()
	if err != nil {
		return nil, fmt.Errorf("getting auth token: %w", err)
	}

	url := fmt.Sprintf("%s/sync?since=%d&type=%s", c.baseURL, since, dataType)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("sync API returned %d", resp.StatusCode)
	}

	var result SyncResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding sync response: %w", err)
	}
	return &result, nil
}
