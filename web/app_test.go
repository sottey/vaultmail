package web

import "testing"

func TestParseQuery(t *testing.T) {
	q := `from:alice subject:report date:>=2025-01-01 att:>10M has:attachment "meeting notes"`
	parsed := parseQuery(q)

	if parsed.FromQuery != "alice" {
		t.Fatalf("expected from query, got %q", parsed.FromQuery)
	}
	if parsed.SubjectQuery != "report" {
		t.Fatalf("expected subject query, got %q", parsed.SubjectQuery)
	}
	if parsed.FromDate != "2025-01-01" {
		t.Fatalf("expected from date, got %q", parsed.FromDate)
	}
	if !parsed.HasAttachments {
		t.Fatalf("expected has attachments true")
	}
	if parsed.MinAttachmentBytes != 10*1024*1024 {
		t.Fatalf("expected min attachment 10M, got %d", parsed.MinAttachmentBytes)
	}
	if parsed.RawText != "meeting notes" {
		t.Fatalf("expected raw text, got %q", parsed.RawText)
	}
	if parsed.FTSQuery == "" {
		t.Fatalf("expected FTS query")
	}
}

func TestParseSize(t *testing.T) {
	cases := []struct {
		in  string
		out int64
		ok  bool
	}{
		{"10K", 10 * 1024, true},
		{"5M", 5 * 1024 * 1024, true},
		{"1G", 1 * 1024 * 1024 * 1024, true},
		{"42", 42, true},
		{"-1", 0, false},
	}
	for _, tc := range cases {
		got, ok := parseSize(tc.in)
		if ok != tc.ok || (ok && got != tc.out) {
			t.Fatalf("parseSize(%q) = %d,%v (want %d,%v)", tc.in, got, ok, tc.out, tc.ok)
		}
	}
}

func TestTokenizeQuery(t *testing.T) {
	tokens := tokenizeQuery(`subject:"hello world" foo "bar baz"`)
	if len(tokens) != 4 {
		t.Fatalf("expected 4 tokens, got %d", len(tokens))
	}
	if tokens[0].Value != "subject:" || tokens[0].Quoted {
		t.Fatalf("unexpected first token: %+v", tokens[0])
	}
	if tokens[1].Value != "hello world" || !tokens[1].Quoted {
		t.Fatalf("unexpected second token: %+v", tokens[1])
	}
	if tokens[2].Value != "foo" || tokens[2].Quoted {
		t.Fatalf("unexpected third token: %+v", tokens[2])
	}
	if tokens[3].Value != "bar baz" || !tokens[3].Quoted {
		t.Fatalf("unexpected fourth token: %+v", tokens[3])
	}
}

func TestSanitizeFilename(t *testing.T) {
	name := "bad\nname\r.txt"
	sanitized := sanitizeFilename(name)
	if sanitized != "bad name .txt" {
		t.Fatalf("expected sanitized filename, got %q", sanitized)
	}
}

func TestIsPreviewable(t *testing.T) {
	if !isPreviewable("image/png") {
		t.Fatalf("expected image/png previewable")
	}
	if !isPreviewable("application/pdf") {
		t.Fatalf("expected application/pdf previewable")
	}
	if isPreviewable("text/plain") {
		t.Fatalf("expected text/plain not previewable")
	}
}
