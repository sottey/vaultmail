package vault

import (
	"path/filepath"
	"testing"
)

func TestEmlRelPathUsesHashPrefix(t *testing.T) {
	path := EmlRelPath("a")
	expected := filepath.Join("blobs", "eml", "xx", "a.eml")
	if path != expected {
		t.Fatalf("expected %q, got %q", expected, path)
	}

	path = EmlRelPath("ab1234")
	expected = filepath.Join("blobs", "eml", "ab", "ab1234.eml")
	if path != expected {
		t.Fatalf("expected %q, got %q", expected, path)
	}
}

func TestAttachmentRelPathNormalizesExt(t *testing.T) {
	path := AttachmentRelPath("abcd", "pdf")
	expected := filepath.Join("blobs", "att", "ab", "abcd.pdf")
	if path != expected {
		t.Fatalf("expected %q, got %q", expected, path)
	}

	path = AttachmentRelPath("abcd", ".txt")
	expected = filepath.Join("blobs", "att", "ab", "abcd.txt")
	if path != expected {
		t.Fatalf("expected %q, got %q", expected, path)
	}

	path = AttachmentRelPath("abcd", " ")
	expected = filepath.Join("blobs", "att", "ab", "abcd")
	if path != expected {
		t.Fatalf("expected %q, got %q", expected, path)
	}
}
