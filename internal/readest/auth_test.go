package readest_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/claytono/readest-hardcover-sync/internal/readest"
)

// authResponse mirrors the Supabase token response shape.
type authResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
	ExpiresAt    int64  `json:"expires_at"`
}

func newAuthServer(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return srv
}

func makeLoginResponse(accessToken, refreshToken string) authResponse {
	const expiresIn int64 = 3600
	return authResponse{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		ExpiresIn:    expiresIn,
		ExpiresAt:    time.Now().Unix() + expiresIn,
	}
}

// TestAuth_Login verifies that Token() performs a login, returns the access token,
// and sends the correct headers and body.
func TestAuth_Login(t *testing.T) {
	var capturedReq struct {
		apikey      string
		contentType string
		body        map[string]string
	}

	srv := newAuthServer(t, func(w http.ResponseWriter, r *http.Request) {
		capturedReq.apikey = r.Header.Get("apikey")
		capturedReq.contentType = r.Header.Get("Content-Type")

		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		_ = json.Unmarshal(body, &capturedReq.body)

		resp := makeLoginResponse("access-token-123", "refresh-token-abc")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	})

	auth, err := readest.NewAuth("user@example.com", "secret")
	require.NoError(t, err)
	auth.SetAuthURL(srv.URL)

	token, err := auth.Token()
	require.NoError(t, err)
	assert.Equal(t, "access-token-123", token)

	// Verify the anon key was decoded and sent.
	assert.NotEmpty(t, capturedReq.apikey)
	assert.Equal(t, "application/json", capturedReq.contentType)
	assert.Equal(t, "user@example.com", capturedReq.body["email"])
	assert.Equal(t, "secret", capturedReq.body["password"])
}

// TestAuth_TokenCached verifies that a second call to Token() does not make
// another HTTP request when the token is still valid.
func TestAuth_TokenCached(t *testing.T) {
	var callCount atomic.Int32

	srv := newAuthServer(t, func(w http.ResponseWriter, r *http.Request) {
		callCount.Add(1)
		resp := makeLoginResponse("cached-token", "refresh-token")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	})

	auth, err := readest.NewAuth("user@example.com", "secret")
	require.NoError(t, err)
	auth.SetAuthURL(srv.URL)

	token1, err := auth.Token()
	require.NoError(t, err)
	assert.Equal(t, "cached-token", token1)

	token2, err := auth.Token()
	require.NoError(t, err)
	assert.Equal(t, "cached-token", token2)

	assert.Equal(t, int32(1), callCount.Load(), "expected only 1 HTTP request")
}

// TestAuth_TokenRefresh verifies that Token() hits the refresh endpoint when
// the token is near expiry (less than half of expires_in remaining).
func TestAuth_TokenRefresh(t *testing.T) {
	var requestPaths []string

	srv := newAuthServer(t, func(w http.ResponseWriter, r *http.Request) {
		requestPaths = append(requestPaths, r.URL.RawQuery)

		var resp authResponse
		if strings.Contains(r.URL.RawQuery, "refresh_token") {
			resp = makeLoginResponse("refreshed-token", "new-refresh-token")
		} else {
			resp = makeLoginResponse("initial-token", "old-refresh-token")
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	})

	auth, err := readest.NewAuth("user@example.com", "secret")
	require.NoError(t, err)
	auth.SetAuthURL(srv.URL)

	// First call: login.
	token, err := auth.Token()
	require.NoError(t, err)
	assert.Equal(t, "initial-token", token)

	// Simulate the token being near expiry (less than expiresIn/2 remaining).
	auth.SetExpiresAt(time.Now().Add(30 * time.Second))

	// Second call: should trigger refresh.
	token, err = auth.Token()
	require.NoError(t, err)
	assert.Equal(t, "refreshed-token", token)

	require.Len(t, requestPaths, 2)
	assert.Contains(t, requestPaths[0], "grant_type=password")
	assert.Contains(t, requestPaths[1], "grant_type=refresh_token")
}

// TestAuth_PostToken_NetworkError verifies that postToken (via login) propagates
// an error when the HTTP request itself fails (server closed before call).
func TestAuth_PostToken_NetworkError(t *testing.T) {
	srv := newAuthServer(t, func(w http.ResponseWriter, r *http.Request) {})
	// Close the server immediately so all requests fail at the transport level.
	srv.Close()

	auth, err := readest.NewAuth("user@example.com", "secret")
	require.NoError(t, err)
	auth.SetAuthURL(srv.URL)

	_, err = auth.Token()
	require.Error(t, err)
}

// TestAuth_PostToken_InvalidJSON verifies that postToken returns an error when
// the server responds with 200 but a non-JSON body.
func TestAuth_PostToken_InvalidJSON(t *testing.T) {
	srv := newAuthServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("not json at all"))
	})

	auth, err := readest.NewAuth("user@example.com", "secret")
	require.NoError(t, err)
	auth.SetAuthURL(srv.URL)

	_, err = auth.Token()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "decoding token response")
}

// TestAuth_StoreTokens_ZeroExpiresAt verifies that when the server omits
// expires_at (zero value), storeTokens falls back to computing expiry from
// expires_in so that the token is still considered valid.
func TestAuth_StoreTokens_ZeroExpiresAt(t *testing.T) {
	srv := newAuthServer(t, func(w http.ResponseWriter, r *http.Request) {
		// Omit expires_at (zero) — only provide expires_in.
		resp := authResponse{
			AccessToken:  "zero-expiry-token",
			RefreshToken: "refresh-token",
			ExpiresIn:    3600,
			ExpiresAt:    0,
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	})

	auth, err := readest.NewAuth("user@example.com", "secret")
	require.NoError(t, err)
	auth.SetAuthURL(srv.URL)

	token, err := auth.Token()
	require.NoError(t, err)
	assert.Equal(t, "zero-expiry-token", token)

	// A second call should return the cached token without hitting the server again.
	token2, err := auth.Token()
	require.NoError(t, err)
	assert.Equal(t, "zero-expiry-token", token2)
}

// TestAuth_RefreshFailsRelogin verifies that when a refresh returns 401,
// Token() falls back to a full login.
func TestAuth_RefreshFailsRelogin(t *testing.T) {
	var callCount atomic.Int32

	srv := newAuthServer(t, func(w http.ResponseWriter, r *http.Request) {
		n := callCount.Add(1)

		if strings.Contains(r.URL.RawQuery, "refresh_token") {
			// Simulate failed refresh.
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		// Login responses: first login and re-login after failed refresh.
		var resp authResponse
		if n == 1 {
			resp = makeLoginResponse("first-token", "first-refresh")
		} else {
			resp = makeLoginResponse("relogin-token", "second-refresh")
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	})

	auth, err := readest.NewAuth("user@example.com", "secret")
	require.NoError(t, err)
	auth.SetAuthURL(srv.URL)

	// First call: login.
	token, err := auth.Token()
	require.NoError(t, err)
	assert.Equal(t, "first-token", token)

	// Simulate near-expiry to trigger refresh path.
	auth.SetExpiresAt(time.Now().Add(30 * time.Second))

	// Second call: refresh fails → should re-login.
	token, err = auth.Token()
	require.NoError(t, err)
	assert.Equal(t, "relogin-token", token)

	// Calls: initial login (1) + failed refresh (2) + re-login (3).
	assert.Equal(t, int32(3), callCount.Load())
}
