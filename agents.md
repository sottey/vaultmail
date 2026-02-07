# VaultMail (Go) — Agent Brief

This document is the **authoritative brief** for building VaultMail.
It is intended to fully align an AI coding agent (Codex in VS Code) before any code is written.
Do **not** guess beyond what is written here.

---

## Project Goal

Build a Go application that:

1. Imports **Gmail MBOX exports via CLI**
2. Indexes email for **fast full‑text search**
3. Stores data locally (SQLite + filesystem)
4. Provides a **web UI** to browse, search, view messages, and download attachments

Target archive size: **~10 GB**

---

## MVP Scope (Strict)

### Included
- CLI-based MBOX import (no web upload)
- Streaming import (never load entire MBOX into memory)
- SQLite database with **FTS5**
- Filesystem storage for:
  - raw `.eml` messages
  - attachments
- Web UI:
  - browse newest messages
  - full-text search
  - date + attachment filters
  - message detail view
  - attachment downloads

### Explicitly Excluded
- Threaded conversations
- Gmail-style advanced search operators
- Inline attachment / CID image rendering
- Labels or folders
- Authentication / multi-user support
- IMAP / live syncing

---

## Architecture Overview

### Tech Choices
- Language: **Go**
- Web routing: `chi`
- Templates: `html/template`
- Database: **SQLite + FTS5**
- Blob storage: filesystem
- UI: server-rendered HTML (HTMX optional)

### Vault Layout

```
<VAULT_DIR>/
  VaultMail.db
  blobs/
    eml/
      ab/
        <sha256>.eml
    att/
      cd/
        <sha256>.<ext?>
```

- All DB paths are **relative to vault root**
- Hash prefixes prevent large directories

---

## Database Schema

### messages
```sql
CREATE TABLE messages (
  id INTEGER PRIMARY KEY,
  message_id TEXT,
  date_utc INTEGER NOT NULL,
  from_name TEXT,
  from_email TEXT,
  subject TEXT,
  snippet TEXT,
  eml_path TEXT NOT NULL,
  size_bytes INTEGER NOT NULL,
  has_attachments INTEGER NOT NULL DEFAULT 0,
  import_batch_id INTEGER NOT NULL,
  created_utc INTEGER NOT NULL
);

CREATE UNIQUE INDEX idx_messages_message_id
ON messages(message_id)
WHERE message_id IS NOT NULL;
```

---

### message_dedupe
```sql
CREATE TABLE message_dedupe (
  message_fk INTEGER PRIMARY KEY REFERENCES messages(id),
  content_hash TEXT NOT NULL UNIQUE
);
```

---

### attachments
```sql
CREATE TABLE attachments (
  id INTEGER PRIMARY KEY,
  message_fk INTEGER REFERENCES messages(id),
  filename TEXT,
  mime_type TEXT,
  size_bytes INTEGER NOT NULL,
  sha256 TEXT NOT NULL,
  stored_path TEXT NOT NULL
);
```

---

### import_batches
```sql
CREATE TABLE import_batches (
  id INTEGER PRIMARY KEY,
  filename TEXT NOT NULL,
  started_utc INTEGER NOT NULL,
  finished_utc INTEGER,
  messages_imported INTEGER NOT NULL DEFAULT 0,
  errors_count INTEGER NOT NULL DEFAULT 0
);
```

---

### FTS5
```sql
CREATE VIRTUAL TABLE messages_fts
USING fts5(subject, from_text, body_text, content='', tokenize='trigram');

CREATE TABLE messages_fts_map (
  message_fk INTEGER PRIMARY KEY REFERENCES messages(id),
  fts_rowid INTEGER NOT NULL UNIQUE
);
```

---

## CLI Commands

### Required
- `vaultmail import --vault <dir> --mbox <path>`
- `vaultmail serve --vault <dir> --addr 127.0.0.1:8080`

Optional:
- `vaultmail init --vault <dir>`
- `--verbose` / `-v` (import/serve/reindex)
- `--password` (serve only; enables login + cookie)
- `--themes` (serve only; load external JSON/YAML themes)
- `--unsafe-html` (serve only; render raw HTML emails)

---

## Import Pipeline

### Rules
- Streaming MBOX parsing only
- Never abort entire import due to one bad message
- Write blobs **before** DB rows
- Use transactions and prepared statements
- Commit every 250–1000 messages

### MBOX Parsing (Gmail)
- Separator line starts with: `From `
- Body lines may contain escaped `>From `
- Must distinguish real separators from escaped content
- Naive splitting is forbidden

---

## MIME Handling

- Use `net/mail`, `mime`, `mime/multipart`
- Decode base64 and quoted-printable
- Charset support:
  - UTF‑8
  - ISO‑8859‑1 fallback

### Body Selection
- Prefer `text/plain`
- Else strip HTML tags from `text/html`
- Generate snippet (first ~200 chars)
- Preserve raw HTML body for optional rendering in UI

### Attachments
- Any MIME part with:
  - `Content-Disposition: attachment`
  - OR filename present and not selected as body
- Store on disk using SHA256 filename
- Download-only in UI

---

## Dedupe Strategy

1. Prefer `Message-ID`
2. Fallback hash:

```
sha256(
  from_email +
  date_utc +
  subject +
  first_8k(body_text) +
  size_bytes
)
```

---

## SQLite Import Pragmas

During import:
- `journal_mode=WAL`
- `synchronous=NORMAL`
- `temp_store=MEMORY`

---

## Web Routes

- `/` → browse + search
- `/message/{id}` → message view
- `/attachment/{id}/download` → attachment
- `/imports` → import history (optional)

---

## UI Expectations

- List: date, from, subject, snippet, attachment indicator
- Message view: headers, body text, attachment list
- No SPA
- Minimal JS
- Header search bar with filter hints
- From and To columns shown in results
- Optional login + logout when password enabled

---

## Build Order

1. CLI skeleton
2. SQLite schema
3. Blob storage helpers
4. Streaming MBOX parser
5. MIME extraction
6. Importer + dedupe
7. Web UI
8. Error handling + polish

---

## Hard Constraints

- Do NOT store large blobs in SQLite
- Do NOT crash on malformed input
- Do NOT block web server with imports
- Do NOT assume Message-ID exists
- Do NOT render unsanitized HTML by default (only allow via explicit `--unsafe-html`)

---

## MVP Success Criteria

- Import Gmail MBOX via CLI
- Browse newest messages
- Full-text search works
- Messages viewable
- Attachments downloadable

Nothing beyond this scope.

---

## Current Implementation Notes

### Implemented
- CLI `import`, `serve`, and `reindex`
- SQLite schema + FTS5 (trigram tokenizer for substring search)
- Filesystem blob storage for `.eml` and attachments
- Streaming MBOX parser with long-line handling and robust separator detection
- MIME parsing with attachment handling, plus EML fallback when parsing fails
- Web UI: browse, search, filter hints, message view, attachment download + inline view for images/PDFs
- Import error logging to JSONL with per-message context
 - Query syntax (in `q`): `date:>=`, `date:<=`, `has:attachment`, `att:>10M`, `subject:`, `from:`, `to:`, `body:`
 - `to_name` / `to_email` persisted on import (backfilled on `reindex`)
- Optional password auth with cookie + logout
- Built-in themes + external themes via `--themes`
- HTML emails rendered safely by default; `--unsafe-html` renders raw HTML

### Search Behavior
- FTS uses `trigram` tokenizer for substring matching (e.g., `kellibyers` matches `kellibyersclark`)
- `reindex` rebuilds the FTS index after tokenizer/schema changes
- If FTS query fails, UI falls back to `LIKE` on subject/from/snippet
 - Fielded search in `q` supports `subject:`, `from:`, `to:`, and `body:` (quoted phrases supported)

### Parse Failures
- Messages that required fallback parsing are stored with `parse_failed = 1`
- These messages still import and are viewable, but attachments may be missing

### CLI Usage
- `vaultmail import --vault <dir> --mbox <path>`
- `vaultmail serve --vault <dir> --addr 127.0.0.1:8080`
- `vaultmail reindex --vault <dir>`
- `vaultmail serve --vault <dir> --addr 127.0.0.1:8080 --password <pw>`
- `vaultmail serve --vault <dir> --addr 127.0.0.1:8080 --themes /path/to/themes`
- `vaultmail serve --vault <dir> --addr 127.0.0.1:8080 --unsafe-html`

### Docker (Manual Steps)
1. Build: `docker build -t vaultmail:latest .`
3. Import (one-time):
   - `docker run --rm -v /path/to/vault:/vault -v /path/to/mbox:/data vaultmail:latest import --vault /vault --mbox /data/all_mail.mbox`
4. Serve:
   - `docker run --rm -p 8080:8080 -v /path/to/vault:/vault vaultmail:latest serve --vault /vault --addr 0.0.0.0:8080`
