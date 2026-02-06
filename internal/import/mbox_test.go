package importer

import (
	"bytes"
	"io"
	"strings"
	"testing"
)

func TestMboxReaderSplitsMessages(t *testing.T) {
	mbox := strings.Join([]string{
		"From sender1 Fri Jan  1 00:00:00 -0700 2021",
		"Header: one",
		"",
		"Body one",
		">From escaped",
		"From notasep",
		"",
		"From sender2 Fri Jan  2 00:00:00 -0700 2021",
		"Header: two",
		"",
		"Body two",
		"",
	}, "\n")

	reader := NewMboxReader(strings.NewReader(mbox))
	msg1, err := reader.Next()
	if err != nil {
		t.Fatalf("read first message: %v", err)
	}
	msg2, err := reader.Next()
	if err != nil {
		t.Fatalf("read second message: %v", err)
	}
	if _, err := reader.Next(); err != io.EOF {
		t.Fatalf("expected EOF, got %v", err)
	}

	if bytes.HasPrefix(msg1, []byte("From ")) {
		t.Fatalf("first message should not include separator")
	}
	if !bytes.Contains(msg1, []byte("Header: one")) {
		t.Fatalf("first message missing header")
	}
	if !bytes.Contains(msg1, []byte("Body one")) {
		t.Fatalf("first message missing body")
	}
	if !bytes.Contains(msg1, []byte(">From escaped")) {
		t.Fatalf("first message missing escaped From line")
	}
	if !bytes.Contains(msg1, []byte("From notasep")) {
		t.Fatalf("first message missing From-not-separator line")
	}
	if !bytes.Contains(msg2, []byte("Header: two")) {
		t.Fatalf("second message missing header")
	}
	if !bytes.Contains(msg2, []byte("Body two")) {
		t.Fatalf("second message missing body")
	}
}
