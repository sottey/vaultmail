package web

import (
	"context"
	"database/sql"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	importer "github.com/sottey/vaultmail/internal/import"
	"github.com/sottey/vaultmail/internal/vault"
)

type App struct {
	Vault     *vault.Vault
	IndexTmpl *template.Template
	MsgTmpl   *template.Template
}

type MessageRow struct {
	ID             int64
	DateUTC        time.Time
	FromName       string
	FromEmail      string
	ToName         string
	ToEmail        string
	Subject        string
	Snippet        string
	HasAttachments bool
	ParseFailed    bool
}

type AttachmentRow struct {
	ID         int64
	Filename   string
	MimeType   string
	SizeBytes  int64
	StoredPath string
}

type ListView struct {
	Messages   []MessageRow
	Query      string
	Page       int
	TotalPages int
	TotalCount int
}

type MessageView struct {
	Message     MessageRow
	BodyText    string
	Attachments []AttachmentRow
	ParseFailed bool
	BackURL     string
}

func NewApp(v *vault.Vault) (*App, error) {
	if v == nil {
		return nil, fmt.Errorf("vault is required")
	}
	server, err := NewServer()
	if err != nil {
		return nil, err
	}
	return &App{Vault: v, IndexTmpl: server.Index, MsgTmpl: server.Message}, nil
}

func (a *App) Router() chi.Router {
	r := chi.NewRouter()
	r.Get("/", a.handleIndex)
	r.Get("/message/{id}", a.handleMessage)
	r.Get("/attachment/{id}", a.handleAttachmentView)
	r.Get("/attachment/{id}/download", a.handleAttachment)
	return r
}

func (a *App) handleIndex(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	parsed := parseQuery(q)
	page := parseIntDefault(r.URL.Query().Get("page"), 1)
	if page < 1 {
		page = 1
	}
	limit := 50
	offset := (page - 1) * limit

	filter := messageFilter{
		Query:              parsed.FTSQuery,
		RawQuery:           parsed.RawText,
		SubjectQuery:       parsed.SubjectQuery,
		FromQuery:          parsed.FromQuery,
		ToQuery:            parsed.ToQuery,
		MinAttachmentBytes: parsed.MinAttachmentBytes,
		FromDate:           parsed.FromDate,
		ToDate:             parsed.ToDate,
		HasAttachments:     parsed.HasAttachments,
		Limit:              limit,
		Offset:             offset,
	}

	messages, total, err := a.queryMessages(r.Context(), filter)
	if err != nil {
		writeError(w, err)
		return
	}

	totalPages := total / limit
	if total%limit != 0 {
		totalPages++
	}
	if totalPages == 0 {
		totalPages = 1
	}

	view := ListView{
		Messages:   messages,
		Query:      q,
		Page:       page,
		TotalPages: totalPages,
		TotalCount: total,
	}

	a.render(w, a.IndexTmpl, "index.html", view)
}

func (a *App) handleMessage(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	messageID, err := strconv.ParseInt(id, 10, 64)
	if err != nil {
		writeError(w, fmt.Errorf("invalid message id"))
		return
	}

	msg, err := a.getMessage(r.Context(), messageID)
	if err != nil {
		writeError(w, err)
		return
	}

	emlPath := a.Vault.AbsPath(msg.EmlPath)
	emlData, err := os.ReadFile(emlPath)
	if err != nil {
		writeError(w, err)
		return
	}

	parsed, parseErr := importer.ParseEmailWithFallback(emlData, nil)
	parseFailed := msg.ParseFailed == 1
	if parseErr != nil {
		parseFailed = true
	}

	attachments, err := a.getAttachments(r.Context(), messageID)
	if err != nil {
		writeError(w, err)
		return
	}

	view := MessageView{
		Message:     msg.ToRow(),
		BodyText:    parsed.BodyText,
		Attachments: attachments,
		ParseFailed: parseFailed,
		BackURL:     buildBackURL(r),
	}

	a.render(w, a.MsgTmpl, "message.html", view)
}

func (a *App) handleAttachment(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	attID, err := strconv.ParseInt(id, 10, 64)
	if err != nil {
		writeError(w, fmt.Errorf("invalid attachment id"))
		return
	}

	att, err := a.getAttachment(r.Context(), attID)
	if err != nil {
		writeError(w, err)
		return
	}

	path := a.Vault.AbsPath(att.StoredPath)
	filename := att.Filename
	if filename == "" {
		filename = filepath.Base(att.StoredPath)
	}

	contentType := att.MimeType
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", sanitizeFilename(filename)))
	http.ServeFile(w, r, path)
}

func (a *App) handleAttachmentView(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	attID, err := strconv.ParseInt(id, 10, 64)
	if err != nil {
		writeError(w, fmt.Errorf("invalid attachment id"))
		return
	}

	att, err := a.getAttachment(r.Context(), attID)
	if err != nil {
		writeError(w, err)
		return
	}
	if !isPreviewable(att.MimeType) {
		http.Redirect(w, r, fmt.Sprintf("/attachment/%d/download", att.ID), http.StatusFound)
		return
	}

	path := a.Vault.AbsPath(att.StoredPath)
	filename := att.Filename
	if filename == "" {
		filename = filepath.Base(att.StoredPath)
	}

	contentType := att.MimeType
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", fmt.Sprintf("inline; filename=\"%s\"", sanitizeFilename(filename)))
	http.ServeFile(w, r, path)
}

func (a *App) render(w http.ResponseWriter, tmpl *template.Template, name string, data interface{}) {
	sw := &statusWriter{ResponseWriter: w}
	sw.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tmpl.ExecuteTemplate(sw, name, data); err != nil {
		if !sw.wroteHeader {
			writeError(sw, err)
		}
	}
}

func writeError(w http.ResponseWriter, err error) {
	http.Error(w, err.Error(), http.StatusInternalServerError)
}

type statusWriter struct {
	http.ResponseWriter
	wroteHeader bool
}

func (sw *statusWriter) WriteHeader(statusCode int) {
	sw.wroteHeader = true
	sw.ResponseWriter.WriteHeader(statusCode)
}

func parseIntDefault(value string, fallback int) int {
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func buildBackURL(r *http.Request) string {
	if r == nil {
		return "/"
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	page := strings.TrimSpace(r.URL.Query().Get("page"))
	if q == "" && page == "" {
		return "/"
	}
	values := url.Values{}
	if q != "" {
		values.Set("q", q)
	}
	if page != "" {
		values.Set("page", page)
	}
	if encoded := values.Encode(); encoded != "" {
		return "/?" + encoded
	}
	return "/"
}

func sanitizeFilename(name string) string {
	name = strings.ReplaceAll(name, "\n", " ")
	name = strings.ReplaceAll(name, "\r", " ")
	return name
}

func sanitizeFTSQuery(q string) string {
	q = strings.TrimSpace(q)
	if q == "" {
		return ""
	}
	tokens := tokenizeQuery(q)
	return buildFTSQuery(tokens)
}

func isPreviewable(mimeType string) bool {
	mimeType = strings.ToLower(strings.TrimSpace(mimeType))
	if strings.HasPrefix(mimeType, "image/") {
		return true
	}
	if mimeType == "application/pdf" {
		return true
	}
	return false
}

type parsedQuery struct {
	RawText            string
	FTSQuery           string
	FromDate           string
	ToDate             string
	HasAttachments     bool
	MinAttachmentBytes int64
	SubjectQuery       string
	FromQuery          string
	ToQuery            string
	BodyFTSQuery       string
}

func parseQuery(q string) parsedQuery {
	q = strings.TrimSpace(q)
	if q == "" {
		return parsedQuery{}
	}

	tokens := tokenizeQuery(q)
	var remaining []string
	out := parsedQuery{}

	for i := 0; i < len(tokens); i++ {
		tok := tokens[i]
		value := tok.Value
		lower := strings.ToLower(value)
		switch {
		case lower == "has:attachment" || lower == "has:attachments":
			out.HasAttachments = true
			continue
		case strings.HasPrefix(lower, "date:>="):
			out.FromDate = strings.TrimPrefix(lower, "date:>=")
			continue
		case strings.HasPrefix(lower, "date:>"):
			out.FromDate = strings.TrimPrefix(lower, "date:>")
			continue
		case strings.HasPrefix(lower, "date:<="):
			out.ToDate = strings.TrimPrefix(lower, "date:<=")
			continue
		case strings.HasPrefix(lower, "date:<"):
			out.ToDate = strings.TrimPrefix(lower, "date:<")
			continue
		case strings.HasPrefix(lower, "att:>="):
			if size, ok := parseSize(strings.TrimPrefix(lower, "att:>=")); ok {
				out.MinAttachmentBytes = size
				out.HasAttachments = true
				continue
			}
		case strings.HasPrefix(lower, "att:>"):
			if size, ok := parseSize(strings.TrimPrefix(lower, "att:>")); ok {
				out.MinAttachmentBytes = size
				out.HasAttachments = true
				continue
			}
		case strings.HasPrefix(lower, "att:"):
			if size, ok := parseSize(strings.TrimPrefix(lower, "att:")); ok {
				out.MinAttachmentBytes = size
				out.HasAttachments = true
				continue
			}
		case strings.HasPrefix(lower, "subject:"):
			out.SubjectQuery = strings.TrimSpace(value[len("subject:"):])
			continue
		case strings.HasPrefix(lower, "from:"):
			out.FromQuery = strings.TrimSpace(value[len("from:"):])
			continue
		case strings.HasPrefix(lower, "to:"):
			out.ToQuery = strings.TrimSpace(value[len("to:"):])
			continue
		case lower == "subject:":
			if i+1 < len(tokens) && tokens[i+1].Quoted {
				out.SubjectQuery = tokens[i+1].Value
				i++
				continue
			}
		case lower == "from:":
			if i+1 < len(tokens) && tokens[i+1].Quoted {
				out.FromQuery = tokens[i+1].Value
				i++
				continue
			}
		case lower == "to:":
			if i+1 < len(tokens) && tokens[i+1].Quoted {
				out.ToQuery = tokens[i+1].Value
				i++
				continue
			}
		case strings.HasPrefix(lower, "body:"):
			out.BodyFTSQuery = buildColumnFTSQuery("body_text", strings.TrimSpace(value[len("body:"):]))
			continue
		case lower == "body:":
			if i+1 < len(tokens) && tokens[i+1].Quoted {
				out.BodyFTSQuery = buildColumnFTSQuery("body_text", tokens[i+1].Value)
				i++
				continue
			}
		}

		remaining = append(remaining, value)
	}

	out.RawText = strings.Join(remaining, " ")
	out.FTSQuery = buildFTSQuery(tokensFromText(out.RawText))
	if out.BodyFTSQuery != "" {
		if out.FTSQuery == "" {
			out.FTSQuery = out.BodyFTSQuery
		} else {
			out.FTSQuery = out.FTSQuery + " " + out.BodyFTSQuery
		}
	}
	return out
}

func parseSize(value string) (int64, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, false
	}
	unit := value[len(value)-1]
	multiplier := int64(1)
	switch unit {
	case 'k', 'K':
		multiplier = 1024
		value = value[:len(value)-1]
	case 'm', 'M':
		multiplier = 1024 * 1024
		value = value[:len(value)-1]
	case 'g', 'G':
		multiplier = 1024 * 1024 * 1024
		value = value[:len(value)-1]
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed < 0 {
		return 0, false
	}
	return parsed * multiplier, true
}

type queryToken struct {
	Value  string
	Quoted bool
}

func tokenizeQuery(q string) []queryToken {
	var tokens []queryToken
	var buf strings.Builder
	inQuote := false
	for i := 0; i < len(q); i++ {
		ch := q[i]
		switch ch {
		case '"':
			if inQuote {
				tokens = appendToken(tokens, buf.String(), true)
				buf.Reset()
				inQuote = false
			} else {
				if buf.Len() > 0 {
					tokens = appendToken(tokens, buf.String(), false)
					buf.Reset()
				}
				inQuote = true
			}
		case ' ', '\t', '\n', '\r':
			if inQuote {
				buf.WriteByte(ch)
			} else {
				tokens = appendToken(tokens, buf.String(), false)
				buf.Reset()
			}
		default:
			buf.WriteByte(ch)
		}
	}
	if buf.Len() > 0 {
		tokens = appendToken(tokens, buf.String(), inQuote)
	}
	return tokens
}

func appendToken(tokens []queryToken, value string, quoted bool) []queryToken {
	value = strings.TrimSpace(value)
	if value == "" {
		return tokens
	}
	return append(tokens, queryToken{Value: value, Quoted: quoted})
}

func tokensFromText(text string) []queryToken {
	if text == "" {
		return nil
	}
	return tokenizeQuery(text)
}

func buildFTSQuery(tokens []queryToken) string {
	if len(tokens) == 0 {
		return ""
	}
	parts := make([]string, 0, len(tokens))
	for _, tok := range tokens {
		if tok.Value == "" {
			continue
		}
		escaped := strings.ReplaceAll(tok.Value, "\"", "\"\"")
		parts = append(parts, "\""+escaped+"\"")
	}
	return strings.Join(parts, " ")
}

func buildColumnFTSQuery(column string, value string) string {
	tokens := tokensFromText(value)
	if len(tokens) == 0 {
		return ""
	}
	parts := make([]string, 0, len(tokens))
	for _, tok := range tokens {
		if tok.Value == "" {
			continue
		}
		escaped := strings.ReplaceAll(tok.Value, "\"", "\"\"")
		parts = append(parts, column+":\""+escaped+"\"")
	}
	return strings.Join(parts, " ")
}

type messageFilter struct {
	Query              string
	RawQuery           string
	FromDate           string
	ToDate             string
	HasAttachments     bool
	MinAttachmentBytes int64
	SubjectQuery       string
	FromQuery          string
	ToQuery            string
	Limit              int
	Offset             int
}

type messageRecord struct {
	ID             int64
	DateUTC        int64
	FromName       sql.NullString
	FromEmail      sql.NullString
	ToName         sql.NullString
	ToEmail        sql.NullString
	Subject        sql.NullString
	Snippet        sql.NullString
	HasAttachments int
	ParseFailed    int
	EmlPath        string
}

func (m messageRecord) ToRow() MessageRow {
	return MessageRow{
		ID:             m.ID,
		DateUTC:        time.Unix(m.DateUTC, 0).UTC(),
		FromName:       m.FromName.String,
		FromEmail:      m.FromEmail.String,
		ToName:         m.ToName.String,
		ToEmail:        m.ToEmail.String,
		Subject:        m.Subject.String,
		Snippet:        m.Snippet.String,
		HasAttachments: m.HasAttachments == 1,
		ParseFailed:    m.ParseFailed == 1,
	}
}

func (a *App) queryMessages(ctx context.Context, filter messageFilter) ([]MessageRow, int, error) {
	var args []interface{}
	var where []string

	if filter.Query != "" {
		where = append(where, "messages_fts MATCH ?")
		args = append(args, filter.Query)
	}
	if filter.HasAttachments {
		where = append(where, "m.has_attachments = 1")
	}
	if filter.SubjectQuery != "" {
		where = append(where, "m.subject LIKE ?")
		args = append(args, "%"+filter.SubjectQuery+"%")
	}
	if filter.FromQuery != "" {
		where = append(where, "(m.from_email LIKE ? OR m.from_name LIKE ?)")
		args = append(args, "%"+filter.FromQuery+"%", "%"+filter.FromQuery+"%")
	}
	if filter.ToQuery != "" {
		where = append(where, "(m.to_email LIKE ? OR m.to_name LIKE ?)")
		args = append(args, "%"+filter.ToQuery+"%", "%"+filter.ToQuery+"%")
	}
	if filter.MinAttachmentBytes > 0 {
		where = append(where, "EXISTS (SELECT 1 FROM attachments a WHERE a.message_fk = m.id AND a.size_bytes >= ?)")
		args = append(args, filter.MinAttachmentBytes)
	}
	if filter.FromDate != "" {
		if ts, ok := parseDate(filter.FromDate, false); ok {
			where = append(where, "m.date_utc >= ?")
			args = append(args, ts)
		}
	}
	if filter.ToDate != "" {
		if ts, ok := parseDate(filter.ToDate, true); ok {
			where = append(where, "m.date_utc <= ?")
			args = append(args, ts)
		}
	}

	var rows *sql.Rows
	var err error
	var total int
	if filter.Query != "" {
		base := `
			FROM messages_fts
			JOIN messages_fts_map map ON map.fts_rowid = messages_fts.rowid
			JOIN messages m ON m.id = map.message_fk
		`
		whereSQL := buildWhere(where)
		countSQL := "SELECT COUNT(1) " + base + whereSQL
		if err := a.Vault.DB.QueryRowContext(ctx, countSQL, args...).Scan(&total); err != nil {
			return a.queryMessagesFallback(ctx, filter, err)
		}

		querySQL := `
			SELECT m.id, m.date_utc, m.from_name, m.from_email, m.to_name, m.to_email, m.subject, m.snippet, m.has_attachments, m.parse_failed, m.eml_path
			` + base + whereSQL + `
			ORDER BY m.date_utc DESC
			LIMIT ? OFFSET ?
		`
		argsPage := append(append([]interface{}{}, args...), filter.Limit, filter.Offset)
		rows, err = a.Vault.DB.QueryContext(ctx, querySQL, argsPage...)
		if err != nil {
			return a.queryMessagesFallback(ctx, filter, err)
		}
	} else {
		whereSQL := buildWhere(where)
		countSQL := "SELECT COUNT(1) FROM messages m" + whereSQL
		if err := a.Vault.DB.QueryRowContext(ctx, countSQL, args...).Scan(&total); err != nil {
			return nil, 0, err
		}

		querySQL := `
			SELECT m.id, m.date_utc, m.from_name, m.from_email, m.to_name, m.to_email, m.subject, m.snippet, m.has_attachments, m.parse_failed, m.eml_path
			FROM messages m
			` + whereSQL + `
			ORDER BY m.date_utc DESC
			LIMIT ? OFFSET ?
		`
		argsPage := append(append([]interface{}{}, args...), filter.Limit, filter.Offset)
		rows, err = a.Vault.DB.QueryContext(ctx, querySQL, argsPage...)
	}
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var result []MessageRow
	for rows.Next() {
		var rec messageRecord
		if err := rows.Scan(
			&rec.ID,
			&rec.DateUTC,
			&rec.FromName,
			&rec.FromEmail,
			&rec.ToName,
			&rec.ToEmail,
			&rec.Subject,
			&rec.Snippet,
			&rec.HasAttachments,
			&rec.ParseFailed,
			&rec.EmlPath,
		); err != nil {
			return nil, 0, err
		}
		result = append(result, rec.ToRow())
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}

	return result, total, nil
}

func (a *App) queryMessagesFallback(ctx context.Context, filter messageFilter, cause error) ([]MessageRow, int, error) {
	if filter.RawQuery == "" {
		return nil, 0, cause
	}

	var args []interface{}
	var where []string
	pattern := "%" + filter.RawQuery + "%"
	where = append(where, "(m.subject LIKE ? OR m.from_email LIKE ? OR m.from_name LIKE ? OR m.to_email LIKE ? OR m.to_name LIKE ? OR m.snippet LIKE ?)")
	args = append(args, pattern, pattern, pattern, pattern, pattern, pattern)
	if filter.HasAttachments {
		where = append(where, "m.has_attachments = 1")
	}
	if filter.SubjectQuery != "" {
		where = append(where, "m.subject LIKE ?")
		args = append(args, "%"+filter.SubjectQuery+"%")
	}
	if filter.FromQuery != "" {
		where = append(where, "(m.from_email LIKE ? OR m.from_name LIKE ?)")
		args = append(args, "%"+filter.FromQuery+"%", "%"+filter.FromQuery+"%")
	}
	if filter.ToQuery != "" {
		where = append(where, "(m.to_email LIKE ? OR m.to_name LIKE ?)")
		args = append(args, "%"+filter.ToQuery+"%", "%"+filter.ToQuery+"%")
	}
	if filter.MinAttachmentBytes > 0 {
		where = append(where, "EXISTS (SELECT 1 FROM attachments a WHERE a.message_fk = m.id AND a.size_bytes >= ?)")
		args = append(args, filter.MinAttachmentBytes)
	}
	if filter.FromDate != "" {
		if ts, ok := parseDate(filter.FromDate, false); ok {
			where = append(where, "m.date_utc >= ?")
			args = append(args, ts)
		}
	}
	if filter.ToDate != "" {
		if ts, ok := parseDate(filter.ToDate, true); ok {
			where = append(where, "m.date_utc <= ?")
			args = append(args, ts)
		}
	}

	whereSQL := buildWhere(where)
	countSQL := "SELECT COUNT(1) FROM messages m" + whereSQL
	var total int
	if err := a.Vault.DB.QueryRowContext(ctx, countSQL, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	querySQL := `
		SELECT m.id, m.date_utc, m.from_name, m.from_email, m.to_name, m.to_email, m.subject, m.snippet, m.has_attachments, m.parse_failed, m.eml_path
		FROM messages m
		` + whereSQL + `
		ORDER BY m.date_utc DESC
		LIMIT ? OFFSET ?
	`
	argsPage := append(append([]interface{}{}, args...), filter.Limit, filter.Offset)
	rows, err := a.Vault.DB.QueryContext(ctx, querySQL, argsPage...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var result []MessageRow
	for rows.Next() {
		var rec messageRecord
		if err := rows.Scan(
			&rec.ID,
			&rec.DateUTC,
			&rec.FromName,
			&rec.FromEmail,
			&rec.ToName,
			&rec.ToEmail,
			&rec.Subject,
			&rec.Snippet,
			&rec.HasAttachments,
			&rec.ParseFailed,
			&rec.EmlPath,
		); err != nil {
			return nil, 0, err
		}
		result = append(result, rec.ToRow())
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}

	return result, total, nil
}

func (a *App) getMessage(ctx context.Context, id int64) (messageRecord, error) {
	row := a.Vault.DB.QueryRowContext(ctx, `
		SELECT id, date_utc, from_name, from_email, to_name, to_email, subject, snippet, has_attachments, parse_failed, eml_path
		FROM messages
		WHERE id = ?
	`, id)

	var rec messageRecord
	if err := row.Scan(
		&rec.ID,
		&rec.DateUTC,
		&rec.FromName,
		&rec.FromEmail,
		&rec.ToName,
		&rec.ToEmail,
		&rec.Subject,
		&rec.Snippet,
		&rec.HasAttachments,
		&rec.ParseFailed,
		&rec.EmlPath,
	); err != nil {
		return messageRecord{}, err
	}
	return rec, nil
}

func (a *App) getAttachments(ctx context.Context, messageID int64) ([]AttachmentRow, error) {
	rows, err := a.Vault.DB.QueryContext(ctx, `
		SELECT id, filename, mime_type, size_bytes, stored_path
		FROM attachments
		WHERE message_fk = ?
		ORDER BY id ASC
	`, messageID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []AttachmentRow
	for rows.Next() {
		var att AttachmentRow
		if err := rows.Scan(&att.ID, &att.Filename, &att.MimeType, &att.SizeBytes, &att.StoredPath); err != nil {
			return nil, err
		}
		result = append(result, att)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

func (a *App) getAttachment(ctx context.Context, id int64) (AttachmentRow, error) {
	row := a.Vault.DB.QueryRowContext(ctx, `
		SELECT id, filename, mime_type, size_bytes, stored_path
		FROM attachments
		WHERE id = ?
	`, id)
	var att AttachmentRow
	if err := row.Scan(&att.ID, &att.Filename, &att.MimeType, &att.SizeBytes, &att.StoredPath); err != nil {
		return AttachmentRow{}, err
	}
	return att, nil
}

func buildWhere(parts []string) string {
	if len(parts) == 0 {
		return ""
	}
	return " WHERE " + strings.Join(parts, " AND ")
}

func parseDate(value string, endOfDay bool) (int64, bool) {
	if value == "" {
		return 0, false
	}
	parsed, err := time.Parse("2006-01-02", value)
	if err != nil {
		return 0, false
	}
	if endOfDay {
		parsed = parsed.Add(23*time.Hour + 59*time.Minute + 59*time.Second)
	}
	return parsed.UTC().Unix(), true
}
