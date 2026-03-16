package hardcover

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"golang.org/x/time/rate"
)

const defaultBaseURL = "https://api.hardcover.app/v1/graphql"

// Client is an authenticated GraphQL client for the Hardcover API.
type Client struct {
	token   string
	baseURL string
	http    *http.Client
	limiter *rate.Limiter
	userID  int // cached from GetMe, used to scope user_books queries
}

// NewClient constructs a Client with the given API token and default rate limiter (~50 req/min).
// The token may optionally include a "Bearer " prefix, which will be stripped.
func NewClient(token string) *Client {
	token = strings.TrimSpace(token)
	token = strings.TrimPrefix(token, "Bearer ")
	return &Client{
		token:   token,
		baseURL: defaultBaseURL,
		http:    &http.Client{Timeout: 30 * time.Second},
		limiter: rate.NewLimiter(rate.Limit(0.83), 5),
	}
}

// SetBaseURL overrides the API base URL (for testing).
func (c *Client) SetBaseURL(url string) {
	c.baseURL = url
}

// SetLimiter replaces the rate limiter (for testing).
func (c *Client) SetLimiter(l *rate.Limiter) {
	c.limiter = l
}

// graphqlRequest is the JSON body sent to the GraphQL endpoint.
type graphqlRequest struct {
	Query     string         `json:"query"`
	Variables map[string]any `json:"variables,omitempty"`
}

// graphqlResponse is the top-level GraphQL response envelope.
type graphqlResponse struct {
	Data   json.RawMessage `json:"data"`
	Errors []graphqlError  `json:"errors,omitempty"`
}

type graphqlError struct {
	Message string `json:"message"`
}

// do executes a GraphQL request and unmarshals the data field into result.
func (c *Client) do(ctx context.Context, query string, vars map[string]any, result any) error {
	if err := c.limiter.Wait(ctx); err != nil {
		return fmt.Errorf("rate limiter: %w", err)
	}

	body, err := json.Marshal(graphqlRequest{Query: query, Variables: vars})
	if err != nil {
		return fmt.Errorf("marshaling request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.token)

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("executing request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d from Hardcover API", resp.StatusCode)
	}

	var gqlResp graphqlResponse
	if err := json.NewDecoder(resp.Body).Decode(&gqlResp); err != nil {
		return fmt.Errorf("decoding response: %w", err)
	}

	if len(gqlResp.Errors) > 0 {
		return fmt.Errorf("GraphQL error: %s", gqlResp.Errors[0].Message)
	}

	if result != nil && gqlResp.Data != nil {
		if err := json.Unmarshal(gqlResp.Data, result); err != nil {
			return fmt.Errorf("unmarshaling data: %w", err)
		}
	}

	return nil
}

// GetMe returns the authenticated user's basic profile.
func (c *Client) GetMe(ctx context.Context) (*MeResponse, error) {
	var data struct {
		Me []MeResponse `json:"me"`
	}
	if err := c.do(ctx, queryMe, nil, &data); err != nil {
		return nil, err
	}
	if len(data.Me) == 0 {
		return nil, nil
	}
	c.userID = data.Me[0].ID
	return &data.Me[0], nil
}

// GetStatuses fetches the list of user book status options from Hardcover.
func (c *Client) GetStatuses(ctx context.Context) ([]BookStatus, error) {
	var data struct {
		Statuses []BookStatus `json:"user_book_statuses"`
	}
	if err := c.do(ctx, queryStatuses, nil, &data); err != nil {
		return nil, err
	}
	return data.Statuses, nil
}

// FindBookBySlug looks up a book by its Hardcover slug. Returns nil, nil if not found.
func (c *Client) FindBookBySlug(ctx context.Context, slug string) (*Book, error) {
	var data struct {
		Books []Book `json:"books"`
	}
	vars := map[string]any{"slug": slug}
	if err := c.do(ctx, queryBookBySlug, vars, &data); err != nil {
		return nil, err
	}
	if len(data.Books) == 0 {
		return nil, nil
	}
	return &data.Books[0], nil
}

// FindEditionByISBN13 looks up an edition by ISBN-13. Returns nil, nil if not found.
func (c *Client) FindEditionByISBN13(ctx context.Context, isbn string) (*Edition, error) {
	var data struct {
		Editions []Edition `json:"editions"`
	}
	vars := map[string]any{"isbn": isbn}
	if err := c.do(ctx, queryEditionByISBN13, vars, &data); err != nil {
		return nil, err
	}
	if len(data.Editions) == 0 {
		return nil, nil
	}
	return &data.Editions[0], nil
}

// FindEditionByISBN10 looks up an edition by ISBN-10. Returns nil, nil if not found.
func (c *Client) FindEditionByISBN10(ctx context.Context, isbn string) (*Edition, error) {
	var data struct {
		Editions []Edition `json:"editions"`
	}
	vars := map[string]any{"isbn": isbn}
	if err := c.do(ctx, queryEditionByISBN10, vars, &data); err != nil {
		return nil, err
	}
	if len(data.Editions) == 0 {
		return nil, nil
	}
	return &data.Editions[0], nil
}

// SearchBooks searches for books by query string, then hydrates the results by ID.
func (c *Client) SearchBooks(ctx context.Context, query string) ([]Book, error) {
	var searchData struct {
		Search SearchResult `json:"search"`
	}
	vars := map[string]any{"query": query}
	if err := c.do(ctx, querySearchBooks, vars, &searchData); err != nil {
		return nil, err
	}

	if len(searchData.Search.IDs) == 0 {
		return nil, nil
	}

	var booksData struct {
		Books []Book `json:"books"`
	}
	ids := make([]int, 0, len(searchData.Search.IDs))
	for _, s := range searchData.Search.IDs {
		id, err := strconv.Atoi(s)
		if err != nil {
			continue
		}
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return nil, nil
	}
	if err := c.do(ctx, queryBooksByIDs, map[string]any{"ids": ids}, &booksData); err != nil {
		return nil, err
	}
	return booksData.Books, nil
}

// GetUserBook retrieves the user's reading record for a given book ID. Returns nil, nil if not found.
func (c *Client) GetUserBook(ctx context.Context, bookID int) (*UserBook, error) {
	var data struct {
		UserBooks []UserBook `json:"user_books"`
	}
	vars := map[string]any{"bookId": bookID, "userId": c.userID}
	if err := c.do(ctx, queryUserBook, vars, &data); err != nil {
		return nil, err
	}
	if len(data.UserBooks) == 0 {
		return nil, nil
	}
	return &data.UserBooks[0], nil
}

// InsertUserBook creates a new user_book record.
func (c *Client) InsertUserBook(ctx context.Context, bookID, statusID, privacySettingID int, editionID *int) (*UserBook, error) {
	object := map[string]any{
		"book_id":            bookID,
		"status_id":          statusID,
		"privacy_setting_id": privacySettingID,
	}
	if editionID != nil {
		object["edition_id"] = *editionID
	}

	var data struct {
		InsertUserBook struct {
			Error    *string  `json:"error"`
			UserBook UserBook `json:"user_book"`
		} `json:"insert_user_book"`
	}
	vars := map[string]any{"object": object}
	if err := c.do(ctx, mutationInsertUserBook, vars, &data); err != nil {
		return nil, err
	}
	if data.InsertUserBook.Error != nil && *data.InsertUserBook.Error != "" {
		return nil, fmt.Errorf("insert_user_book: %s", *data.InsertUserBook.Error)
	}
	return &data.InsertUserBook.UserBook, nil
}

// UpdateUserBook updates the status of an existing user_book record.
func (c *Client) UpdateUserBook(ctx context.Context, id, statusID int) (*UserBook, error) {
	object := map[string]any{
		"status_id": statusID,
	}

	var data struct {
		UpdateUserBook struct {
			Error    *string  `json:"error"`
			UserBook UserBook `json:"user_book"`
		} `json:"update_user_book"`
	}
	vars := map[string]any{"id": id, "object": object}
	if err := c.do(ctx, mutationUpdateUserBook, vars, &data); err != nil {
		return nil, err
	}
	if data.UpdateUserBook.Error != nil && *data.UpdateUserBook.Error != "" {
		return nil, fmt.Errorf("update_user_book: %s", *data.UpdateUserBook.Error)
	}
	return &data.UpdateUserBook.UserBook, nil
}

// InsertUserBookRead creates a new reading session for an existing user_book.
func (c *Client) InsertUserBookRead(ctx context.Context, userBookID, progressPages int, editionID *int, startedAt string, finishedAt *string) (*UserBookRead, error) {
	read := map[string]any{
		"progress_pages": progressPages,
		"started_at":     startedAt,
	}
	if editionID != nil {
		read["edition_id"] = *editionID
	}
	if finishedAt != nil {
		read["finished_at"] = *finishedAt
	}

	var data struct {
		InsertUserBookRead struct {
			Error        *string      `json:"error"`
			UserBookRead UserBookRead `json:"user_book_read"`
		} `json:"insert_user_book_read"`
	}
	vars := map[string]any{"id": userBookID, "read": read}
	if err := c.do(ctx, mutationInsertUserBookRead, vars, &data); err != nil {
		return nil, err
	}
	if data.InsertUserBookRead.Error != nil && *data.InsertUserBookRead.Error != "" {
		return nil, fmt.Errorf("insert_user_book_read: %s", *data.InsertUserBookRead.Error)
	}
	return &data.InsertUserBookRead.UserBookRead, nil
}

// UpdateUserBookRead updates progress/finish date on an existing reading session.
func (c *Client) UpdateUserBookRead(ctx context.Context, id, progressPages int, finishedAt *string) (*UserBookRead, error) {
	object := map[string]any{
		"progress_pages": progressPages,
	}
	if finishedAt != nil {
		object["finished_at"] = *finishedAt
	}

	var data struct {
		UpdateUserBookRead struct {
			Error        *string      `json:"error"`
			UserBookRead UserBookRead `json:"user_book_read"`
		} `json:"update_user_book_read"`
	}
	vars := map[string]any{"id": id, "object": object}
	if err := c.do(ctx, mutationUpdateUserBookRead, vars, &data); err != nil {
		return nil, err
	}
	if data.UpdateUserBookRead.Error != nil && *data.UpdateUserBookRead.Error != "" {
		return nil, fmt.Errorf("update_user_book_read: %s", *data.UpdateUserBookRead.Error)
	}
	return &data.UpdateUserBookRead.UserBookRead, nil
}
