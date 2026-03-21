package web

import (
	"context"
	"embed"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/claytono/readest-hardcover-sync/internal/state"
	syncsvc "github.com/claytono/readest-hardcover-sync/internal/sync"
)

//go:embed templates/*.html
var templateFS embed.FS

// NewServer creates an HTTP server with all web UI routes registered.
// The ctx is used as the base context for all requests, enabling clean shutdown of SSE connections.
func NewServer(ctx context.Context, st *state.State, finder syncsvc.BookFinder, updater syncsvc.ProgressUpdater, engine *syncsvc.Engine, events *syncsvc.EventBus, addr string, coversDir string, logger *slog.Logger) *http.Server {
	mux := http.NewServeMux()

	statusNames := make(map[int]string)
	statuses, err := updater.GetStatuses(context.Background())
	if err == nil {
		for _, s := range statuses {
			statusNames[s.ID] = s.Status
		}
	}

	h := &handlers{
		state:       st,
		finder:      finder,
		updater:     updater,
		engine:      engine,
		events:      events,
		ctx:         ctx,
		coversDir:   coversDir,
		statusNames: statusNames,
		logger:      logger,
	}
	h.loadTemplates()

	mux.HandleFunc("GET /", h.handleRoot)
	mux.HandleFunc("GET /books", h.handleBooks)
	mux.HandleFunc("GET /books/{hash}/link-modal", h.handleLinkModal)
	mux.HandleFunc("GET /books/{hash}/search", h.handleSearch)
	mux.HandleFunc("GET /books/{hash}/detail", h.handleBookDetail)
	mux.HandleFunc("POST /books/{hash}/link", h.handleLink)
	mux.HandleFunc("POST /books/{hash}/unlink", h.handleUnlink)
	mux.HandleFunc("GET /sidebar-status", h.handleSidebarStatus)
	mux.HandleFunc("POST /sync", h.handleTriggerSync)
	mux.HandleFunc("POST /full-sync", h.handleFullSync)
	mux.HandleFunc("GET /events", h.handleSSE)
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.Dir("static"))))
	if coversDir != "" {
		mux.Handle("GET /covers/", http.StripPrefix("/covers/", http.FileServer(http.Dir(coversDir))))
	}

	return &http.Server{
		Addr:         addr,
		Handler:      mux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 0, // disabled — SSE connections are long-lived
		IdleTimeout:  120 * time.Second,
		BaseContext: func(_ net.Listener) context.Context {
			return ctx
		},
	}
}
