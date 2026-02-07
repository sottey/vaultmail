package web

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
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
	LoginTmpl *template.Template
	Media     http.Handler
	authHash  string
	themes    []ThemeOption
	themeCSS  template.CSS
	rawHTML   bool
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
	Deleted        bool
}

type AttachmentRow struct {
	ID         int64
	Filename   string
	MimeType   string
	SizeBytes  int64
	StoredPath string
}

type ListView struct {
	Messages    []MessageRow
	Query       string
	Page        int
	PerPage     int
	TotalPages  int
	TotalCount  int
	AuthEnabled bool
	Themes      []ThemeOption
	ThemeCSS    template.CSS
}

type MessageView struct {
	Message     MessageRow
	BodyText    string
	BodyHTML    template.HTML
	Attachments []AttachmentRow
	ParseFailed bool
	BackURL     string
	AuthEnabled bool
	Themes      []ThemeOption
	ThemeCSS    template.CSS
	Query       string
	PerPage     int
}

type LoginView struct {
	Error       string
	Next        string
	AuthEnabled bool
	Themes      []ThemeOption
	ThemeCSS    template.CSS
	Query       string
	PerPage     int
}

func NewApp(v *vault.Vault, password string, themesDir string, rawHTML bool) (*App, error) {
	if v == nil {
		return nil, fmt.Errorf("vault is required")
	}
	server, err := NewServer()
	if err != nil {
		return nil, err
	}
	media, err := mediaFileServer()
	if err != nil {
		return nil, err
	}
	themeOptions := builtinThemes()
	themeCSS, extraThemes, err := loadThemes(themesDir)
	if err != nil {
		return nil, err
	}
	if len(extraThemes) > 0 {
		themeOptions = append(themeOptions, extraThemes...)
	}

	app := &App{
		Vault:     v,
		IndexTmpl: server.Index,
		MsgTmpl:   server.Message,
		LoginTmpl: server.Login,
		Media:     media,
		themes:    themeOptions,
		themeCSS:  themeCSS,
		rawHTML:   rawHTML,
	}
	app.authHash = hashPassword(password)
	return app, nil
}

func (a *App) Router() chi.Router {
	r := chi.NewRouter()
	if a.authEnabled() {
		r.Use(a.authMiddleware)
		r.Get("/login", a.handleLoginForm)
		r.Post("/login", a.handleLoginSubmit)
		r.Post("/logout", a.handleLogout)
	}
	if a.Media != nil {
		r.Handle("/media/*", http.StripPrefix("/media/", a.Media))
	}
	r.Get("/", a.handleIndex)
	r.Get("/message/{id}", a.handleMessage)
	r.Post("/message/{id}/archive", a.handleMessageArchive)
	r.Post("/message/{id}/unarchive", a.handleMessageUnarchive)
	r.Post("/messages/archive", a.handleBulkArchive)
	r.Get("/attachment/{id}", a.handleAttachmentView)
	r.Get("/attachment/{id}/download", a.handleAttachment)
	return r
}

func (a *App) handleIndex(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	parsed := parseQuery(q)
	page := parseIntDefault(r.URL.Query().Get("page"), 1)
	perPage := parsePerPage(r.URL.Query().Get("per_page"))
	if page < 1 {
		page = 1
	}
	limit := perPage
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
		DeletedOnly:        parsed.DeletedOnly,
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
		Messages:    messages,
		Query:       q,
		Page:        page,
		PerPage:     perPage,
		TotalPages:  totalPages,
		TotalCount:  total,
		AuthEnabled: a.authEnabled(),
		Themes:      a.themes,
		ThemeCSS:    a.themeCSS,
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
	bodyHTML := template.HTML("")
	if strings.TrimSpace(parsed.HTMLText) != "" {
		if a.rawHTML {
			bodyHTML = template.HTML(parsed.HTMLText)
		} else {
			bodyHTML = sanitizeHTML(parsed.HTMLText)
			if strings.TrimSpace(string(bodyHTML)) == "" {
				bodyHTML = template.HTML("")
			}
		}
	}

	attachments, err := a.getAttachments(r.Context(), messageID)
	if err != nil {
		writeError(w, err)
		return
	}

	view := MessageView{
		Message:     msg.ToRow(),
		BodyText:    parsed.BodyText,
		BodyHTML:    bodyHTML,
		Attachments: attachments,
		ParseFailed: parseFailed,
		BackURL:     buildBackURL(r),
		AuthEnabled: a.authEnabled(),
		Themes:      a.themes,
		ThemeCSS:    a.themeCSS,
		Query:       strings.TrimSpace(r.URL.Query().Get("q")),
		PerPage:     parsePerPage(r.URL.Query().Get("per_page")),
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

func (a *App) handleMessageArchive(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	messageID, err := strconv.ParseInt(id, 10, 64)
	if err != nil {
		writeError(w, fmt.Errorf("invalid message id"))
		return
	}
	if err := a.setMessageDeleted(r.Context(), messageID, true); err != nil {
		writeError(w, err)
		return
	}
	next := strings.TrimSpace(r.FormValue("next"))
	if next == "" {
		next = "/"
	}
	http.Redirect(w, r, next, http.StatusFound)
}

func (a *App) handleMessageUnarchive(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	messageID, err := strconv.ParseInt(id, 10, 64)
	if err != nil {
		writeError(w, fmt.Errorf("invalid message id"))
		return
	}
	if err := a.setMessageDeleted(r.Context(), messageID, false); err != nil {
		writeError(w, err)
		return
	}
	next := strings.TrimSpace(r.FormValue("next"))
	if next == "" {
		next = "/"
	}
	http.Redirect(w, r, next, http.StatusFound)
}

func (a *App) handleBulkArchive(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeError(w, err)
		return
	}
	ids := r.Form["ids"]
	messageIDs := make([]int64, 0, len(ids))
	for _, raw := range ids {
		id, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
		if err != nil || id <= 0 {
			continue
		}
		messageIDs = append(messageIDs, id)
	}
	if len(messageIDs) > 0 {
		if err := a.setMessagesDeleted(r.Context(), messageIDs, true); err != nil {
			writeError(w, err)
			return
		}
	}
	next := strings.TrimSpace(r.FormValue("next"))
	if next == "" {
		next = "/"
	}
	http.Redirect(w, r, next, http.StatusFound)
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

func (a *App) renderLogin(w http.ResponseWriter, view LoginView) {
	if a.LoginTmpl == nil {
		writeError(w, fmt.Errorf("login template unavailable"))
		return
	}
	a.render(w, a.LoginTmpl, "login.html", view)
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

func parsePerPage(value string) int {
	switch strings.TrimSpace(value) {
	case "25":
		return 25
	case "50":
		return 50
	case "100":
		return 100
	case "250":
		return 250
	case "500":
		return 500
	default:
		return 25
	}
}

func buildBackURL(r *http.Request) string {
	if r == nil {
		return "/"
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	page := strings.TrimSpace(r.URL.Query().Get("page"))
	perPage := strings.TrimSpace(r.URL.Query().Get("per_page"))
	if q == "" && page == "" && perPage == "" {
		return "/"
	}
	values := url.Values{}
	if q != "" {
		values.Set("q", q)
	}
	if page != "" {
		values.Set("page", page)
	}
	if perPage != "" {
		values.Set("per_page", perPage)
	}
	if encoded := values.Encode(); encoded != "" {
		return "/?" + encoded
	}
	return "/"
}

const authCookieName = "vaultmail_auth"

func (a *App) authEnabled() bool {
	return a != nil && a.authHash != ""
}

func (a *App) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !a.authEnabled() {
			next.ServeHTTP(w, r)
			return
		}
		if r.URL.Path == "/login" || strings.HasPrefix(r.URL.Path, "/media/") {
			next.ServeHTTP(w, r)
			return
		}
		if a.isAuthed(r) {
			next.ServeHTTP(w, r)
			return
		}
		http.Redirect(w, r, "/login?next="+url.QueryEscape(r.URL.RequestURI()), http.StatusFound)
	})
}

func (a *App) handleLoginForm(w http.ResponseWriter, r *http.Request) {
	next := strings.TrimSpace(r.URL.Query().Get("next"))
	a.renderLogin(w, LoginView{Next: next, AuthEnabled: a.authEnabled(), Themes: a.themes, ThemeCSS: a.themeCSS, PerPage: 25})
}

func (a *App) handleLoginSubmit(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeError(w, err)
		return
	}
	password := r.FormValue("password")
	next := strings.TrimSpace(r.FormValue("next"))
	if hashPassword(password) != a.authHash {
		a.renderLogin(w, LoginView{Error: "Invalid password", Next: next, AuthEnabled: a.authEnabled(), Themes: a.themes, ThemeCSS: a.themeCSS, PerPage: 25})
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     authCookieName,
		Value:    a.authHash,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   60 * 60 * 24 * 30,
	})

	if next == "" {
		next = "/"
	}
	http.Redirect(w, r, next, http.StatusFound)
}

func (a *App) handleLogout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     authCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
	http.Redirect(w, r, "/", http.StatusFound)
}

func (a *App) isAuthed(r *http.Request) bool {
	if r == nil || !a.authEnabled() {
		return true
	}
	cookie, err := r.Cookie(authCookieName)
	if err != nil {
		return false
	}
	return cookie.Value == a.authHash
}

func hashPassword(password string) string {
	password = strings.TrimSpace(password)
	if password == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(password))
	return hex.EncodeToString(sum[:])
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
	DeletedOnly        bool
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
		case lower == "archived:true" || lower == "archived:1":
			out.DeletedOnly = true
			continue
		case lower == "archived:false" || lower == "archived:0":
			out.DeletedOnly = false
			continue
		case lower == "deleted:true" || lower == "deleted:1":
			out.DeletedOnly = true
			continue
		case lower == "deleted:false" || lower == "deleted:0":
			out.DeletedOnly = false
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
	DeletedOnly        bool
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
	Deleted        int
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
		Deleted:        m.Deleted == 1,
	}
}

func (a *App) queryMessages(ctx context.Context, filter messageFilter) ([]MessageRow, int, error) {
	var args []interface{}
	var where []string

	if filter.Query != "" {
		where = append(where, "messages_fts MATCH ?")
		args = append(args, filter.Query)
	}
	if filter.DeletedOnly {
		where = append(where, "m.deleted = 1")
	} else {
		where = append(where, "m.deleted = 0")
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
			SELECT m.id, m.date_utc, m.from_name, m.from_email, m.to_name, m.to_email, m.subject, m.snippet, m.has_attachments, m.parse_failed, m.deleted, m.eml_path
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
			SELECT m.id, m.date_utc, m.from_name, m.from_email, m.to_name, m.to_email, m.subject, m.snippet, m.has_attachments, m.parse_failed, m.deleted, m.eml_path
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
			&rec.Deleted,
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
	if filter.DeletedOnly {
		where = append(where, "m.deleted = 1")
	} else {
		where = append(where, "m.deleted = 0")
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

	whereSQL := buildWhere(where)
	countSQL := "SELECT COUNT(1) FROM messages m" + whereSQL
	var total int
	if err := a.Vault.DB.QueryRowContext(ctx, countSQL, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	querySQL := `
		SELECT m.id, m.date_utc, m.from_name, m.from_email, m.to_name, m.to_email, m.subject, m.snippet, m.has_attachments, m.parse_failed, m.deleted, m.eml_path
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
			&rec.Deleted,
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
		SELECT id, date_utc, from_name, from_email, to_name, to_email, subject, snippet, has_attachments, parse_failed, deleted, eml_path
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
		&rec.Deleted,
		&rec.EmlPath,
	); err != nil {
		return messageRecord{}, err
	}
	return rec, nil
}

func (a *App) setMessageDeleted(ctx context.Context, id int64, deleted bool) error {
	value := 0
	if deleted {
		value = 1
	}
	_, err := a.Vault.DB.ExecContext(ctx, `UPDATE messages SET deleted = ? WHERE id = ?`, value, id)
	return err
}

func (a *App) setMessagesDeleted(ctx context.Context, ids []int64, deleted bool) error {
	if len(ids) == 0 {
		return nil
	}
	value := 0
	if deleted {
		value = 1
	}
	placeholders := make([]string, 0, len(ids))
	args := make([]interface{}, 0, len(ids)+1)
	args = append(args, value)
	for _, id := range ids {
		placeholders = append(placeholders, "?")
		args = append(args, id)
	}
	query := fmt.Sprintf("UPDATE messages SET deleted = ? WHERE id IN (%s)", strings.Join(placeholders, ","))
	_, err := a.Vault.DB.ExecContext(ctx, query, args...)
	return err
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
