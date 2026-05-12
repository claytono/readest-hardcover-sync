package hardcover_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
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

func doLiveGraphQL(t *testing.T, token string, query string, vars map[string]any, result any) {
	t.Helper()

	body, err := json.Marshal(map[string]any{
		"query":     query,
		"variables": vars,
	})
	require.NoError(t, err)

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, "https://api.hardcover.app/v1/graphql", bytes.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	token = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(token), "Bearer "))
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var gqlResp struct {
		Data   json.RawMessage `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
		Error string `json:"error"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&gqlResp))
	require.Empty(t, gqlResp.Error)
	if len(gqlResp.Errors) > 0 {
		require.Fail(t, fmt.Sprintf("GraphQL error: %s", gqlResp.Errors[0].Message))
	}
	require.NoError(t, json.Unmarshal(gqlResp.Data, result))
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
	// Use a generous lower bound (80ms) to avoid flakiness on slow CI runners.
	assert.GreaterOrEqual(t, elapsed, 80*time.Millisecond,
		"expected rate limiting to slow calls; got %v", elapsed)
	assert.Less(t, elapsed, 5*time.Second,
		"rate-limited calls took too long; got %v", elapsed)
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

func TestClient_FindEditionByISBN13_NotFound(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		data := map[string]any{"editions": []any{}}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(gqlResponse(t, data))
	}

	c := newTestClient(t, handler)
	edition, err := c.FindEditionByISBN13(context.Background(), "0000000000000")
	require.NoError(t, err)
	assert.Nil(t, edition)
}

func TestClient_HTTPNon200(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}

	c := newTestClient(t, handler)
	book, err := c.FindBookBySlug(context.Background(), "anything")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "HTTP 500")
	assert.Nil(t, book)
}

func TestClient_GetMe(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		data := map[string]any{
			"me": []map[string]any{
				{
					"id":                         7,
					"account_privacy_setting_id": 1,
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(gqlResponse(t, data))
	}

	c := newTestClient(t, handler)
	me, err := c.GetMe(context.Background())
	require.NoError(t, err)
	require.NotNil(t, me)
	assert.Equal(t, 7, me.ID)
	assert.Equal(t, 1, me.AccountPrivacySettingID)
}

func TestClient_InvalidBaseURL(t *testing.T) {
	c := hardcover.NewClient("test-token")
	c.SetBaseURL("http://127.0.0.1:0") // nothing listening here
	c.SetLimiter(rate.NewLimiter(rate.Inf, 100))
	book, err := c.FindBookBySlug(context.Background(), "slug")
	require.Error(t, err)
	assert.Nil(t, book)
}

func TestClient_InvalidJSONResponse(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("not valid json at all"))
	}

	c := newTestClient(t, handler)
	book, err := c.FindBookBySlug(context.Background(), "slug")
	require.Error(t, err)
	assert.Nil(t, book)
}

func TestClient_GetMe_Error(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}

	c := newTestClient(t, handler)
	me, err := c.GetMe(context.Background())
	require.Error(t, err)
	assert.Nil(t, me)
}

func TestClient_GetMe_Empty(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		data := map[string]any{"me": []any{}}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(gqlResponse(t, data))
	}

	c := newTestClient(t, handler)
	me, err := c.GetMe(context.Background())
	require.NoError(t, err)
	assert.Nil(t, me)
}

func TestClient_GetStatuses(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		data := map[string]any{
			"user_book_statuses": []map[string]any{
				{"id": 1, "status": "want to read"},
				{"id": 2, "status": "currently reading"},
				{"id": 3, "status": "read"},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(gqlResponse(t, data))
	}

	c := newTestClient(t, handler)
	statuses, err := c.GetStatuses(context.Background())
	require.NoError(t, err)
	require.Len(t, statuses, 3)
	assert.Equal(t, 1, statuses[0].ID)
	assert.Equal(t, "want to read", statuses[0].Status)
	assert.Equal(t, 3, statuses[2].ID)
}

func TestClient_GetStatuses_Error(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}

	c := newTestClient(t, handler)
	statuses, err := c.GetStatuses(context.Background())
	require.Error(t, err)
	assert.Nil(t, statuses)
}

func TestClient_FindEditionByISBN10(t *testing.T) {
	pages := 200
	rfID := hardcover.ReadingFormatEBook
	handler := func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Variables map[string]any `json:"variables"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		assert.Equal(t, "0306406152", body.Variables["isbn"])

		data := map[string]any{
			"editions": []map[string]any{
				{
					"id":                5,
					"book_id":           10,
					"pages":             pages,
					"isbn_10":           "0306406152",
					"reading_format_id": rfID,
					"book": map[string]any{
						"id":    10,
						"title": "ISBN10 Book",
						"slug":  "isbn10-book",
					},
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(gqlResponse(t, data))
	}

	c := newTestClient(t, handler)
	edition, err := c.FindEditionByISBN10(context.Background(), "0306406152")
	require.NoError(t, err)
	require.NotNil(t, edition)
	assert.Equal(t, 5, edition.ID)
	assert.Equal(t, 10, edition.BookID)
	assert.Equal(t, "0306406152", edition.ISBN10)
	require.NotNil(t, edition.Pages)
	assert.Equal(t, pages, *edition.Pages)
}

func TestClient_FindEditionByISBN10_NotFound(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		data := map[string]any{"editions": []any{}}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(gqlResponse(t, data))
	}

	c := newTestClient(t, handler)
	edition, err := c.FindEditionByISBN10(context.Background(), "0000000000")
	require.NoError(t, err)
	assert.Nil(t, edition)
}

func TestClient_SearchBooks_Empty(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		data := map[string]any{
			"search": map[string]any{
				"ids": []int{},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(gqlResponse(t, data))
	}

	c := newTestClient(t, handler)
	books, err := c.SearchBooks(context.Background(), "nonexistent book")
	require.NoError(t, err)
	assert.Nil(t, books)
}

func TestClient_SearchBooks_NumericIDs(t *testing.T) {
	callCount := 0
	handler := func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "application/json")

		var body struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))

		if callCount == 1 {
			// First call: search query returns IDs.
			data := map[string]any{
				"search": map[string]any{
					"ids": []int{101, 102},
				},
			}
			_, _ = w.Write(gqlResponse(t, data))
		} else {
			// Second call: hydrate books by IDs.
			data := map[string]any{
				"books": []map[string]any{
					{"id": 101, "title": "Book One", "slug": "book-one"},
					{"id": 102, "title": "Book Two", "slug": "book-two"},
				},
			}
			_, _ = w.Write(gqlResponse(t, data))
		}
	}

	c := newTestClient(t, handler)
	books, err := c.SearchBooks(context.Background(), "book")
	require.NoError(t, err)
	require.Len(t, books, 2)
	assert.Equal(t, 2, callCount)
	assert.Equal(t, 101, books[0].ID)
	assert.Equal(t, "book-one", books[0].Slug)
	assert.Equal(t, 102, books[1].ID)
}

func TestClient_SearchBooks_LiveContract(t *testing.T) {
	token := os.Getenv("HARDCOVER_TOKEN")
	if token == "" {
		t.Skip("HARDCOVER_TOKEN is not set; skipping live Hardcover contract test")
	}

	var bookData struct {
		Books []struct {
			Title string `json:"title"`
		} `json:"books"`
	}
	doLiveGraphQL(t, token, `query LiveContractBook {
  books(limit: 1, where: { title: { _is_null: false } }, order_by: { id: asc }) { title }
}`, nil, &bookData)
	require.NotEmpty(t, bookData.Books)
	require.NotEmpty(t, bookData.Books[0].Title)

	var searchData struct {
		Search hardcover.SearchResult `json:"search"`
	}
	doLiveGraphQL(t, token, `query SearchBooks($query: String!) {
  search(query: $query, query_type: "Book", per_page: 5, page: 1) { ids }
}`, map[string]any{"query": bookData.Books[0].Title}, &searchData)
	require.NotEmpty(t, searchData.Search.IDs)
	assert.NotZero(t, searchData.Search.IDs[0])
}

func TestClient_GetUserBook(t *testing.T) {
	edID := 55
	handler := func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Variables map[string]any `json:"variables"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		assert.Equal(t, float64(42), body.Variables["bookId"])

		data := map[string]any{
			"user_books": []map[string]any{
				{
					"id":         10,
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
	ub, err := c.GetUserBook(context.Background(), 42)
	require.NoError(t, err)
	require.NotNil(t, ub)
	assert.Equal(t, 10, ub.ID)
	assert.Equal(t, 42, ub.BookID)
	assert.Equal(t, 2, ub.StatusID)
}

func TestClient_GetUserBook_NotFound(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		data := map[string]any{"user_books": []any{}}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(gqlResponse(t, data))
	}

	c := newTestClient(t, handler)
	ub, err := c.GetUserBook(context.Background(), 99)
	require.NoError(t, err)
	assert.Nil(t, ub)
}

func TestClient_InsertUserBook_Error(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		errMsg := "book already exists"
		data := map[string]any{
			"insert_user_book": map[string]any{
				"error":     errMsg,
				"user_book": map[string]any{},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(gqlResponse(t, data))
	}

	c := newTestClient(t, handler)
	ub, err := c.InsertUserBook(context.Background(), 42, 2, 1, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "book already exists")
	assert.Nil(t, ub)
}

func TestClient_UpdateUserBook(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Variables map[string]any `json:"variables"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		assert.Equal(t, float64(10), body.Variables["id"])
		object := body.Variables["object"].(map[string]any)
		assert.Equal(t, float64(3), object["status_id"])
		assert.Equal(t, float64(77), object["edition_id"])

		data := map[string]any{
			"update_user_book": map[string]any{
				"error": nil,
				"user_book": map[string]any{
					"id":         10,
					"book_id":    42,
					"status_id":  3,
					"edition_id": 77,
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(gqlResponse(t, data))
	}

	c := newTestClient(t, handler)
	editionID := 77
	ub, err := c.UpdateUserBook(context.Background(), 10, 3, &editionID)
	require.NoError(t, err)
	require.NotNil(t, ub)
	assert.Equal(t, 10, ub.ID)
	assert.Equal(t, 3, ub.StatusID)
	require.NotNil(t, ub.EditionID)
	assert.Equal(t, editionID, *ub.EditionID)
}

func TestClient_UpdateUserBook_Error(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		errMsg := "record not found"
		data := map[string]any{
			"update_user_book": map[string]any{
				"error":     errMsg,
				"user_book": map[string]any{},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(gqlResponse(t, data))
	}

	c := newTestClient(t, handler)
	ub, err := c.UpdateUserBook(context.Background(), 999, 3, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "record not found")
	assert.Nil(t, ub)
}

func TestClient_InsertUserBookRead(t *testing.T) {
	pages := 150
	handler := func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Variables map[string]any `json:"variables"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		assert.Equal(t, float64(10), body.Variables["id"])

		data := map[string]any{
			"insert_user_book_read": map[string]any{
				"error": nil,
				"user_book_read": map[string]any{
					"id":             20,
					"progress_pages": pages,
					"started_at":     "2024-01-01",
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(gqlResponse(t, data))
	}

	c := newTestClient(t, handler)
	editionID := 55
	ubr, err := c.InsertUserBookRead(context.Background(), 10, pages, &editionID, "2024-01-01", nil)
	require.NoError(t, err)
	require.NotNil(t, ubr)
	assert.Equal(t, 20, ubr.ID)
	require.NotNil(t, ubr.ProgressPages)
	assert.Equal(t, pages, *ubr.ProgressPages)
}

func TestClient_InsertUserBookRead_WithFinishedAt(t *testing.T) {
	pages := 300
	handler := func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Variables map[string]any `json:"variables"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		read, ok := body.Variables["read"].(map[string]any)
		require.True(t, ok)
		assert.Equal(t, "2024-06-01", read["finished_at"])

		data := map[string]any{
			"insert_user_book_read": map[string]any{
				"error": nil,
				"user_book_read": map[string]any{
					"id":             21,
					"progress_pages": pages,
					"started_at":     "2024-01-01",
					"finished_at":    "2024-06-01",
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(gqlResponse(t, data))
	}

	c := newTestClient(t, handler)
	finishedAt := "2024-06-01"
	ubr, err := c.InsertUserBookRead(context.Background(), 10, pages, nil, "2024-01-01", &finishedAt)
	require.NoError(t, err)
	require.NotNil(t, ubr)
	assert.Equal(t, "2024-06-01", ubr.FinishedAt)
}

func TestClient_InsertUserBookRead_Error(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		errMsg := "user_book not found"
		data := map[string]any{
			"insert_user_book_read": map[string]any{
				"error":          errMsg,
				"user_book_read": map[string]any{},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(gqlResponse(t, data))
	}

	c := newTestClient(t, handler)
	ubr, err := c.InsertUserBookRead(context.Background(), 999, 0, nil, "2024-01-01", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "user_book not found")
	assert.Nil(t, ubr)
}

func TestClient_UpdateUserBookRead(t *testing.T) {
	pages := 200
	handler := func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Variables map[string]any `json:"variables"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		assert.Equal(t, float64(20), body.Variables["id"])

		data := map[string]any{
			"update_user_book_read": map[string]any{
				"error": nil,
				"user_book_read": map[string]any{
					"id":             20,
					"progress_pages": pages,
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(gqlResponse(t, data))
	}

	c := newTestClient(t, handler)
	ubr, err := c.UpdateUserBookRead(context.Background(), 20, pages, nil)
	require.NoError(t, err)
	require.NotNil(t, ubr)
	assert.Equal(t, 20, ubr.ID)
	require.NotNil(t, ubr.ProgressPages)
	assert.Equal(t, pages, *ubr.ProgressPages)
}

func TestClient_UpdateUserBookRead_WithFinishedAt(t *testing.T) {
	pages := 350
	handler := func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Variables map[string]any `json:"variables"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		obj, ok := body.Variables["object"].(map[string]any)
		require.True(t, ok)
		assert.Equal(t, "2024-12-31", obj["finished_at"])

		data := map[string]any{
			"update_user_book_read": map[string]any{
				"error": nil,
				"user_book_read": map[string]any{
					"id":             20,
					"progress_pages": pages,
					"finished_at":    "2024-12-31",
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(gqlResponse(t, data))
	}

	c := newTestClient(t, handler)
	finishedAt := "2024-12-31"
	ubr, err := c.UpdateUserBookRead(context.Background(), 20, pages, &finishedAt)
	require.NoError(t, err)
	require.NotNil(t, ubr)
	assert.Equal(t, "2024-12-31", ubr.FinishedAt)
}

func TestClient_UpdateUserBookRead_Error(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		errMsg := "read session not found"
		data := map[string]any{
			"update_user_book_read": map[string]any{
				"error":          errMsg,
				"user_book_read": map[string]any{},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(gqlResponse(t, data))
	}

	c := newTestClient(t, handler)
	ubr, err := c.UpdateUserBookRead(context.Background(), 999, 100, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "read session not found")
	assert.Nil(t, ubr)
}

func TestClient_CancelledContext(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("should not reach handler")
	}

	c := newTestClient(t, handler)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	_, err := c.FindBookBySlug(ctx, "slug")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "rate limiter")
}

func TestClient_UnmarshalDataError(t *testing.T) {
	// Return valid GraphQL response but with data that doesn't match the expected struct.
	handler := func(w http.ResponseWriter, r *http.Request) {
		// books should be an array, but we return a string to cause unmarshal error.
		raw := `{"data":{"books":"not-an-array"}}`
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(raw))
	}

	c := newTestClient(t, handler)
	_, err := c.FindBookBySlug(context.Background(), "slug")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unmarshaling data")
}

func TestClient_FindEditionByISBN13_Error(t *testing.T) {
	c := hardcover.NewClient("test-token")
	c.SetBaseURL("http://127.0.0.1:0")
	c.SetLimiter(rate.NewLimiter(rate.Inf, 100))
	_, err := c.FindEditionByISBN13(context.Background(), "9781234567890")
	require.Error(t, err)
}

func TestClient_FindEditionByISBN10_Error(t *testing.T) {
	c := hardcover.NewClient("test-token")
	c.SetBaseURL("http://127.0.0.1:0")
	c.SetLimiter(rate.NewLimiter(rate.Inf, 100))
	_, err := c.FindEditionByISBN10(context.Background(), "0306406152")
	require.Error(t, err)
}

func TestClient_SearchBooks_Error(t *testing.T) {
	c := hardcover.NewClient("test-token")
	c.SetBaseURL("http://127.0.0.1:0")
	c.SetLimiter(rate.NewLimiter(rate.Inf, 100))
	_, err := c.SearchBooks(context.Background(), "query")
	require.Error(t, err)
}

func TestClient_GetUserBook_Error(t *testing.T) {
	c := hardcover.NewClient("test-token")
	c.SetBaseURL("http://127.0.0.1:0")
	c.SetLimiter(rate.NewLimiter(rate.Inf, 100))
	_, err := c.GetUserBook(context.Background(), 42)
	require.Error(t, err)
}

func TestClient_UpdateUserBook_NetworkError(t *testing.T) {
	c := hardcover.NewClient("test-token")
	c.SetBaseURL("http://127.0.0.1:0")
	c.SetLimiter(rate.NewLimiter(rate.Inf, 100))
	_, err := c.UpdateUserBook(context.Background(), 10, 3, nil)
	require.Error(t, err)
}

func TestClient_InsertUserBookRead_NetworkError(t *testing.T) {
	c := hardcover.NewClient("test-token")
	c.SetBaseURL("http://127.0.0.1:0")
	c.SetLimiter(rate.NewLimiter(rate.Inf, 100))
	_, err := c.InsertUserBookRead(context.Background(), 10, 100, nil, "2024-01-01", nil)
	require.Error(t, err)
}

func TestClient_UpdateUserBookRead_NetworkError(t *testing.T) {
	c := hardcover.NewClient("test-token")
	c.SetBaseURL("http://127.0.0.1:0")
	c.SetLimiter(rate.NewLimiter(rate.Inf, 100))
	_, err := c.UpdateUserBookRead(context.Background(), 20, 100, nil)
	require.Error(t, err)
}

func TestClient_SearchBooks_HydrationError(t *testing.T) {
	callCount := 0
	handler := func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "application/json")

		if callCount == 1 {
			// Search returns valid IDs.
			data := map[string]any{
				"search": map[string]any{"ids": []int{101}},
			}
			_, _ = w.Write(gqlResponse(t, data))
		} else {
			// Hydration call returns HTTP error.
			w.WriteHeader(http.StatusInternalServerError)
		}
	}

	c := newTestClient(t, handler)
	_, err := c.SearchBooks(context.Background(), "book")
	require.Error(t, err)
}

func TestClient_InsertUserBook_NetworkError(t *testing.T) {
	c := hardcover.NewClient("test-token")
	c.SetBaseURL("http://127.0.0.1:0")
	c.SetLimiter(rate.NewLimiter(rate.Inf, 100))
	_, err := c.InsertUserBook(context.Background(), 42, 2, 1, nil)
	require.Error(t, err)
}
