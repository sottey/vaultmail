# VaultMail

VaultMail is a local Gmail MBOX archive browser built in Go. It imports MBOX files into a SQLite + FTS index, stores blobs on disk, and provides a simple web UI for browsing, searching, and downloading attachments.

## Features

- CLI MBOX import (streaming, resilient to malformed messages)
- Full-text search with substring matching (FTS5 trigram)
- Local storage (SQLite + filesystem blobs)
- Web UI: browse, search, filters, message view, attachments
- Inline preview for images and PDFs
- Import error logging (JSONL) with per-message context
- Query syntax in `q` (date/attachment/fielded filters)

## Requirements

- Go 1.25+
- A Gmail MBOX export file

## Quick Start

### 1) Import an MBOX

```
go run main.go import --vault ./vault --mbox ./input/all_mail.mbox
```

Optional flags:
- `--verbose` / `-v` (enable verbose output)
- `--progress` (default `true`)
- `--error-log-enabled` (default `true`)
- `--error-log` (custom log path)

### 2) Serve the Web UI

```
go run main.go serve --vault ./vault --addr 127.0.0.1:8080
```

Open `http://127.0.0.1:8080` in your browser.

Optional flags:
- `--verbose` / `-v` (enable verbose output)
- `--password` (require a password for web access)
- `--themes` (load additional themes from a directory of `.json`/`.yaml` files)
- `--unsafe-html` (render raw HTML emails without sanitizing)

### 3) Rebuild Search Index

If the FTS tokenizer changes or you want to rebuild the search index:

```
go run main.go reindex --vault ./vault
```

Optional flags:
- `--verbose` / `-v` (enable verbose output)

## Export Gmail MBOX (Google Takeout)

Step-by-step:

1. Open `takeout.google.com` and sign in.
2. Click **Deselect all**, then scroll to **Mail** and check it.
3. Click **All Mail data included** to keep everything or select specific labels.
4. Click **Next step**.
5. Choose delivery method (email link is simplest), keep **Export once**, and choose file type/size (defaults are fine).
6. Click **Create export** and wait for the email notification.
7. Download the archive(s) from the link in the email.
8. Extract the `.zip`/`.tgz`. Your MBOX file is in `Takeout/Mail/` (often named `All mail Including Spam and Trash.mbox`). If your mailbox is large, Google may split the export into multiple archive files.

## Data Layout

```
<Vault>/
  VaultMail.db
  blobs/
    eml/
    <hash>.eml
    att/
    <hash>.<ext>
```

## Notes

- Search uses FTS5 trigram tokenizer for substring matching.
- Messages that require fallback parsing are stored with `parse_failed = 1`.
- Attachments are stored on disk and referenced by SHA256.

## Query Syntax

All filters are entered in the single `q` field.

Supported filters:

- `date:>=YYYY-MM-DD` / `date:<=YYYY-MM-DD`
- `has:attachment`
- `att:>10M` / `att:>=500K` (K/M/G suffixes)
- `subject:"exact phrase"` (also works without quotes)
- `from:someone@example.com`
- `to:"Jane Doe"` (requires reindex to backfill)
- `body:"exact phrase"`

Examples:

- `from:linkedin subject:"reactivate premium" att:>1M`
- `date:>=2025-01-01 body:"red carpet"`

## Reindex Notes

`reindex` now also backfills `to_name` / `to_email` from stored `.eml` files.

## Docker Release Workflow

Tagged releases automatically build and push multi-arch Docker images to Docker Hub.

1. Ensure the repo has secrets `DOCKERHUB_USERNAME` and `DOCKERHUB_TOKEN` set in GitHub.
2. Tag a release locally and push it:

```bash
git tag v1.2.3
git push origin v1.2.3
```

This publishes:
- `sottey/vaultmail:v1.2.3`
- `sottey/vaultmail:latest`

## Docker Deployment

Import (one-time):

```bash
docker run --rm \
  -v /path/to/vault:/vault \
  -v /path/to/mbox:/data \
  sottey/vaultmail:latest \
  import --vault /vault --mbox /data/all_mail.mbox
```

Serve:

```bash
docker run --rm -p 8080:8080 \
  -v /path/to/vault:/vault \
  sottey/vaultmail:latest \
  serve --vault /vault --addr 0.0.0.0:8080
```

## Themes

VaultMail ships with built-in themes (Paper, Sage, Slate, Noir) and lets you add your own.

1. Create a themes directory with JSON or YAML files.
2. Start the server with `--themes /path/to/themes`.
3. Choose the theme from the header dropdown (saved in a cookie).

Example `ocean.yaml`:

```yaml
name: ocean
vars:
  bg: "#e6f2ff"
  ink: "#0a1c2e"
  muted: "#4b657a"
  accent: "#0077cc"
  accent-2: "#00a3a3"
  card: "#ffffff"
  border: "#c7d7e6"
  header-bg: "linear-gradient(90deg, #f2f8ff, #dce9f7)"
  sidebar-bg: "#edf4fb"
  row-bg: "#ffffff"
  row-alt: "#f6f9fd"
```

## License

MIT. See `LICENSE`.
