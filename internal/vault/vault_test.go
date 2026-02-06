package vault

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenCreatesDirsAndDB(t *testing.T) {
	root := t.TempDir()
	v, err := Open(root)
	if err != nil {
		t.Fatalf("open vault: %v", err)
	}
	defer v.Close()

	checks := []string{
		filepath.Join(root, "blobs", "eml"),
		filepath.Join(root, "blobs", "att"),
		filepath.Join(root, "VaultMail.db"),
	}
	for _, path := range checks {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected %s to exist: %v", path, err)
		}
	}
}

func TestWriteBlobIsIdempotent(t *testing.T) {
	root := t.TempDir()
	v, err := Open(root)
	if err != nil {
		t.Fatalf("open vault: %v", err)
	}
	defer v.Close()

	rel := EmlRelPath("abcd")

	n1, err := v.WriteBlob(rel, strings.NewReader("hello"))
	if err != nil {
		t.Fatalf("write blob: %v", err)
	}
	if n1 != 5 {
		t.Fatalf("expected size 5, got %d", n1)
	}

	n2, err := v.WriteBlob(rel, strings.NewReader("world!!!!!"))
	if err != nil {
		t.Fatalf("write blob again: %v", err)
	}
	if n2 != n1 {
		t.Fatalf("expected size %d, got %d", n1, n2)
	}

	data, err := os.ReadFile(v.AbsPath(rel))
	if err != nil {
		t.Fatalf("read blob: %v", err)
	}
	if string(data) != "hello" {
		t.Fatalf("expected original contents, got %q", string(data))
	}
}
