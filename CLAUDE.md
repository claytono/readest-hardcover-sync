# readest-hardcover-sync

Syncs reading progress from Readest cloud to Hardcover.app.

## Context

Readest is an e-book reader with cloud sync via Supabase. Hardcover.app is a book tracking platform with a GraphQL API. There is no integration between them. This service bridges the gap by polling Readest's cloud for reading progress and pushing updates to Hardcover.

Each user runs their own instance, configured with their Readest and Hardcover credentials via environment variables. The Readest sync API was reverse-engineered from the app source — there is no official API documentation.

**Read the README.md** for user-facing documentation: setup, usage, CLI commands, and Docker deployment.

## Development

- Use `task test` to run tests
- Use `task lint` to run linters
- Use `task build` to build the binary
- Use `task coverage` to check test coverage
- Use `task run` to build and run the server

## Rules

- **Read the README.md at the start of every new session** to understand user-facing behavior
- All packages live under `internal/`
- Import organization: stdlib, external, local (`github.com/claytono/readest-hardcover-sync`)
- Use `testify/require` for blocking assertions, `testify/assert` for non-blocking
- Constructor pattern: `New(opts Options)` returning `(*T, error)`
- Error wrapping: `fmt.Errorf("context: %w", err)`
- Sentinel errors as package-level vars: `var ErrFoo = errors.New("...")`
- Never bypass pre-commit hooks

## Architecture

- Binary requires a subcommand: `serve`, `check-readest-auth`, `check-hardcover-auth`, `list-readest-books`, `lookup`, `dry-run`
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
- State persisted in `state.json` (configurable via `STATE_FILE`)
- Cover images cached in `covers/` directory (configurable via `COVERS_DIR`)

## Before every commit

- Verify `.gitignore` and `.dockerignore` are up to date if new file types or directories were introduced
- Verify `CLAUDE.md` reflects any new commands, env vars, patterns, or architectural changes
- Verify `.env.example` includes any new environment variables

## Design decisions

- **Single-user only** — one instance per user, no auth on the web UI. This is a scope constraint, not a missing feature.
- **Polling, not webhooks** — Readest has no webhook support; the API was reverse-engineered. Polling every 10 minutes is the only option.
- **Dry-run as decorator** — the `dryRunUpdater` wraps `ProgressUpdater` so engine code stays unchanged. Chosen over a flag on the engine to avoid scattering conditionals through mutation paths.
- **One-direction sync** — Readest → Hardcover only. No reverse sync planned.
- **Local cover cache** — covers downloaded from Hardcover at match time, served locally to avoid hotlinking their CDN. Backfilled on sync for existing books missing covers.
- **EventBus for SSE** — `internal/sync/events.go` broadcasts structured sync events; SSE handler in `internal/web/handlers.go` streams them to the browser via `EventSource`.

## Key patterns

- Dry-run updater (`internal/sync/dry_run.go`): decorator wrapping `ProgressUpdater` — reads pass through, writes are no-ops
- Hardcover client implements both `BookFinder` and `ProgressUpdater` interfaces
- `GetUserBook` queries are scoped to the authenticated user (user ID cached from `GetMe`)
- Tests use `httptest` servers, per-package mocks, `testify/require` + `testify/assert`
