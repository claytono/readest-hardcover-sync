package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/claytono/readest-hardcover-sync/internal/config"
	"github.com/claytono/readest-hardcover-sync/internal/hardcover"
	"github.com/claytono/readest-hardcover-sync/internal/identifier"
	"github.com/claytono/readest-hardcover-sync/internal/readest"
	"github.com/claytono/readest-hardcover-sync/internal/state"
	syncsvc "github.com/claytono/readest-hardcover-sync/internal/sync"
	"github.com/claytono/readest-hardcover-sync/internal/web"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))

	if len(os.Args) < 2 {
		runServer(logger)
		return
	}

	switch os.Args[1] {
	case "check-readest-auth":
		runCheckReadestAuth(logger)
	case "check-hardcover-auth":
		runCheckHardcoverAuth(logger)
	case "list-books":
		runListBooks(logger)
	case "lookup":
		runLookup(logger)
	case "dry-run":
		runDryRun(logger)
	default:
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Fprintf(os.Stderr, "Usage: %s [subcommand]\n\n", filepath.Base(os.Args[0]))
	fmt.Fprintln(os.Stderr, "Subcommands:")
	fmt.Fprintln(os.Stderr, "  (none)               Start the sync server")
	fmt.Fprintln(os.Stderr, "  check-readest-auth   Verify Readest credentials")
	fmt.Fprintln(os.Stderr, "  check-hardcover-auth Verify Hardcover API token")
	fmt.Fprintln(os.Stderr, "  list-books           List all books from Readest")
	fmt.Fprintln(os.Stderr, "  lookup <slug|isbn>   Look up a book on Hardcover")
	fmt.Fprintln(os.Stderr, "  dry-run              Run a sync cycle without writing to Hardcover")
}

// loadConfig loads the config and exits on error.
func loadConfig(logger *slog.Logger) *config.Config {
	cfg, err := config.Load()
	if err != nil {
		logger.Error("failed to load config", "error", err)
		os.Exit(1)
	}
	return cfg
}

// newReadestClient creates the Readest auth and client, exiting on error.
func newReadestClient(cfg *config.Config, logger *slog.Logger) *readest.Client {
	auth, err := readest.NewAuth(cfg.ReadestEmail, cfg.ReadestPassword)
	if err != nil {
		logger.Error("failed to create readest auth", "error", err)
		os.Exit(1)
	}
	return readest.NewClient(auth)
}

// newHardcoverClient creates a Hardcover client.
func newHardcoverClient(cfg *config.Config) *hardcover.Client {
	return hardcover.NewClient(cfg.HardcoverToken)
}

// runServer is the default mode: start the HTTP server and sync engine.
func runServer(logger *slog.Logger) {
	cfg := loadConfig(logger)

	if err := cfg.RequireReadest(); err != nil {
		logger.Error("readest config error", "error", err)
		os.Exit(1)
	}
	if err := cfg.RequireHardcover(); err != nil {
		logger.Error("hardcover config error", "error", err)
		os.Exit(1)
	}

	readestClient := newReadestClient(cfg, logger)
	hardcoverClient := newHardcoverClient(cfg)

	st := state.New(cfg.StateFile)
	if err := st.Load(); err != nil {
		logger.Error("failed to load state", "error", err)
		os.Exit(1)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	matcher := syncsvc.NewMatcher(hardcoverClient, cfg.EnableTitleMatch)
	engine := syncsvc.NewEngine(readestClient, hardcoverClient, hardcoverClient, st, matcher, logger, cfg.ManualSync)

	go engine.Run(ctx, cfg.SyncInterval)

	syncMode := "auto"
	if cfg.ManualSync {
		syncMode = "manual"
	}

	server := web.NewServer(st, hardcoverClient, hardcoverClient, engine, cfg.ListenAddr, logger)
	go func() {
		logger.Info("readest-hardcover-sync starting", "listen", cfg.ListenAddr, "sync_mode", syncMode)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("http server error", "error", err)
		}
	}()

	<-ctx.Done()
	logger.Info("shutting down")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Error("http shutdown error", "error", err)
	}
}

// runCheckReadestAuth verifies that Readest credentials work.
func runCheckReadestAuth(logger *slog.Logger) {
	fs := flag.NewFlagSet("check-readest-auth", flag.ExitOnError)
	_ = fs.Parse(os.Args[2:])

	cfg := loadConfig(logger)
	if err := cfg.RequireReadest(); err != nil {
		logger.Error("readest config error", "error", err)
		os.Exit(1)
	}

	auth, err := readest.NewAuth(cfg.ReadestEmail, cfg.ReadestPassword)
	if err != nil {
		logger.Error("failed to create readest auth", "error", err)
		os.Exit(1)
	}

	if _, err := auth.Token(); err != nil {
		logger.Error("readest auth failed", "error", err)
		os.Exit(1)
	}

	fmt.Println("Readest auth OK")
}

// runCheckHardcoverAuth verifies that the Hardcover API token works.
func runCheckHardcoverAuth(logger *slog.Logger) {
	fs := flag.NewFlagSet("check-hardcover-auth", flag.ExitOnError)
	_ = fs.Parse(os.Args[2:])

	cfg := loadConfig(logger)
	if err := cfg.RequireHardcover(); err != nil {
		logger.Error("hardcover config error", "error", err)
		os.Exit(1)
	}

	client := newHardcoverClient(cfg)
	ctx := context.Background()

	me, err := client.GetMe(ctx)
	if err != nil {
		logger.Error("hardcover auth failed", "error", err)
		os.Exit(1)
	}

	fmt.Printf("Hardcover auth OK: user_id=%d privacy_setting_id=%d\n", me.ID, me.AccountPrivacySettingID)
}

// runListBooks pulls all books from Readest and prints them.
func runListBooks(logger *slog.Logger) {
	fs := flag.NewFlagSet("list-books", flag.ExitOnError)
	_ = fs.Parse(os.Args[2:])

	cfg := loadConfig(logger)
	if err := cfg.RequireReadest(); err != nil {
		logger.Error("readest config error", "error", err)
		os.Exit(1)
	}

	client := newReadestClient(cfg, logger)
	ctx := context.Background()

	books, err := client.PullBooks(ctx, 0)
	if err != nil {
		logger.Error("failed to pull books", "error", err)
		os.Exit(1)
	}

	for _, book := range books {
		var progress string
		if book.Progress == nil {
			progress = "no progress"
		} else {
			progress = fmt.Sprintf("%d/%d", book.Progress[0], book.Progress[1])
		}

		ids := identifier.Parse(book.Metadata, book.Title, book.Author)

		fmt.Printf("Title:  %s\n", book.Title)
		fmt.Printf("Author: %s\n", book.Author)
		fmt.Printf("Status: %s\n", book.ReadingStatus)
		fmt.Printf("Progress: %s\n", progress)

		if len(ids.HardcoverSlugs) > 0 {
			fmt.Printf("Hardcover slugs: %s\n", strings.Join(ids.HardcoverSlugs, ", "))
		}
		if len(ids.ISBN13s) > 0 {
			fmt.Printf("ISBN-13: %s\n", strings.Join(ids.ISBN13s, ", "))
		}
		if len(ids.ISBN10s) > 0 {
			fmt.Printf("ISBN-10: %s\n", strings.Join(ids.ISBN10s, ", "))
		}
		if len(ids.ASINs) > 0 {
			fmt.Printf("ASINs: %s\n", strings.Join(ids.ASINs, ", "))
		}

		fmt.Println()
	}

	fmt.Printf("Total: %d books\n", len(books))
}

// runLookup looks up a book on Hardcover by slug or ISBN.
func runLookup(logger *slog.Logger) {
	fs := flag.NewFlagSet("lookup", flag.ExitOnError)
	_ = fs.Parse(os.Args[2:])

	args := fs.Args()
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "usage: lookup <slug|isbn>")
		os.Exit(1)
	}
	query := args[0]

	cfg := loadConfig(logger)
	if err := cfg.RequireHardcover(); err != nil {
		logger.Error("hardcover config error", "error", err)
		os.Exit(1)
	}

	client := newHardcoverClient(cfg)
	ctx := context.Background()

	// Try FindBookBySlug.
	book, err := client.FindBookBySlug(ctx, query)
	if err != nil {
		logger.Error("slug lookup failed", "error", err)
		os.Exit(1)
	}
	if book != nil {
		printBook(book)
		return
	}

	// Try FindEditionByISBN13.
	edition, err := client.FindEditionByISBN13(ctx, query)
	if err != nil {
		logger.Error("ISBN-13 lookup failed", "error", err)
		os.Exit(1)
	}
	if edition != nil {
		printEdition(edition)
		return
	}

	// Try FindEditionByISBN10.
	edition, err = client.FindEditionByISBN10(ctx, query)
	if err != nil {
		logger.Error("ISBN-10 lookup failed", "error", err)
		os.Exit(1)
	}
	if edition != nil {
		printEdition(edition)
		return
	}

	fmt.Println("not found")
}

func printBook(book *hardcover.Book) {
	fmt.Printf("Title: %s\n", book.Title)
	fmt.Printf("Slug:  %s\n", book.Slug)
	fmt.Printf("Book ID: %d\n", book.ID)

	if book.DefaultEbookEdition != nil {
		ed := book.DefaultEbookEdition
		fmt.Printf("Default ebook edition ID: %d\n", ed.ID)
		if ed.Pages != nil {
			fmt.Printf("Pages: %d\n", *ed.Pages)
		}
	} else if book.DefaultPhysicalEdition != nil {
		ed := book.DefaultPhysicalEdition
		fmt.Printf("Default physical edition ID: %d\n", ed.ID)
		if ed.Pages != nil {
			fmt.Printf("Pages: %d\n", *ed.Pages)
		}
	}
}

func printEdition(edition *hardcover.Edition) {
	title := ""
	slug := ""
	if edition.Book != nil {
		title = edition.Book.Title
		slug = edition.Book.Slug
	}
	fmt.Printf("Title: %s\n", title)
	fmt.Printf("Slug:  %s\n", slug)
	fmt.Printf("Edition ID: %d\n", edition.ID)
	if edition.Pages != nil {
		fmt.Printf("Pages: %d\n", *edition.Pages)
	}
}

// runDryRun performs a single sync tick without writing to Hardcover.
func runDryRun(logger *slog.Logger) {
	fs := flag.NewFlagSet("dry-run", flag.ExitOnError)
	_ = fs.Parse(os.Args[2:])

	cfg := loadConfig(logger)
	if err := cfg.RequireReadest(); err != nil {
		logger.Error("readest config error", "error", err)
		os.Exit(1)
	}
	if err := cfg.RequireHardcover(); err != nil {
		logger.Error("hardcover config error", "error", err)
		os.Exit(1)
	}

	readestClient := newReadestClient(cfg, logger)
	hardcoverClient := newHardcoverClient(cfg)

	// Use a temp directory for state so Save() is a no-op on the real state file.
	tmpDir, err := os.MkdirTemp("", "readest-hardcover-dryrun-*")
	if err != nil {
		logger.Error("failed to create temp dir", "error", err)
		os.Exit(1)
	}

	st := state.New(filepath.Join(tmpDir, "state.json"))

	dryRunUpdater := syncsvc.NewDryRunUpdater(hardcoverClient, logger)
	matcher := syncsvc.NewMatcher(hardcoverClient, cfg.EnableTitleMatch)
	engine := syncsvc.NewEngine(readestClient, hardcoverClient, dryRunUpdater, st, matcher, logger, false)

	ctx := context.Background()
	tickErr := engine.Tick(ctx)
	_ = os.RemoveAll(tmpDir)
	if tickErr != nil {
		logger.Error("dry run tick failed", "error", tickErr)
		os.Exit(1)
	}

	fmt.Println("dry run complete")
}
