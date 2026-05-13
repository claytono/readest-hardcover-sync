package notifications

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/claytono/readest-hardcover-sync/internal/state"
)

// SlackOptions configures a Slack incoming-webhook notifier.
type SlackOptions struct {
	WebhookURL string
	BaseURL    string
	HTTPClient *http.Client
}

// Slack sends best-effort notifications to a Slack incoming webhook.
type Slack struct {
	webhookURL string
	baseURL    string
	client     *http.Client
}

// NewSlack creates a Slack incoming-webhook notifier.
func NewSlack(opts SlackOptions) (*Slack, error) {
	if opts.WebhookURL == "" {
		return nil, errors.New("slack webhook URL is required")
	}
	if opts.BaseURL == "" {
		return nil, errors.New("public base URL is required for Slack book links")
	}
	webhookURL, err := parseAbsoluteURL(opts.WebhookURL, "Slack webhook URL", "https")
	if err != nil {
		return nil, err
	}
	baseURL, err := parseAbsoluteURL(opts.BaseURL, "public base URL", "http", "https")
	if err != nil {
		return nil, err
	}

	client := opts.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	return &Slack{
		webhookURL: webhookURL.String(),
		baseURL:    strings.TrimRight(baseURL.String(), "/"),
		client:     client,
	}, nil
}

func parseAbsoluteURL(raw, label string, allowedSchemes ...string) (*url.URL, error) {
	u, err := url.ParseRequestURI(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid %s: %w", label, err)
	}
	if u.Host == "" {
		return nil, fmt.Errorf("invalid %s: must include host", label)
	}
	for _, scheme := range allowedSchemes {
		if u.Scheme == scheme {
			return u, nil
		}
	}
	return nil, fmt.Errorf("invalid %s: must use %s", label, strings.Join(allowedSchemes, " or "))
}

// NotifyBookAdded sends a notification for a newly discovered Readest book.
func (s *Slack) NotifyBookAdded(ctx context.Context, book state.BookState, autoLinked bool) error {
	header := "New book added"
	if !autoLinked {
		header = "Action needed"
	}
	return s.post(ctx, s.bookPayload(
		header,
		book,
		[]string{s.linkedLine(book, autoLinked)},
	))
}

// NotifyBookCompleted sends a notification when a book is marked complete.
func (s *Slack) NotifyBookCompleted(ctx context.Context, book state.BookState) error {
	lines := []string{"Marked complete in Hardcover"}
	if hardcoverLine := s.hardcoverLine(book); hardcoverLine != "" {
		lines = append(lines, hardcoverLine)
	}
	return s.post(ctx, s.bookPayload(
		"Book marked complete",
		book,
		lines,
	))
}

// NotifyCriticalError sends a notification for a critical sync error.
func (s *Slack) NotifyCriticalError(ctx context.Context, err error) error {
	payload := slackPayload{
		Blocks: []map[string]any{
			{
				"type": "section",
				"text": mrkdwnText(fmt.Sprintf(":rotating_light: Critical sync error\n```%s```\n<%s|Open web UI>", truncate(err.Error(), 1800), s.baseURL+"/books")),
			},
		},
	}
	return s.post(ctx, payload)
}

type slackPayload struct {
	Text   string           `json:"text,omitempty"`
	Blocks []map[string]any `json:"blocks,omitempty"`
}

func (s *Slack) bookPayload(header string, book state.BookState, lines []string) slackPayload {
	body := s.bookMessageText(header, book, lines)
	if s.coverURL(book) == "" {
		body = ":question: " + body
	}

	blocks := []map[string]any{
		{
			"type": "section",
			"text": mrkdwnText(body),
		},
	}
	if coverURL := s.coverURL(book); coverURL != "" {
		blocks = append(blocks, map[string]any{
			"type":      "image",
			"image_url": coverURL,
			"alt_text":  "Cover for " + plainText(titleOrUntitled(book)),
		})
	}
	return slackPayload{Blocks: blocks}
}

func (s *Slack) bookMessageText(header string, book state.BookState, lines []string) string {
	parts := []string{
		fmt.Sprintf("%s: %s%s", escapeMrkdwn(header), slackLink(s.bookURL(book), titleOrUntitled(book)), authorSuffix(book.Author)),
	}
	parts = append(parts, lines...)
	return strings.Join(parts, "\n")
}

func (s *Slack) post(ctx context.Context, payload slackPayload) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal Slack payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.webhookURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create Slack request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		var urlErr *url.Error
		if errors.As(err, &urlErr) && urlErr.Err != nil {
			return fmt.Errorf("post Slack webhook: %w", urlErr.Err)
		}
		return fmt.Errorf("post Slack webhook: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		responseBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("Slack webhook returned %d: %s", resp.StatusCode, strings.TrimSpace(string(responseBody)))
	}
	return nil
}

func (s *Slack) bookURL(book state.BookState) string {
	u, err := url.Parse(s.baseURL)
	if err != nil {
		return s.baseURL + "/books"
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/books"
	u.RawQuery = url.Values{"book": {book.BookHash}}.Encode()
	return u.String()
}

func (s *Slack) coverURL(book state.BookState) string {
	if book.CoverURL == "" {
		return ""
	}
	u, err := url.Parse(book.CoverURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return ""
	}
	return book.CoverURL
}

func (s *Slack) hardcoverSlugLink(book state.BookState) string {
	if book.HardcoverSlug == "" {
		return ""
	}
	u := url.URL{
		Scheme: "https",
		Host:   "hardcover.app",
		Path:   "/books/" + book.HardcoverSlug,
	}
	return slackLink(u.String(), book.HardcoverSlug)
}

func (s *Slack) linkedLine(book state.BookState, autoLinked bool) string {
	if !autoLinked {
		return "Could not link automatically. Open the book and choose a Hardcover match."
	}
	method := book.MatchMethod
	if method == "" {
		method = "auto"
	}
	slugLink := s.hardcoverSlugLink(book)
	if method == "slug" && slugLink != "" {
		return "Linked automatically via Hardcover slug " + slugLink
	}
	if slugLink != "" {
		return fmt.Sprintf("Linked automatically via %s to %s", matchMethodLabel(method), slugLink)
	}
	return "Linked automatically via " + matchMethodLabel(method)
}

func (s *Slack) hardcoverLine(book state.BookState) string {
	if slugLink := s.hardcoverSlugLink(book); slugLink != "" {
		return "Hardcover slug " + slugLink
	}
	return ""
}

func matchMethodLabel(method string) string {
	switch method {
	case "isbn13":
		return "ISBN-13"
	case "isbn10":
		return "ISBN-10"
	case "title":
		return "title search"
	case "slug":
		return "Hardcover slug"
	case "":
		return "auto"
	default:
		return escapeMrkdwn(method)
	}
}

func authorSuffix(author string) string {
	if author == "" {
		return ""
	}
	return " by " + escapeMrkdwn(author)
}

func mrkdwnText(text string) map[string]any {
	return map[string]any{
		"type": "mrkdwn",
		"text": text,
	}
}

func titleOrUntitled(book state.BookState) string {
	if book.Title == "" {
		return "Untitled book"
	}
	return book.Title
}

func slackLink(link, label string) string {
	return fmt.Sprintf("<%s|%s>", link, escapeMrkdwn(label))
}

func escapeMrkdwn(s string) string {
	replacer := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		"|", "¦",
	)
	return replacer.Replace(s)
}

func plainText(s string) string {
	return strings.ReplaceAll(s, "\n", " ")
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	if max <= 3 {
		return s[:max]
	}
	return s[:max-3] + "..."
}
