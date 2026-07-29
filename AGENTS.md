# readest-hardcover-sync

Syncs reading progress from Readest cloud to Hardcover.app.

## Context

Readest is an e-book reader with cloud sync via Supabase. Hardcover.app is a book tracking platform with a GraphQL API. There is no integration between them. This service bridges the gap by polling Readest's cloud for reading progress and pushing updates to Hardcover.

Each user runs their own instance, configured with their Readest and Hardcover credentials via environment variables. The Readest sync API was reverse-engineered from the app source — there is no official API documentation.

**Read the README.md** for user-facing documentation: setup, usage, CLI commands, and Docker deployment.

## Development

- Use `task test` to run tests
- Use `task test-live-hardcover` to run live Hardcover API contract tests when `HARDCOVER_TOKEN` is set
- Use `task lint` to run linters
- Use `task build` to build the binary
- Use `task coverage` to check test coverage
- Use `task run` to build and run the server
- Use `task demo-covers` to download cover images for demo mode
- Use `task screenshots` to generate README screenshots from demo mode

## Rules

- **Read the README.md at the start of every new session** to understand user-facing behavior
- Treat `state.json` and all in-memory `BookState` fields as a disposable cache, never as authoritative data or required provenance
- Reconstruct missing Hardcover IDs and reading state from Hardcover before deciding whether to create or update remote records
- Immediately before creating a Hardcover `user_book_read`, query the current remote reading history; if any row exists, do not create another
- When recovering from missing cache state, adopt only one compatible unfinished remote reading row; leave completed, incompatible, or ambiguous histories untouched
- All packages live under `internal/`
- Import organization: stdlib, external, local (`github.com/claytono/readest-hardcover-sync`)
- Use `testify/require` for blocking assertions, `testify/assert` for non-blocking
- Constructor pattern: `New(opts Options)` returning `(*T, error)`
- Error wrapping: `fmt.Errorf("context: %w", err)`
- Sentinel errors as package-level vars: `var ErrFoo = errors.New("...")`
- Never bypass pre-commit hooks

## Architecture

- Binary requires a subcommand: `serve`, `check-readest-auth`, `check-hardcover-auth`, `list-readest-books`, `lookup`, `dry-run`, `demo`
- Sync engine polls Readest on interval, matches books to Hardcover via identifier fallback chain (slug → ISBN-13 → ISBN-10 → title), pushes status and progress
- `MANUAL_SYNC=true`: polling still runs (reads from Readest, matches books) but uses a dry-run updater for Hardcover writes. Actual writes only via "Sync Now" in web UI.

## Web UI

- Single-page dark-themed layout with fixed sidebar and scrollable book card grid
- htmx for partial updates, native `EventSource` for real-time sync log via SSE (`/events`)
- Templates embedded via `//go:embed` in `internal/web/server.go` — changes require rebuild
- Static files (`static/`) and covers (`covers/`) served from disk via `http.FileServer`
- Sidebar: sync status (auto-refreshes), Sync/Resync All buttons, live sync log
- Book cards: cover images (cached locally from Hardcover), progress bars, match badges
- Detail modal: click a card to see identifiers, sync state, and actions (View on Hardcover, Relink, Unlink)
- Link modal: search Hardcover, opened from detail modal's Link/Relink button
- Client-side filter tabs (All/Matched/Unmatched) and search (title, author, series)
- Filter/search state persisted in URL hash
- Mobile responsive: sidebar becomes slide-out drawer on narrow screens
- SSE may require `proxy_buffering off` in nginx reverse proxy configs

## Environment / Config

- All config from env vars (`internal/config/config.go`)
- Local dev: `.envrc` loads nix flake + sources `.envrc.local` via `dotenv_if_exists`
- Credentials go in `.envrc.local` (gitignored)
- Disposable sync state is cached in `state.json` (configurable via `STATE_FILE`); deleting it must not prevent reconciliation from Readest and Hardcover
- Cover images cached in `covers/` directory (configurable via `COVERS_DIR`)
- Optional notifications use `SLACK_WEBHOOK_URL`; set `PUBLIC_BASE_URL` so Slack can link to `/books?book=<hash>`; cover images use Hardcover's cached image URLs
- Demo cover images downloaded to `demo-covers/` (gitignored, via `task demo-covers`)
- Generated screenshots in `screenshots/` (committed)

## Before every commit

- Verify `.gitignore` and `.dockerignore` are up to date if new file types or directories were introduced
- Verify `AGENTS.md` reflects any new commands, env vars, patterns, or architectural changes
- Verify `.env.example` includes any new environment variables

## Design decisions

- **Single-user only** — one instance per user, no auth on the web UI. This is a scope constraint, not a missing feature.
- **Polling, not webhooks** — Readest has no webhook support; the API was reverse-engineered. Polling every 10 minutes is the only option.
- **Dry-run as decorator** — the `dryRunUpdater` wraps `ProgressUpdater` so engine code stays unchanged. Chosen over a flag on the engine to avoid scattering conditionals through mutation paths.
- **One-direction sync** — Readest → Hardcover only. No reverse sync planned.
- **Disposable local state** — `state.json` accelerates synchronization and supports the UI, but is never a source of truth. Cache loss must recover from Readest and Hardcover rather than orphaning remote records or relying on locally remembered ownership.
- **Conservative reading history** — the sync never creates a new Hardcover reading row when any remote reading history already exists. A uniquely identifiable compatible unfinished row may be resumed after cache loss; completed, incompatible, or ambiguous histories are left unchanged rather than interpreted as rereads.
- **Local cover cache** — covers downloaded from Hardcover at match time, served locally to avoid hotlinking their CDN. Backfilled on sync for existing books missing covers.
- **EventBus for SSE** — `internal/sync/events.go` broadcasts structured sync events; SSE handler in `internal/web/handlers.go` streams them to the browser via `EventSource`.

## Key patterns

- Dry-run updater (`internal/sync/dry_run.go`): decorator wrapping `ProgressUpdater` — reads pass through, writes are no-ops
- Hardcover client implements both `BookFinder` and `ProgressUpdater` interfaces
- `GetUserBook` queries are scoped to the authenticated user (user ID cached from `GetMe`)
- Notifications use the `sync.Notifier` interface; Slack lives in `internal/notifications` and must remain best-effort
- Tests use `httptest` servers, per-package mocks, `testify/require` + `testify/assert`
