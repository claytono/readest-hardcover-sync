# readest-hardcover-sync

Syncs reading progress from [Readest](https://readest.com) to [Hardcover.app](https://hardcover.app). One-direction only: Readest → Hardcover.

Each user runs their own instance. The service polls Readest's cloud for book and progress changes, matches them to Hardcover books, and updates your reading status and page progress.

## How it works

1. Polls Readest's sync API every 10 minutes for books and reading progress
2. Matches each book to Hardcover using a fallback chain: Hardcover slug → ISBN-13 → ISBN-10 → title search
3. Updates your Hardcover reading status and page progress
4. Books that can't be auto-matched appear in the web UI for manual linking

## Setup

### Prerequisites

- A Readest account with cloud sync enabled
- A Hardcover API token (get one at https://hardcover.app/account/api)

### Environment variables

| Variable             | Required | Default      | Description                                                                                         |
| -------------------- | -------- | ------------ | --------------------------------------------------------------------------------------------------- |
| `READEST_EMAIL`      | yes      |              | Your Readest account email                                                                          |
| `READEST_PASSWORD`   | yes      |              | Your Readest account password                                                                       |
| `HARDCOVER_TOKEN`    | yes      |              | Hardcover API token (may include "Bearer " prefix)                                                  |
| `SYNC_INTERVAL`      | no       | `10m`        | How often to poll Readest                                                                           |
| `LISTEN_ADDR`        | no       | `:8080`      | Web UI listen address                                                                               |
| `STATE_FILE`         | no       | `state.json` | Path to the state file                                                                              |
| `ENABLE_TITLE_MATCH` | no       | `false`      | Allow matching books by title search (may produce false matches)                                    |
| `MANUAL_SYNC`        | no       | `false`      | If true, poll and match books but don't write to Hardcover until you click "Sync Now" in the web UI |

### Local development

```bash
# Clone and enter the project
cd readest-hardcover-sync

# Allow direnv (loads nix dev shell)
direnv allow

# Create credentials file
cat > .envrc.local << 'EOF'
READEST_EMAIL=your@email.com
READEST_PASSWORD=your-password
HARDCOVER_TOKEN=your-token
EOF

# Verify credentials
go run ./cmd/readest-hardcover-sync check-readest-auth
go run ./cmd/readest-hardcover-sync check-hardcover-auth

# Start the server in manual sync mode
MANUAL_SYNC=true go run ./cmd/readest-hardcover-sync
```

### Docker

```bash
docker build -t readest-hardcover-sync .
docker run -d \
  -e READEST_EMAIL=your@email.com \
  -e READEST_PASSWORD=your-password \
  -e HARDCOVER_TOKEN=your-token \
  -e STATE_FILE=/data/state.json \
  -e MANUAL_SYNC=true \
  -v $(pwd)/data:/data \
  -p 8080:8080 \
  readest-hardcover-sync
```

## Manual sync mode

Set `MANUAL_SYNC=true` for first-time use. In this mode:

- The service still polls Readest and matches books automatically
- Reading progress is tracked locally but **not** pushed to Hardcover
- Open the web UI at `http://localhost:8080` to review matches
- Use the link modal to manually fix any incorrect matches
- Click "Sync Now" on the status page when ready to push to Hardcover

Once you're confident the matching is correct, you can switch to automatic mode by removing `MANUAL_SYNC` or setting it to `false`.

## Web UI

Available at `http://localhost:8080`:

- **Books** — lists all matched and unmatched books. Click "Link" on unmatched books to search Hardcover and manually link them. Click "Review" on matched books to verify or change the match.
- **Status** — shows last sync times, book counts, and a "Sync Now" button.

## CLI commands

| Command                | Description                                                            |
| ---------------------- | ---------------------------------------------------------------------- |
| _(none)_               | Start the server (default)                                             |
| `check-readest-auth`   | Verify Readest credentials work                                        |
| `check-hardcover-auth` | Verify Hardcover token works                                           |
| `list-books`           | Pull and display all books from Readest with parsed identifiers        |
| `lookup <slug\|isbn>`  | Look up a book on Hardcover by slug, ISBN-13, or ISBN-10               |
| `dry-run`              | Run one sync cycle without writing to Hardcover (uses temporary state) |

## Book matching

Books are matched using identifiers embedded in EPUB metadata. The service tries these in order:

1. **Hardcover slug** — if the EPUB has a `{scheme: "HARDCOVER", value: "slug"}` identifier (added by tools like Booklore)
2. **ISBN-13** — looks up the edition on Hardcover
3. **ISBN-10** — falls back to ISBN-10 lookup
4. **Title search** — (only if `ENABLE_TITLE_MATCH=true`) searches Hardcover by title + author

If no match is found, the book appears as "unmatched" in the web UI for manual linking.
