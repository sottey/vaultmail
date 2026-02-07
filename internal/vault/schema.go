package vault

import "database/sql"

const schemaSQL = `
PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS messages (
  id INTEGER PRIMARY KEY,
  message_id TEXT,
  date_utc INTEGER NOT NULL,
  from_name TEXT,
  from_email TEXT,
  to_name TEXT,
  to_email TEXT,
  subject TEXT,
  snippet TEXT,
  eml_path TEXT NOT NULL,
  size_bytes INTEGER NOT NULL,
  has_attachments INTEGER NOT NULL DEFAULT 0,
  parse_failed INTEGER NOT NULL DEFAULT 0,
  deleted INTEGER NOT NULL DEFAULT 0,
  import_batch_id INTEGER NOT NULL,
  created_utc INTEGER NOT NULL
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_messages_message_id
ON messages(message_id)
WHERE message_id IS NOT NULL;

CREATE TABLE IF NOT EXISTS message_dedupe (
  message_fk INTEGER PRIMARY KEY REFERENCES messages(id),
  content_hash TEXT NOT NULL UNIQUE
);

CREATE TABLE IF NOT EXISTS attachments (
  id INTEGER PRIMARY KEY,
  message_fk INTEGER REFERENCES messages(id),
  filename TEXT,
  mime_type TEXT,
  size_bytes INTEGER NOT NULL,
  sha256 TEXT NOT NULL,
  stored_path TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS import_batches (
  id INTEGER PRIMARY KEY,
  filename TEXT NOT NULL,
  started_utc INTEGER NOT NULL,
  finished_utc INTEGER,
  messages_imported INTEGER NOT NULL DEFAULT 0,
  errors_count INTEGER NOT NULL DEFAULT 0
);

CREATE VIRTUAL TABLE IF NOT EXISTS messages_fts
USING fts5(subject, from_text, body_text, content='', tokenize='trigram');

CREATE TABLE IF NOT EXISTS messages_fts_map (
  message_fk INTEGER PRIMARY KEY REFERENCES messages(id),
  fts_rowid INTEGER NOT NULL UNIQUE
);
`

func InitSchema(db *sql.DB) error {
	if _, err := db.Exec(schemaSQL); err != nil {
		return err
	}
	if err := ensureParseFailedColumn(db); err != nil {
		return err
	}
	if err := ensureToColumns(db); err != nil {
		return err
	}
	return ensureDeletedColumn(db)
}

func ensureParseFailedColumn(db *sql.DB) error {
	rows, err := db.Query(`PRAGMA table_info(messages);`)
	if err != nil {
		return err
	}
	defer rows.Close()

	hasColumn := false
	for rows.Next() {
		var cid int
		var name string
		var ctype string
		var notnull int
		var dflt sql.NullString
		var pk int
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return err
		}
		if name == "parse_failed" {
			hasColumn = true
			break
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if hasColumn {
		return nil
	}
	_, err = db.Exec(`ALTER TABLE messages ADD COLUMN parse_failed INTEGER NOT NULL DEFAULT 0;`)
	return err
}

func ensureToColumns(db *sql.DB) error {
	rows, err := db.Query(`PRAGMA table_info(messages);`)
	if err != nil {
		return err
	}
	defer rows.Close()

	hasToName := false
	hasToEmail := false
	for rows.Next() {
		var cid int
		var name string
		var ctype string
		var notnull int
		var dflt sql.NullString
		var pk int
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return err
		}
		if name == "to_name" {
			hasToName = true
		}
		if name == "to_email" {
			hasToEmail = true
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}

	if !hasToName {
		if _, err := db.Exec(`ALTER TABLE messages ADD COLUMN to_name TEXT;`); err != nil {
			return err
		}
	}
	if !hasToEmail {
		if _, err := db.Exec(`ALTER TABLE messages ADD COLUMN to_email TEXT;`); err != nil {
			return err
		}
	}
	return nil
}

func ensureDeletedColumn(db *sql.DB) error {
	rows, err := db.Query(`PRAGMA table_info(messages);`)
	if err != nil {
		return err
	}
	defer rows.Close()

	hasColumn := false
	for rows.Next() {
		var cid int
		var name string
		var ctype string
		var notnull int
		var dflt sql.NullString
		var pk int
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return err
		}
		if name == "deleted" {
			hasColumn = true
			break
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if hasColumn {
		return nil
	}
	_, err = db.Exec(`ALTER TABLE messages ADD COLUMN deleted INTEGER NOT NULL DEFAULT 0;`)
	return err
}
