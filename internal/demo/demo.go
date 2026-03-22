package demo

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/claytono/readest-hardcover-sync/internal/state"
	syncsvc "github.com/claytono/readest-hardcover-sync/internal/sync"
	"github.com/claytono/readest-hardcover-sync/internal/web"
)

// StartServer starts a demo web server with synthetic data. It returns the
// HTTP server, the base URL (e.g. "http://localhost:12345"), and any error.
// The caller is responsible for shutting down the server.
func StartServer(ctx context.Context, logger *slog.Logger, listenAddr string, coversDir string) (*http.Server, string, error) {
	st := state.New("")
	if err := st.Load(); err != nil {
		return nil, "", fmt.Errorf("loading state: %w", err)
	}

	// Populate state with demo books.
	for _, db := range demoBooks() {
		bs := db.state
		bs.Metadata = db.metadata
		st.SetBook(bs.BookHash, bs)
	}

	events := syncsvc.NewEventBus(200)

	// Publish demo sync events to simulate a completed sync cycle.
	events.Publish(syncsvc.SyncEvent{Type: "sync_start", Detail: "Syncing 10 books"})
	events.Publish(syncsvc.SyncEvent{Type: "book_matched", Title: "Pride and Prejudice", Detail: "matched via slug → pride-and-prejudice"})
	events.Publish(syncsvc.SyncEvent{Type: "book_matched", Title: "Moby Dick", Detail: "matched via ISBN-13"})
	events.Publish(syncsvc.SyncEvent{Type: "book_matched", Title: "A Study in Scarlet", Detail: "matched via slug → a-study-in-scarlet"})
	events.Publish(syncsvc.SyncEvent{Type: "progress_synced", Title: "Moby Dick", Detail: "67% → Hardcover"})
	events.Publish(syncsvc.SyncEvent{Type: "progress_synced", Title: "Crime and Punishment", Detail: "40% → Hardcover"})
	events.Publish(syncsvc.SyncEvent{Type: "book_unmatched", Title: "The War of the Worlds", Detail: "no identifiers found"})
	events.Publish(syncsvc.SyncEvent{Type: "book_unmatched", Title: "The Adventures of Sherlock Holmes", Detail: "no identifiers found"})
	events.Publish(syncsvc.SyncEvent{Type: "sync_complete", Detail: "8 matched, 2 unmatched"})

	st.SetLastSyncRanAt(time.Now())

	finder := newDemoFinder()
	updater := &demoUpdater{}

	server := web.NewServer(ctx, st, finder, updater, nil, events, listenAddr, coversDir, logger)

	listener, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return nil, "", fmt.Errorf("binding %s: %w", listenAddr, err)
	}

	baseURL := fmt.Sprintf("http://localhost:%d", listener.Addr().(*net.TCPAddr).Port)

	go func() {
		if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
			logger.Error("http server error", "error", err)
		}
	}()

	return server, baseURL, nil
}
