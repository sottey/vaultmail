package importer

import (
	"io"
	"strings"
	"testing"
	"time"
)

func TestParseEmailPlainText(t *testing.T) {
	raw := strings.Join([]string{
		"From: Alice <alice@example.com>",
		"To: Bob <bob@example.com>",
		"Subject: Hello",
		"Date: Mon, 02 Jan 2006 15:04:05 -0700",
		"Content-Type: text/plain; charset=utf-8",
		"",
		"This is the body.",
	}, "\r\n")

	parsed, err := ParseEmail([]byte(raw), nil)
	if err != nil {
		t.Fatalf("parse email: %v", err)
	}
	if parsed.FromEmail != "alice@example.com" {
		t.Fatalf("expected from email, got %q", parsed.FromEmail)
	}
	if parsed.ToEmail != "bob@example.com" {
		t.Fatalf("expected to email, got %q", parsed.ToEmail)
	}
	if parsed.Subject != "Hello" {
		t.Fatalf("expected subject, got %q", parsed.Subject)
	}
	if parsed.BodyText != "This is the body." {
		t.Fatalf("expected body, got %q", parsed.BodyText)
	}
	if parsed.Snippet == "" {
		t.Fatalf("expected snippet")
	}
	expectedDate := time.Date(2006, 1, 2, 22, 4, 5, 0, time.UTC)
	if !parsed.DateUTC.Equal(expectedDate) {
		t.Fatalf("expected date %s, got %s", expectedDate, parsed.DateUTC)
	}
}

func TestParseEmailHtmlFallback(t *testing.T) {
	raw := strings.Join([]string{
		"From: Alice <alice@example.com>",
		"To: Bob <bob@example.com>",
		"Subject: HTML",
		"Date: Mon, 02 Jan 2006 15:04:05 -0700",
		"Content-Type: text/html; charset=utf-8",
		"",
		"<html><body><h1>Hello</h1><p>World</p></body></html>",
	}, "\r\n")

	parsed, err := ParseEmail([]byte(raw), nil)
	if err != nil {
		t.Fatalf("parse email: %v", err)
	}
	if parsed.BodyText != "Hello World" {
		t.Fatalf("expected html stripped, got %q", parsed.BodyText)
	}
}

func TestParseEmailAttachments(t *testing.T) {
	raw := strings.Join([]string{
		"From: Alice <alice@example.com>",
		"To: Bob <bob@example.com>",
		"Subject: Attach",
		"Date: Mon, 02 Jan 2006 15:04:05 -0700",
		"Content-Type: multipart/mixed; boundary=BOUNDARY",
		"",
		"--BOUNDARY",
		"Content-Type: text/plain; charset=utf-8",
		"",
		"Body text",
		"--BOUNDARY",
		"Content-Type: text/plain; name=note.txt",
		"Content-Disposition: attachment; filename=note.txt",
		"Content-Transfer-Encoding: base64",
		"",
		"aGVsbG8=",
		"--BOUNDARY--",
	}, "\r\n")

	sink := func(meta AttachmentMeta, content io.Reader) (StoredAttachment, error) {
		data, err := io.ReadAll(content)
		if err != nil {
			return StoredAttachment{}, err
		}
		return StoredAttachment{
			Filename:  meta.Filename,
			MimeType:  meta.MimeType,
			SizeBytes: int64(len(data)),
			SHA256:    "",
		}, nil
	}

	parsed, err := ParseEmail([]byte(raw), sink)
	if err != nil {
		t.Fatalf("parse email: %v", err)
	}
	if !parsed.HasAttachments {
		t.Fatalf("expected attachments")
	}
	if len(parsed.Attachments) != 1 {
		t.Fatalf("expected 1 attachment, got %d", len(parsed.Attachments))
	}
	att := parsed.Attachments[0]
	if att.Filename != "note.txt" {
		t.Fatalf("expected filename note.txt, got %q", att.Filename)
	}
	if att.SizeBytes != 5 {
		t.Fatalf("expected size 5, got %d", att.SizeBytes)
	}
}
