package importer

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	"github.com/sottey/vaultmail/internal/vault"
)

type ReindexResult struct {
	Messages int
	Errors   int
	Updated  int
}

func RebuildFTS(v *vault.Vault) (ReindexResult, error) {
	if v == nil || v.DB == nil {
		return ReindexResult{}, fmt.Errorf("vault is required")
	}

	if err := recreateFTSTables(v.DB); err != nil {
		return ReindexResult{}, err
	}

	rows, err := v.DB.Query(`SELECT id, eml_path FROM messages ORDER BY id ASC`)
	if err != nil {
		return ReindexResult{}, err
	}
	defer rows.Close()

	result := ReindexResult{}

	tx, stmts, err := beginReindexTx(v.DB)
	if err != nil {
		return result, err
	}
	defer func() {
		_ = stmts.close()
		_ = tx.Rollback()
	}()

	processed := 0
	for rows.Next() {
		var id int64
		var emlRel string
		if err := rows.Scan(&id, &emlRel); err != nil {
			return result, err
		}

		emlPath := filepath.Join(v.Root, emlRel)
		emlData, err := os.ReadFile(emlPath)
		if err != nil {
			result.Errors++
			continue
		}

		parsed, _ := ParseEmailWithFallback(emlData, nil)
		if _, err := stmts.insertFts.Exec(parsed.Subject, formatFromText(parsed.FromName, parsed.FromEmail), parsed.BodyText); err != nil {
			result.Errors++
			continue
		}
		if _, err := stmts.insertFtsMap.Exec(id); err != nil {
			result.Errors++
			continue
		}
		if _, err := stmts.updateTo.Exec(nullableString(parsed.ToName), nullableString(parsed.ToEmail), id); err == nil {
			result.Updated++
		}

		result.Messages++
		processed++
		if processed%1000 == 0 {
			if err := stmts.close(); err != nil {
				return result, err
			}
			if err := tx.Commit(); err != nil {
				return result, err
			}
			tx, stmts, err = beginReindexTx(v.DB)
			if err != nil {
				return result, err
			}
		}
	}
	if err := rows.Err(); err != nil {
		return result, err
	}

	if err := stmts.close(); err != nil {
		return result, err
	}
	if err := tx.Commit(); err != nil {
		return result, err
	}

	return result, nil
}

func recreateFTSTables(db *sql.DB) error {
	stmts := []string{
		`DROP TABLE IF EXISTS messages_fts_map;`,
		`DROP TABLE IF EXISTS messages_fts;`,
		`CREATE VIRTUAL TABLE IF NOT EXISTS messages_fts USING fts5(subject, from_text, body_text, content='', tokenize='trigram');`,
		`CREATE TABLE IF NOT EXISTS messages_fts_map (message_fk INTEGER PRIMARY KEY REFERENCES messages(id), fts_rowid INTEGER NOT NULL UNIQUE);`,
	}
	for _, stmt := range stmts {
		if _, err := db.Exec(stmt); err != nil {
			return err
		}
	}
	return nil
}

type reindexStatements struct {
	insertFts    *sql.Stmt
	insertFtsMap *sql.Stmt
	updateTo     *sql.Stmt
}

func (s *reindexStatements) close() error {
	var err error
	if s == nil {
		return nil
	}
	if s.insertFts != nil {
		if cerr := s.insertFts.Close(); cerr != nil && err == nil {
			err = cerr
		}
	}
	if s.insertFtsMap != nil {
		if cerr := s.insertFtsMap.Close(); cerr != nil && err == nil {
			err = cerr
		}
	}
	if s.updateTo != nil {
		if cerr := s.updateTo.Close(); cerr != nil && err == nil {
			err = cerr
		}
	}
	return err
}

func beginReindexTx(db *sql.DB) (*sql.Tx, *reindexStatements, error) {
	tx, err := db.Begin()
	if err != nil {
		return nil, nil, err
	}

	stmts := &reindexStatements{}
	if stmts.insertFts, err = tx.Prepare(`INSERT INTO messages_fts (subject, from_text, body_text) VALUES (?, ?, ?)`); err != nil {
		_ = tx.Rollback()
		return nil, nil, err
	}
	if stmts.insertFtsMap, err = tx.Prepare(`INSERT INTO messages_fts_map (message_fk, fts_rowid) VALUES (?, last_insert_rowid())`); err != nil {
		_ = tx.Rollback()
		return nil, nil, err
	}
	if stmts.updateTo, err = tx.Prepare(`UPDATE messages SET to_name = ?, to_email = ? WHERE id = ?`); err != nil {
		_ = tx.Rollback()
		return nil, nil, err
	}

	return tx, stmts, nil
}
