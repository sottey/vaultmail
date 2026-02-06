package importer

import (
	"bufio"
	"bytes"
	"io"
	"strings"
	"time"
)

type MboxReader struct {
	r       *bufio.Reader
	pending []byte
	eof     bool
}

func NewMboxReader(r io.Reader) *MboxReader {
	return &MboxReader{r: bufio.NewReader(r)}
}

func (mr *MboxReader) Next() ([]byte, error) {
	if mr.eof {
		return nil, io.EOF
	}

	var buf bytes.Buffer
	if len(mr.pending) > 0 {
		buf.Write(mr.pending)
		mr.pending = nil
	}

	for {
		line, err := mr.readLine()
		if err == io.EOF {
			mr.eof = true
			if buf.Len() == 0 {
				return nil, io.EOF
			}
			return buf.Bytes(), nil
		}
		if err != nil {
			return nil, err
		}

		if isSeparator(line) {
			nextLine, nerr := mr.readLine()
			if nerr == io.EOF {
				mr.eof = true
				if buf.Len() == 0 {
					return nil, io.EOF
				}
				return buf.Bytes(), nil
			}
			if nerr != nil {
				return nil, nerr
			}

			if looksLikeHeader(nextLine) {
				if buf.Len() == 0 {
					buf.Write(nextLine)
					continue
				}

				mr.pending = nextLine
				return buf.Bytes(), nil
			}

			buf.Write(line)
			buf.Write(nextLine)
			continue
		}

		buf.Write(line)
	}
}

func (mr *MboxReader) readLine() ([]byte, error) {
	var buf bytes.Buffer
	for {
		line, err := mr.r.ReadSlice('\n')
		if len(line) > 0 {
			buf.Write(line)
		}
		if err == nil {
			return buf.Bytes(), nil
		}
		if err == bufio.ErrBufferFull {
			continue
		}
		if err == io.EOF {
			return buf.Bytes(), io.EOF
		}
		return buf.Bytes(), err
	}
}

func isSeparator(line []byte) bool {
	if bytes.HasPrefix(line, []byte(">From ")) {
		return false
	}
	if !bytes.HasPrefix(line, []byte("From ")) {
		return false
	}
	return looksLikeMboxFromLine(line)
}

func looksLikeMboxFromLine(line []byte) bool {
	text := strings.TrimRight(string(line), "\r\n")
	fields := strings.Fields(text)
	if len(fields) < 5 {
		return false
	}
	if len(fields) >= 6 {
		lastSix := strings.Join(fields[len(fields)-6:], " ")
		if _, err := time.Parse("Mon Jan 2 15:04:05 -0700 2006", lastSix); err == nil {
			return true
		}
		if _, err := time.Parse("Mon Jan 2 15:04:05 2006", lastSix); err == nil {
			return true
		}
	}
	lastFive := strings.Join(fields[len(fields)-5:], " ")
	if _, err := time.Parse("Jan 2 15:04:05 -0700 2006", lastFive); err == nil {
		return true
	}
	if _, err := time.Parse("Jan 2 15:04:05 2006", lastFive); err == nil {
		return true
	}
	return false
}

func looksLikeHeader(line []byte) bool {
	line = bytes.TrimRight(line, "\r\n")
	if len(line) == 0 {
		return false
	}
	if line[0] == ' ' || line[0] == '\t' {
		return false
	}

	for i, b := range line {
		switch b {
		case ':':
			return i > 0
		case ' ', '\t':
			return false
		}
	}

	return false
}
