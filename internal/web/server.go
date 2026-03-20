package web

import (
	"context"
	"embed"
	"log/slog"
	"net/http"

	"github.com/claytono/readest-hardcover-sync/internal/state"
	syncsvc "github.com/claytono/readest-hardcover-sync/internal/sync"
)

//go:embed templates/*.html
var templateFS embed.FS

// NewServer creates an HTTP server with all web UI routes registered.
func NewServer(st *state.State, finder syncsvc.BookFinder, updater syncsvc.ProgressUpdater, engine *syncsvc.Engine, addr string, logger *slog.Logger) *http.Server {
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
		statusNames: statusNames,
		logger:      logger,
	}
	h.loadTemplates()

	mux.HandleFunc("GET /", h.handleRoot)
	mux.HandleFunc("GET /books", h.handleBooks)
	mux.HandleFunc("GET /books/{hash}/link-modal", h.handleLinkModal)
	mux.HandleFunc("GET /books/{hash}/search", h.handleSearch)
	mux.HandleFunc("POST /books/{hash}/link", h.handleLink)
	mux.HandleFunc("POST /books/{hash}/unlink", h.handleUnlink)
	mux.HandleFunc("GET /status", h.handleStatus)
	mux.HandleFunc("POST /sync", h.handleTriggerSync)
	mux.HandleFunc("POST /full-sync", h.handleFullSync)
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.Dir("static"))))

	return &http.Server{Addr: addr, Handler: mux}
}
