package notifications

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/claytono/readest-hardcover-sync/internal/state"
)

type failingRoundTripper struct {
	err error
}

func (f failingRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, f.err
}

func newTestSlack(t *testing.T, srv *httptest.Server, baseURL string) *Slack {
	t.Helper()
	return &Slack{
		webhookURL: srv.URL,
		baseURL:    strings.TrimRight(baseURL, "/"),
		client:     srv.Client(),
	}
}

func requireSlackSectionText(t *testing.T, block map[string]any) string {
	t.Helper()
	require.Equal(t, "section", block["type"])
	textBlock, ok := block["text"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "mrkdwn", textBlock["type"])
	text, ok := textBlock["text"].(string)
	require.True(t, ok)
	return text
}

func requireSlackImageBlock(t *testing.T, block map[string]any) (string, string) {
	t.Helper()
	require.Equal(t, "image", block["type"])
	imageURL, ok := block["image_url"].(string)
	require.True(t, ok)
	altText, ok := block["alt_text"].(string)
	require.True(t, ok)
	return imageURL, altText
}

func TestSlack_NotifyBookAdded_WithCover(t *testing.T) {
	var payload slackPayload
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "application/json", r.Header.Get("Content-Type"))
		require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	notifier := newTestSlack(t, srv, "https://readest.example.com")

	book := state.BookState{
		BookHash:      "abc123",
		Title:         "Test Book",
		Author:        "Test Author",
		HardcoverSlug: "test-book",
		MatchMethod:   "isbn13",
		CoverURL:      "https://assets.example.com/abc123.jpg",
	}

	require.NoError(t, notifier.NotifyBookAdded(context.Background(), book, true))

	assert.Equal(t, 1, requests)
	require.Len(t, payload.Blocks, 2)
	assert.Empty(t, payload.Text)
	text := requireSlackSectionText(t, payload.Blocks[0])
	assert.Equal(t, "New book added: <https://readest.example.com/books?book=abc123|Test Book> by Test Author\nLinked automatically via ISBN-13 to <https://hardcover.app/books/test-book|test-book>", text)
	assert.NotContains(t, text, ":question:")

	imageURL, altText := requireSlackImageBlock(t, payload.Blocks[1])
	assert.Equal(t, "https://assets.example.com/abc123.jpg", imageURL)
	assert.Equal(t, "Cover for Test Book", altText)
}

func TestSlack_NotifyBookAdded_NoCoverUsesQuestionEmoji(t *testing.T) {
	var payload slackPayload
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	notifier := newTestSlack(t, srv, "https://readest.example.com/app/")

	book := state.BookState{
		BookHash: "hash with spaces",
		Title:    "Unmatched Book",
	}

	require.NoError(t, notifier.NotifyBookAdded(context.Background(), book, false))

	assert.Equal(t, 1, requests)
	require.Len(t, payload.Blocks, 1)
	assert.Empty(t, payload.Text)
	text := requireSlackSectionText(t, payload.Blocks[0])
	assert.Equal(t, ":question: Action needed: <https://readest.example.com/app/books?book=hash+with+spaces|Unmatched Book>\nCould not link automatically. Open the book and choose a Hardcover match.", text)
}

func TestSlack_NotifyBookAdded_InvalidCoverUsesQuestionEmoji(t *testing.T) {
	var payload slackPayload
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	notifier := newTestSlack(t, srv, "https://readest.example.com")

	require.NoError(t, notifier.NotifyBookAdded(context.Background(), state.BookState{
		BookHash: "invalid-cover",
		Title:    "Invalid Cover",
		CoverURL: "ftp://assets.example.com/cover.jpg",
	}, true))

	assert.Equal(t, 1, requests)
	require.Len(t, payload.Blocks, 1)
	assert.Empty(t, payload.Text)
	text := requireSlackSectionText(t, payload.Blocks[0])
	assert.Equal(t, ":question: New book added: <https://readest.example.com/books?book=invalid-cover|Invalid Cover>\nLinked automatically via auto", text)
}

func TestSlack_NotifyBookCompleted(t *testing.T) {
	var payload slackPayload
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	notifier := newTestSlack(t, srv, "https://readest.example.com")

	require.NoError(t, notifier.NotifyBookCompleted(context.Background(), state.BookState{
		BookHash:      "done",
		Title:         "Finished Book",
		HardcoverSlug: "finished-book",
		CoverURL:      "https://assets.example.com/done.jpg",
	}))

	assert.Equal(t, 1, requests)
	assert.Empty(t, payload.Text)
	require.Len(t, payload.Blocks, 2)
	assert.Equal(t, "Book marked complete: <https://readest.example.com/books?book=done|Finished Book>\nMarked complete in Hardcover\nHardcover slug <https://hardcover.app/books/finished-book|finished-book>", requireSlackSectionText(t, payload.Blocks[0]))
	imageURL, altText := requireSlackImageBlock(t, payload.Blocks[1])
	assert.Equal(t, "https://assets.example.com/done.jpg", imageURL)
	assert.Equal(t, "Cover for Finished Book", altText)
}

func TestSlack_NotifyCriticalError(t *testing.T) {
	var payload slackPayload
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	notifier := newTestSlack(t, srv, "https://readest.example.com")

	require.NoError(t, notifier.NotifyCriticalError(context.Background(), assert.AnError))

	assert.Equal(t, 1, requests)
	assert.Empty(t, payload.Text)
	require.Len(t, payload.Blocks, 1)
	assert.Equal(t, ":rotating_light: Critical sync error\n```assert.AnError general error for testing```\n<https://readest.example.com/books|Open web UI>", requireSlackSectionText(t, payload.Blocks[0]))
}

func TestSlack_PostError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad hook", http.StatusBadRequest)
	}))
	defer srv.Close()

	notifier := newTestSlack(t, srv, "https://readest.example.com")

	err := notifier.NotifyBookCompleted(context.Background(), state.BookState{BookHash: "x", Title: "X"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "400")
	assert.Contains(t, err.Error(), "bad hook")
}

func TestSlack_PostTransportErrorRedactsWebhookURL(t *testing.T) {
	webhookURL := "https://hooks.slack.com/services/T000/B000/super-secret-token"
	notifier, err := NewSlack(SlackOptions{
		WebhookURL: webhookURL,
		BaseURL:    "https://readest.example.com",
		HTTPClient: &http.Client{
			Transport: failingRoundTripper{err: errors.New("dial tcp: connection refused")},
		},
	})
	require.NoError(t, err)

	err = notifier.NotifyBookCompleted(context.Background(), state.BookState{BookHash: "x", Title: "X"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "post Slack webhook")
	assert.Contains(t, err.Error(), "connection refused")
	assert.NotContains(t, err.Error(), webhookURL)
	assert.NotContains(t, err.Error(), "super-secret-token")
}

func TestSlack_PostMarshalError(t *testing.T) {
	notifier := &Slack{
		webhookURL: "https://hooks.slack.com/services/test",
		client:     http.DefaultClient,
	}

	err := notifier.post(context.Background(), slackPayload{
		Blocks: []map[string]any{{"bad": func() {}}},
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "marshal Slack payload")
}

func TestSlack_PostCreateRequestError(t *testing.T) {
	notifier := &Slack{
		webhookURL: "http://[::1",
		client:     http.DefaultClient,
	}

	err := notifier.post(context.Background(), slackPayload{})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "create Slack request")
}

func TestSlack_HelperFormattingBranches(t *testing.T) {
	notifier := &Slack{baseURL: "https://readest.example.com/app"}

	assert.Equal(t, "https://readest.example.com/app/books?book=hash+with+spaces", notifier.bookURL(state.BookState{BookHash: "hash with spaces"}))
	assert.Equal(t, "://bad/books", (&Slack{baseURL: "://bad"}).bookURL(state.BookState{BookHash: "hash"}))

	assert.Empty(t, notifier.coverURL(state.BookState{}))
	assert.Empty(t, notifier.coverURL(state.BookState{CoverURL: "ftp://assets.example.com/cover.jpg"}))
	assert.Empty(t, notifier.coverURL(state.BookState{CoverURL: "://bad"}))
	assert.Equal(t, "http://assets.example.com/cover.jpg", notifier.coverURL(state.BookState{CoverURL: "http://assets.example.com/cover.jpg"}))

	assert.Equal(t, "Linked automatically via auto", notifier.linkedLine(state.BookState{}, true))
	assert.Equal(t, "Linked automatically via Hardcover slug <https://hardcover.app/books/test-book|test-book>", notifier.linkedLine(state.BookState{
		HardcoverSlug: "test-book",
		MatchMethod:   "slug",
	}, true))
	assert.Equal(t, "Linked automatically via custom¦method to <https://hardcover.app/books/test-book|test-book>", notifier.linkedLine(state.BookState{
		HardcoverSlug: "test-book",
		MatchMethod:   "custom|method",
	}, true))
	assert.Empty(t, notifier.hardcoverLine(state.BookState{}))

	assert.Equal(t, "ISBN-10", matchMethodLabel("isbn10"))
	assert.Equal(t, "title search", matchMethodLabel("title"))
	assert.Equal(t, "Hardcover slug", matchMethodLabel("slug"))
	assert.Equal(t, "auto", matchMethodLabel(""))
	assert.Equal(t, "custom&lt;method&gt;", matchMethodLabel("custom<method>"))

	assert.Empty(t, authorSuffix(""))
	assert.Equal(t, "Untitled book", titleOrUntitled(state.BookState{}))
	assert.Equal(t, "line one line two", plainText("line one\nline two"))
	assert.Equal(t, "abc", truncate("abcdef", 3))
	assert.Equal(t, "ab...", truncate("abcdef", 5))
}

func TestNewSlack_Validation(t *testing.T) {
	_, err := NewSlack(SlackOptions{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "webhook")

	_, err = NewSlack(SlackOptions{WebhookURL: "https://hooks.slack.com/services/test"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "public base URL")

	_, err = NewSlack(SlackOptions{WebhookURL: "not a url", BaseURL: "https://readest.example.com"})
	require.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "webhook") || strings.Contains(err.Error(), "URL"))

	_, err = NewSlack(SlackOptions{WebhookURL: "http://hooks.slack.com/services/test", BaseURL: "https://readest.example.com"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "https")

	_, err = NewSlack(SlackOptions{WebhookURL: "/services/test", BaseURL: "https://readest.example.com"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "host")

	_, err = NewSlack(SlackOptions{WebhookURL: "https://hooks.slack.com/services/test", BaseURL: "/app"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "host")

	_, err = NewSlack(SlackOptions{WebhookURL: "https://hooks.slack.com/services/test", BaseURL: "ftp://readest.example.com"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "http or https")

	_, err = NewSlack(SlackOptions{WebhookURL: "https://hooks.slack.com/services/test", BaseURL: "http://localhost:8080"})
	require.NoError(t, err)
}
