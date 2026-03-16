package readest_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/claytono/readest-hardcover-sync/internal/readest"
)

// newTestAuth creates an Auth backed by a test httptest server that always
// returns "test-bearer-token".
func newTestAuth(t *testing.T) *readest.Auth {
	t.Helper()
	const token = "test-bearer-token"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := authResponse{
			AccessToken:  token,
			RefreshToken: "refresh-token",
			ExpiresIn:    3600,
			ExpiresAt:    time.Now().Unix() + 3600,
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	t.Cleanup(srv.Close)

	auth, err := readest.NewAuth("user@example.com", "password")
	require.NoError(t, err)
	auth.SetAuthURL(srv.URL)

	return auth
}

// TestClient_PullBooks verifies that PullBooks calls the correct URL with query
// params, sends the Authorization header, and decodes the returned books.
func TestClient_PullBooks(t *testing.T) {
	var capturedURL string
	var capturedAuthHeader string

	syncSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedURL = r.URL.String()
		capturedAuthHeader = r.Header.Get("Authorization")

		result := readest.SyncResult{
			Books: []readest.DBBook{
				{
					UserID:   "user-1",
					BookHash: "aaaa1111bbbb2222cccc3333dddd4444",
					Title:    "The Go Programming Language",
					Author:   "Donovan",
					Format:   "EPUB",
				},
				{
					UserID:   "user-1",
					BookHash: "1234567890abcdef1234567890abcdef",
					Title:    "Clean Code",
					Author:   "Martin",
					Format:   "EPUB",
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(result)
	}))
	t.Cleanup(syncSrv.Close)

	auth := newTestAuth(t)
	client := readest.NewClient(auth)
	client.SetBaseURL(syncSrv.URL)

	books, err := client.PullBooks(t.Context(), 1700000000000)
	require.NoError(t, err)
	require.Len(t, books, 2)

	assert.Equal(t, "/sync?since=1700000000000&type=books", capturedURL)
	assert.Equal(t, "Bearer test-bearer-token", capturedAuthHeader)

	assert.Equal(t, "The Go Programming Language", books[0].Title)
	assert.Equal(t, "Clean Code", books[1].Title)
}

// TestClient_PullBooks_FiltersDummy verifies that PullBooks filters out books
// whose BookHash equals the dummy hash.
func TestClient_PullBooks_FiltersDummy(t *testing.T) {
	syncSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		result := readest.SyncResult{
			Books: []readest.DBBook{
				{
					UserID:   "user-1",
					BookHash: readest.DummyBookHash,
					Title:    "Dummy Book",
					Format:   "EPUB",
				},
				{
					UserID:   "user-1",
					BookHash: "aaaa1111bbbb2222cccc3333dddd4444",
					Title:    "Real Book",
					Format:   "EPUB",
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(result)
	}))
	t.Cleanup(syncSrv.Close)

	auth := newTestAuth(t)
	client := readest.NewClient(auth)
	client.SetBaseURL(syncSrv.URL)

	books, err := client.PullBooks(t.Context(), 0)
	require.NoError(t, err)
	require.Len(t, books, 1)
	assert.Equal(t, "Real Book", books[0].Title)
}

// TestClient_PullConfigs verifies that PullConfigs calls the correct endpoint
// and returns configs with the progress string preserved as-is.
func TestClient_PullConfigs(t *testing.T) {
	var capturedURL string

	syncSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedURL = r.URL.String()

		result := readest.SyncResult{
			Configs: []readest.DBBookConfig{
				{
					UserID:   "user-1",
					BookHash: "aaaa1111bbbb2222cccc3333dddd4444",
					Progress: "[1500, 30000]",
				},
				{
					UserID:   "user-1",
					BookHash: "1234567890abcdef1234567890abcdef",
					Progress: "[500, 10000]",
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(result)
	}))
	t.Cleanup(syncSrv.Close)

	auth := newTestAuth(t)
	client := readest.NewClient(auth)
	client.SetBaseURL(syncSrv.URL)

	configs, err := client.PullConfigs(t.Context(), 1700000000000)
	require.NoError(t, err)
	require.Len(t, configs, 2)

	assert.Equal(t, "/sync?since=1700000000000&type=configs", capturedURL)
	assert.Equal(t, "[1500, 30000]", configs[0].Progress)
	assert.Equal(t, "[500, 10000]", configs[1].Progress)
}

// TestClient_AuthError verifies that a non-200 response from the sync API
// results in an error being returned.
func TestClient_AuthError(t *testing.T) {
	syncSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	t.Cleanup(syncSrv.Close)

	auth := newTestAuth(t)
	client := readest.NewClient(auth)
	client.SetBaseURL(syncSrv.URL)

	books, err := client.PullBooks(t.Context(), 0)
	assert.Error(t, err)
	assert.Nil(t, books)
	assert.Contains(t, err.Error(), "403")
}
