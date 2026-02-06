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

### 3) Rebuild Search Index

If the FTS tokenizer changes or you want to rebuild the search index:

```
go run main.go reindex --vault ./vault
```

Optional flags:
- `--verbose` / `-v` (enable verbose output)

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

## License

MIT. See `LICENSE`.
