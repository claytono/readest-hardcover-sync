package hardcover_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/time/rate"

	"github.com/claytono/readest-hardcover-sync/internal/hardcover"
)

// gqlResponse builds a JSON GraphQL response with the given data payload.
func gqlResponse(t *testing.T, data any) []byte {
	t.Helper()
	raw, err := json.Marshal(data)
	require.NoError(t, err)
	resp := map[string]any{"data": json.RawMessage(raw)}
	out, err := json.Marshal(resp)
	require.NoError(t, err)
	return out
}

func newTestClient(t *testing.T, handler http.HandlerFunc) *hardcover.Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	c := hardcover.NewClient("test-token")
	c.SetBaseURL(srv.URL)
	// Use a very permissive limiter so tests don't wait on the default limiter.
	c.SetLimiter(rate.NewLimiter(rate.Inf, 100))
	return c
}

func TestClient_FindBookBySlug(t *testing.T) {
	pages := 300
	handler := func(w http.ResponseWriter, r *http.Request) {
		// Verify authorization header.
		assert.Equal(t, "Bearer test-token", r.Header.Get("Authorization"))

		// Decode request body to verify variable.
		var body struct {
			Variables map[string]any `json:"variables"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		assert.Equal(t, "my-slug", body.Variables["slug"])

		data := map[string]any{
			"books": []map[string]any{
				{
					"id":    42,
					"title": "My Book",
					"slug":  "my-slug",
					"default_ebook_edition": map[string]any{
						"id":                42,
						"pages":             pages,
						"reading_format_id": hardcover.ReadingFormatEBook,
					},
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(gqlResponse(t, data))
	}

	c := newTestClient(t, handler)
	book, err := c.FindBookBySlug(context.Background(), "my-slug")
	require.NoError(t, err)
	require.NotNil(t, book)
	assert.Equal(t, 42, book.ID)
	assert.Equal(t, "my-slug", book.Slug)
	assert.Equal(t, "My Book", book.Title)
	require.NotNil(t, book.DefaultEbookEdition)
	assert.Equal(t, pages, *book.DefaultEbookEdition.Pages)
}

func TestClient_FindBookBySlug_NotFound(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		data := map[string]any{"books": []any{}}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(gqlResponse(t, data))
	}

	c := newTestClient(t, handler)
	book, err := c.FindBookBySlug(context.Background(), "nonexistent")
	require.NoError(t, err)
	assert.Nil(t, book)
}

func TestClient_FindEditionByISBN13(t *testing.T) {
	pages := 350
	rfID := hardcover.ReadingFormatEBook
	handler := func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Variables map[string]any `json:"variables"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		assert.Equal(t, "9781234567890", body.Variables["isbn"])

		data := map[string]any{
			"editions": []map[string]any{
				{
					"id":                10,
					"book_id":           42,
					"pages":             pages,
					"isbn_13":           "9781234567890",
					"reading_format_id": rfID,
					"book": map[string]any{
						"id":    42,
						"title": "Test Book",
						"slug":  "test-book",
						"default_ebook_edition": map[string]any{
							"id":                10,
							"pages":             pages,
							"reading_format_id": rfID,
						},
					},
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(gqlResponse(t, data))
	}

	c := newTestClient(t, handler)
	edition, err := c.FindEditionByISBN13(context.Background(), "9781234567890")
	require.NoError(t, err)
	require.NotNil(t, edition)
	assert.Equal(t, 10, edition.ID)
	assert.Equal(t, 42, edition.BookID)
	assert.Equal(t, "9781234567890", edition.ISBN13)
	require.NotNil(t, edition.Pages)
	assert.Equal(t, pages, *edition.Pages)
	require.NotNil(t, edition.Book)
	assert.Equal(t, "test-book", edition.Book.Slug)
}

func TestClient_RateLimiting(t *testing.T) {
	callCount := 0
	handler := func(w http.ResponseWriter, r *http.Request) {
		callCount++
		data := map[string]any{"books": []any{}}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(gqlResponse(t, data))
	}

	srv := httptest.NewServer(http.HandlerFunc(handler))
	t.Cleanup(srv.Close)

	c := hardcover.NewClient("test-token")
	c.SetBaseURL(srv.URL)
	// Allow one token at a time, refilling at 1 per 50ms.
	c.SetLimiter(rate.NewLimiter(rate.Every(50*time.Millisecond), 1))

	start := time.Now()
	for i := 0; i < 3; i++ {
		_, err := c.FindBookBySlug(context.Background(), "slug")
		require.NoError(t, err)
	}
	elapsed := time.Since(start)

	assert.Equal(t, 3, callCount)
	// With burst=1 and rate=1/50ms, 3 calls require at least 2 refill intervals (~100ms).
	assert.GreaterOrEqual(t, elapsed, 100*time.Millisecond,
		"expected rate limiting to slow calls; got %v", elapsed)
}

func TestClient_GraphQLError(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"errors": []map[string]any{
				{"message": "bad"},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		out, _ := json.Marshal(resp)
		_, _ = w.Write(out)
	}

	c := newTestClient(t, handler)
	book, err := c.FindBookBySlug(context.Background(), "anything")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "bad")
	assert.Nil(t, book)
}

func TestClient_InsertUserBook(t *testing.T) {
	var capturedBody map[string]any
	handler := func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, json.NewDecoder(r.Body).Decode(&capturedBody))

		edID := 99
		data := map[string]any{
			"insert_user_book": map[string]any{
				"error": nil,
				"user_book": map[string]any{
					"id":         1,
					"book_id":    42,
					"status_id":  2,
					"edition_id": edID,
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(gqlResponse(t, data))
	}

	c := newTestClient(t, handler)
	editionID := 99
	ub, err := c.InsertUserBook(context.Background(), 42, 2, 1, &editionID)
	require.NoError(t, err)
	require.NotNil(t, ub)
	assert.Equal(t, 42, ub.BookID)
	assert.Equal(t, 2, ub.StatusID)

	// Verify privacy_setting_id was included in the mutation variables.
	vars, ok := capturedBody["variables"].(map[string]any)
	require.True(t, ok, "expected variables in request body")
	obj, ok := vars["object"].(map[string]any)
	require.True(t, ok, "expected object in variables")
	assert.Equal(t, float64(1), obj["privacy_setting_id"], "expected privacy_setting_id to be sent")
}
