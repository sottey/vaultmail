package vault

import (
	"database/sql"
	"fmt"
	"io"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

type Vault struct {
	Root string
	DB   *sql.DB
}

func Open(root string) (*Vault, error) {
	if root == "" {
		return nil, fmt.Errorf("vault root is required")
	}

	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}

	if err := os.MkdirAll(abs, 0o755); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(abs, "blobs", "eml"), 0o755); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(abs, "blobs", "att"), 0o755); err != nil {
		return nil, err
	}

	dbPath := filepath.Join(abs, "VaultMail.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, err
	}

	if err := InitSchema(db); err != nil {
		_ = db.Close()
		return nil, err
	}

	return &Vault{Root: abs, DB: db}, nil
}

func (v *Vault) Close() error {
	if v == nil || v.DB == nil {
		return nil
	}
	return v.DB.Close()
}

func (v *Vault) AbsPath(rel string) string {
	return filepath.Join(v.Root, rel)
}

func (v *Vault) WriteBlob(rel string, r io.Reader) (int64, error) {
	abs := v.AbsPath(rel)
	if st, err := os.Stat(abs); err == nil {
		return st.Size(), nil
	}

	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return 0, err
	}

	tmp := abs + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return 0, err
	}

	n, copyErr := io.Copy(f, r)
	closeErr := f.Close()
	if copyErr != nil {
		_ = os.Remove(tmp)
		return 0, copyErr
	}
	if closeErr != nil {
		_ = os.Remove(tmp)
		return 0, closeErr
	}
	if err := os.Rename(tmp, abs); err != nil {
		_ = os.Remove(tmp)
		return 0, err
	}

	return n, nil
}
