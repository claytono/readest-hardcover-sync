package readest

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
)

const (
	defaultAuthURL = "https://readest.supabase.co/auth/v1"
	encodedAnonKey = "ZXlKaGJHY2lPaUpJVXpJMU5pSXNJblI1Y0NJNklrcFhWQ0o5LmV5SnBjM01pT2lKemRYQmhZbUZ6WlNJc0luSmxaaUk2SW5aaWMzbDRablZ6YW1weFpIaHJhbkZzZVhOaklpd2ljbTlzWlNJNkltRnViMjRpTENKcFlYUWlPakUzTXpReE1qTTJOekVzSW1WNGNDSTZNakEwT1RZNU9UWTNNWDAuM1U1VXFhb3VfMVNnclZlMWVvOXJBcGMwdUtqcWhwUWRVWGh2d1VIbVVmZw=="
)

// tokenResponse is the shape returned by Supabase /auth/v1/token.
type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
	ExpiresAt    int64  `json:"expires_at"`
}

// Auth manages Supabase authentication tokens for the Readest API.
type Auth struct {
	mu           sync.Mutex
	email        string
	password     string
	anonKey      string
	authURL      string
	accessToken  string
	refreshToken string
	expiresAt    time.Time
	expiresIn    int64
	httpClient   *http.Client
}

// NewAuth constructs an Auth, decoding the embedded anon key.
func NewAuth(email, password string) (*Auth, error) {
	decoded, err := base64.StdEncoding.DecodeString(encodedAnonKey)
	if err != nil {
		return nil, fmt.Errorf("decoding anon key: %w", err)
	}
	return &Auth{
		email:      email,
		password:   password,
		anonKey:    string(decoded),
		authURL:    defaultAuthURL,
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}, nil
}

// SetAuthURL overrides the Supabase auth base URL (for testing).
func (a *Auth) SetAuthURL(url string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.authURL = url
}

// SetExpiresAt overrides the token expiry time (for testing).
func (a *Auth) SetExpiresAt(t time.Time) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.expiresAt = t
}

// Token returns a valid access token, refreshing or re-logging in as needed.
func (a *Auth) Token() (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	// No token yet — do a fresh login.
	if a.accessToken == "" {
		return a.login()
	}

	// Token still has more than half its lifetime remaining — return cached.
	halfLife := time.Duration(a.expiresIn/2) * time.Second
	if time.Until(a.expiresAt) > halfLife {
		return a.accessToken, nil
	}

	// Near expiry — try to refresh.
	if err := a.refresh(); err == nil {
		return a.accessToken, nil
	}

	// Refresh failed — fall back to full login.
	return a.login()
}

// login performs a password grant and stores the resulting tokens.
// Caller must hold a.mu.
func (a *Auth) login() (string, error) {
	body, _ := json.Marshal(map[string]string{
		"email":    a.email,
		"password": a.password,
	})

	resp, err := a.postToken("password", body)
	if err != nil {
		return "", err
	}
	a.storeTokens(resp)
	return a.accessToken, nil
}

// refresh performs a refresh_token grant.
// Caller must hold a.mu.
func (a *Auth) refresh() error {
	body, _ := json.Marshal(map[string]string{
		"refresh_token": a.refreshToken,
	})

	resp, err := a.postToken("refresh_token", body)
	if err != nil {
		return err
	}
	a.storeTokens(resp)
	return nil
}

// postToken calls /token?grant_type=<grantType> with the given JSON body.
func (a *Auth) postToken(grantType string, body []byte) (*tokenResponse, error) {
	url := fmt.Sprintf("%s/token?grant_type=%s", a.authURL, grantType)

	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("apikey", a.anonKey)
	req.Header.Set("Content-Type", "application/json")

	httpResp, err := a.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("posting to %s: %w", url, err)
	}
	defer func() { _ = httpResp.Body.Close() }()

	if httpResp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("auth endpoint returned %d", httpResp.StatusCode)
	}

	var tr tokenResponse
	if err := json.NewDecoder(httpResp.Body).Decode(&tr); err != nil {
		return nil, fmt.Errorf("decoding token response: %w", err)
	}
	return &tr, nil
}

// storeTokens updates internal token state from a response.
// Caller must hold a.mu.
func (a *Auth) storeTokens(tr *tokenResponse) {
	a.accessToken = tr.AccessToken
	a.refreshToken = tr.RefreshToken
	a.expiresIn = tr.ExpiresIn
	if tr.ExpiresAt != 0 {
		a.expiresAt = time.Unix(tr.ExpiresAt, 0)
	} else {
		a.expiresAt = time.Now().Add(time.Duration(tr.ExpiresIn) * time.Second)
	}
}
