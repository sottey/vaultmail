package importer

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"net/textproto"
	"strings"
	"time"

	"golang.org/x/net/html"
	"golang.org/x/text/encoding/charmap"
)

type AttachmentMeta struct {
	Filename string
	MimeType string
}

type StoredAttachment struct {
	Filename   string
	MimeType   string
	SizeBytes  int64
	SHA256     string
	StoredPath string
}

type AttachmentSink func(meta AttachmentMeta, content io.Reader) (StoredAttachment, error)

type ParsedEmail struct {
	MessageID      string
	DateUTC        time.Time
	FromName       string
	FromEmail      string
	ToName         string
	ToEmail        string
	Subject        string
	BodyText       string
	HTMLText       string
	Snippet        string
	HasAttachments bool
	Attachments    []StoredAttachment
}

func ParseEmail(raw []byte, sink AttachmentSink) (ParsedEmail, error) {
	msg, err := readMessage(bytes.NewReader(raw))
	if err != nil {
		return ParsedEmail{}, err
	}

	headers := msg.Header
	messageID := strings.TrimSpace(headers.Get("Message-ID"))
	subject := decodeHeader(headers.Get("Subject"))
	fromName, fromEmail := parseFrom(headers.Get("From"))
	toName, toEmail := parseTo(headers.Get("To"))
	dateUTC := parseDate(headers.Get("Date"))

	plainText, htmlText, attachments, err := extractParts(headers, msg.Body, sink)
	if err != nil {
		return ParsedEmail{}, err
	}

	bodyText := plainText
	if bodyText == "" {
		bodyText = htmlToText(htmlText)
	}
	bodyText = normalizeWhitespace(bodyText)
	snippet := truncateRunes(bodyText, 200)

	return ParsedEmail{
		MessageID:      messageID,
		DateUTC:        dateUTC,
		FromName:       fromName,
		FromEmail:      fromEmail,
		ToName:         toName,
		ToEmail:        toEmail,
		Subject:        subject,
		BodyText:       bodyText,
		HTMLText:       htmlText,
		Snippet:        snippet,
		HasAttachments: len(attachments) > 0,
		Attachments:    attachments,
	}, nil
}

func ParseEmailWithFallback(raw []byte, sink AttachmentSink) (ParsedEmail, error) {
	parsed, err := ParseEmail(raw, sink)
	if err == nil {
		return parsed, nil
	}
	return parseEmailFallback(raw), err
}

func parseEmailFallback(raw []byte) ParsedEmail {
	headers, body := splitHeadersBody(raw)

	messageID := strings.TrimSpace(headers["message-id"])
	subject := decodeHeader(headers["subject"])
	fromName, fromEmail := parseFrom(headers["from"])
	toName, toEmail := parseTo(headers["to"])
	dateUTC := parseDate(headers["date"])

	bodyText := string(body)
	htmlText := ""
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(headers["content-type"])), "text/html") {
		htmlText = bodyText
		bodyText = htmlToText(bodyText)
	}
	bodyText = normalizeWhitespace(bodyText)
	snippet := truncateRunes(bodyText, 200)

	return ParsedEmail{
		MessageID:      messageID,
		DateUTC:        dateUTC,
		FromName:       fromName,
		FromEmail:      fromEmail,
		ToName:         toName,
		ToEmail:        toEmail,
		Subject:        subject,
		BodyText:       bodyText,
		HTMLText:       htmlText,
		Snippet:        snippet,
		HasAttachments: false,
		Attachments:    nil,
	}
}

func splitHeadersBody(raw []byte) (map[string]string, []byte) {
	headerMap := map[string]string{}
	sep := []byte("\r\n\r\n")
	idx := bytes.Index(raw, sep)
	if idx < 0 {
		sep = []byte("\n\n")
		idx = bytes.Index(raw, sep)
	}

	var headerBytes []byte
	var body []byte
	if idx >= 0 {
		headerBytes = raw[:idx]
		body = raw[idx+len(sep):]
	} else {
		headerBytes = raw
		body = nil
	}

	lines := bytes.Split(headerBytes, []byte{'\n'})
	var currentKey string
	for _, line := range lines {
		line = bytes.TrimRight(line, "\r")
		if len(line) == 0 {
			continue
		}
		if line[0] == ' ' || line[0] == '\t' {
			if currentKey != "" {
				headerMap[currentKey] = headerMap[currentKey] + " " + strings.TrimSpace(string(line))
			}
			continue
		}
		parts := bytes.SplitN(line, []byte{':'}, 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(string(parts[0])))
		value := strings.TrimSpace(string(parts[1]))
		if key != "" {
			headerMap[key] = value
			currentKey = key
		}
	}

	return headerMap, body
}

const headerBufferSize = 1024 * 1024

func readMessage(r io.Reader) (*mail.Message, error) {
	br := bufio.NewReaderSize(r, headerBufferSize)
	tp := textproto.NewReader(br)
	hdr, err := tp.ReadMIMEHeader()
	if err != nil {
		return nil, err
	}
	return &mail.Message{
		Header: mail.Header(hdr),
		Body:   br,
	}, nil
}

func extractParts(headers mail.Header, body io.Reader, sink AttachmentSink) (string, string, []StoredAttachment, error) {
	contentType := headers.Get("Content-Type")
	mediaType, params, _ := mime.ParseMediaType(contentType)
	if strings.HasPrefix(strings.ToLower(mediaType), "multipart/") {
		boundary := params["boundary"]
		if boundary == "" {
			return "", "", nil, fmt.Errorf("multipart without boundary")
		}
		mr := multipart.NewReader(body, boundary)
		state := &bodyState{}
		for {
			part, err := mr.NextPart()
			if err != nil {
				if errors.Is(err, io.EOF) {
					break
				}
				return "", "", nil, err
			}
			if err := walkPart(part.Header, part, sink, state); err != nil {
				return "", "", nil, err
			}
		}
		return state.plainText, state.htmlText, state.attachments, nil
	}

	state := &bodyState{}
	hdr := textproto.MIMEHeader(headers)
	if err := walkPart(hdr, body, sink, state); err != nil {
		return "", "", nil, err
	}
	return state.plainText, state.htmlText, state.attachments, nil
}

type bodyState struct {
	plainText   string
	htmlText    string
	attachments []StoredAttachment
}

func walkPart(headers textproto.MIMEHeader, body io.Reader, sink AttachmentSink, state *bodyState) error {
	contentType := headers.Get("Content-Type")
	mediaType, params, _ := mime.ParseMediaType(contentType)
	if mediaType == "" {
		mediaType = "text/plain"
	}

	if strings.HasPrefix(strings.ToLower(mediaType), "multipart/") {
		boundary := params["boundary"]
		if boundary == "" {
			return fmt.Errorf("multipart without boundary")
		}
		mr := multipart.NewReader(body, boundary)
		for {
			part, err := mr.NextPart()
			if err != nil {
				if errors.Is(err, io.EOF) {
					break
				}
				return err
			}
			if err := walkPart(part.Header, part, sink, state); err != nil {
				return err
			}
		}
		return nil
	}

	disposition, dispParams, _ := mime.ParseMediaType(headers.Get("Content-Disposition"))
	filename := strings.TrimSpace(dispParams["filename"])
	if filename == "" {
		if _, params, err := mime.ParseMediaType(contentType); err == nil {
			filename = strings.TrimSpace(params["name"])
		}
	}

	isAttachment := false
	if strings.EqualFold(disposition, "attachment") {
		isAttachment = true
	} else if filename != "" {
		isAttachment = true
	}

	cte := strings.ToLower(strings.TrimSpace(headers.Get("Content-Transfer-Encoding")))
	decoded := decodeTransferEncoding(cte, body)

	if isAttachment {
		if sink == nil {
			return nil
		}
		stored, err := sink(AttachmentMeta{Filename: filename, MimeType: mediaType}, decoded)
		if err != nil {
			if errors.Is(err, ErrBadAttachment) {
				return nil
			}
			return err
		}
		state.attachments = append(state.attachments, stored)
		return nil
	}

	switch strings.ToLower(mediaType) {
	case "text/plain":
		if state.plainText != "" {
			return nil
		}
		data, err := io.ReadAll(decoded)
		if err != nil {
			if errors.Is(err, ErrBadAttachment) {
				return nil
			}
			return err
		}
		charset := strings.ToLower(strings.TrimSpace(params["charset"]))
		state.plainText = decodeText(data, charset)
	case "text/html":
		if state.htmlText != "" {
			return nil
		}
		data, err := io.ReadAll(decoded)
		if err != nil {
			if errors.Is(err, ErrBadAttachment) {
				return nil
			}
			return err
		}
		charset := strings.ToLower(strings.TrimSpace(params["charset"]))
		state.htmlText = decodeText(data, charset)
	default:
		return nil
	}

	return nil
}

func decodeTransferEncoding(encoding string, r io.Reader) io.Reader {
	switch strings.ToLower(encoding) {
	case "base64":
		return base64.NewDecoder(base64.StdEncoding, r)
	case "quoted-printable":
		return quotedprintable.NewReader(r)
	default:
		return r
	}
}

func decodeHeader(value string) string {
	decoder := mime.WordDecoder{
		CharsetReader: charsetReader,
	}
	decoded, err := decoder.DecodeHeader(value)
	if err != nil {
		return value
	}
	return decoded
}

func parseFrom(value string) (string, string) {
	decoded := decodeHeader(value)
	addr, err := mail.ParseAddress(decoded)
	if err != nil {
		return "", ""
	}
	return addr.Name, addr.Address
}

func parseTo(value string) (string, string) {
	decoded := decodeHeader(value)
	list, err := mail.ParseAddressList(decoded)
	if err != nil || len(list) == 0 {
		return "", ""
	}
	var names []string
	var emails []string
	for _, addr := range list {
		if addr.Name != "" {
			names = append(names, addr.Name)
		}
		if addr.Address != "" {
			emails = append(emails, addr.Address)
		}
	}
	return strings.Join(names, ", "), strings.Join(emails, ", ")
}

func parseDate(value string) time.Time {
	parsed, err := mail.ParseDate(value)
	if err != nil {
		return time.Time{}
	}
	return parsed.UTC()
}

func decodeText(data []byte, charset string) string {
	if charset == "" || charset == "utf-8" || charset == "utf8" {
		return string(data)
	}
	if charset == "iso-8859-1" || charset == "latin1" {
		decoded, err := charmap.ISO8859_1.NewDecoder().Bytes(data)
		if err == nil {
			return string(decoded)
		}
	}
	return string(data)
}

func charsetReader(charset string, input io.Reader) (io.Reader, error) {
	lower := strings.ToLower(strings.TrimSpace(charset))
	if lower == "iso-8859-1" || lower == "latin1" {
		return charmap.ISO8859_1.NewDecoder().Reader(input), nil
	}
	if lower == "utf-8" || lower == "utf8" {
		return input, nil
	}
	return input, nil
}

func htmlToText(htmlSource string) string {
	if htmlSource == "" {
		return ""
	}

	node, err := html.Parse(strings.NewReader(htmlSource))
	if err != nil {
		return htmlSource
	}

	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			switch n.Data {
			case "script", "style", "noscript":
				return
			}
		}
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
			b.WriteString(" ")
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(node)

	return b.String()
}

func normalizeWhitespace(value string) string {
	fields := strings.Fields(value)
	if len(fields) == 0 {
		return ""
	}
	return strings.Join(fields, " ")
}

func truncateRunes(value string, max int) string {
	runes := []rune(value)
	if len(runes) <= max {
		return value
	}
	return string(runes[:max])
}
