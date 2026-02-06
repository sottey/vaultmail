package importer

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/sottey/vaultmail/internal/vault"
)

type Result struct {
	Imported   int
	Skipped    int
	Errors     int
	Attachment int
}

type Options struct {
	Progress       bool
	Interval       time.Duration
	ErrorLog       bool
	ErrorLogPath   string
	ErrorLogTopK   int
	ErrorLogFormat ErrorLogFormat
}

type Importer struct {
	Vault     *vault.Vault
	BatchSize int
	Options   Options
}

func ImportMbox(vaultDir, mboxPath string) (Result, error) {
	return ImportMboxWithOptions(vaultDir, mboxPath, Options{
		Progress:       true,
		Interval:       2 * time.Second,
		ErrorLog:       true,
		ErrorLogTopK:   3,
		ErrorLogFormat: ErrorLogJSONL,
	})
}

func ImportMboxWithOptions(vaultDir, mboxPath string, opts Options) (Result, error) {
	v, err := vault.Open(vaultDir)
	if err != nil {
		return Result{}, err
	}
	defer v.Close()

	imp := &Importer{Vault: v, BatchSize: 500, Options: opts}
	return imp.Run(mboxPath)
}

func (imp *Importer) Run(mboxPath string) (Result, error) {
	if imp == nil || imp.Vault == nil {
		return Result{}, fmt.Errorf("vault is required")
	}

	file, err := os.Open(mboxPath)
	if err != nil {
		return Result{}, err
	}
	defer file.Close()

	var totalSize int64
	if info, err := file.Stat(); err == nil {
		totalSize = info.Size()
	}

	if err := applyImportPragmas(imp.Vault.DB); err != nil {
		return Result{}, err
	}

	batchID, err := startBatch(imp.Vault.DB, filepath.Base(mboxPath))
	if err != nil {
		return Result{}, err
	}

	var errLogger *errorLogger
	if imp.Options.ErrorLog {
		logger, err := newErrorLogger(imp.Vault.Root, batchID, imp.Options)
		if err != nil {
			return Result{}, err
		}
		errLogger = logger
		defer errLogger.Close()
	}

	counter := &countingReader{r: file}
	reader := NewMboxReader(counter)
	result := Result{}
	processed := 0
	lastLog := time.Now()
	interval := imp.Options.Interval
	if interval <= 0 {
		interval = 2 * time.Second
	}

	tx, stmts, err := beginImportTx(imp.Vault.DB)
	if err != nil {
		return Result{}, err
	}
	defer func() {
		_ = stmts.close()
		_ = tx.Rollback()
	}()

	messageIndex := 0
	for {
		startOffset := counter.Count()
		raw, err := reader.Next()
		endOffset := counter.Count()
		if err == io.EOF {
			break
		}
		if err != nil {
			result.Errors++
			if errLogger != nil {
				errLogger.Log(ErrorMeta{
					MessageIndex: messageIndex,
					ByteStart:    startOffset,
					ByteEnd:      endOffset,
				}, err)
			}
			continue
		}

		if len(raw) == 0 {
			continue
		}

		messageIndex++
		meta := ErrorMeta{
			MessageIndex: messageIndex,
			ByteStart:    startOffset,
			ByteEnd:      endOffset,
		}
		err = imp.importMessage(tx, stmts, batchID, raw, &result, &meta, errLogger)
		if err != nil {
			result.Errors++
			if errLogger != nil {
				errLogger.Log(meta, err)
			}
			continue
		}

		processed++
		if imp.Options.Progress && time.Since(lastLog) >= interval {
			logProgress(result, counter.Count(), totalSize)
			lastLog = time.Now()
		}
		if imp.BatchSize > 0 && processed%imp.BatchSize == 0 {
			if err := stmts.close(); err != nil {
				return result, err
			}
			if err := tx.Commit(); err != nil {
				return result, err
			}
			tx, stmts, err = beginImportTx(imp.Vault.DB)
			if err != nil {
				return result, err
			}
		}
	}

	if err := stmts.close(); err != nil {
		return result, err
	}
	if err := tx.Commit(); err != nil {
		return result, err
	}

	if imp.Options.Progress {
		logProgress(result, counter.Count(), totalSize)
	}

	if err := finishBatch(imp.Vault.DB, batchID, result.Imported, result.Errors); err != nil {
		return result, err
	}

	if errLogger != nil {
		errLogger.PrintSummary()
	}

	return result, nil
}

func (imp *Importer) importMessage(tx *sql.Tx, stmts *importStatements, batchID int64, raw []byte, result *Result, meta *ErrorMeta, errLogger *errorLogger) error {
	if result == nil {
		return fmt.Errorf("result is required")
	}

	if _, err := tx.Exec("SAVEPOINT msg"); err != nil {
		return err
	}

	parsed, parseErr := ParseEmailWithFallback(raw, drainAttachment)
	if meta != nil {
		meta.MessageID = parsed.MessageID
		meta.Subject = parsed.Subject
		meta.From = formatFromText(parsed.FromName, parsed.FromEmail)
		meta.DateUTC = parsed.DateUTC
	}
	if parseErr != nil {
		result.Errors++
		if errLogger != nil && meta != nil {
			errLogger.Log(*meta, parseErr)
		}
	}

	contentHash := computeContentHash(parsed.FromEmail, parsed.DateUTC, parsed.Subject, parsed.BodyText, int64(len(raw)))

	if parsed.MessageID != "" {
		exists, err := messageIDExists(tx, stmts, parsed.MessageID)
		if err != nil {
			_, _ = tx.Exec("ROLLBACK TO msg")
			_, _ = tx.Exec("RELEASE msg")
			return err
		}
		if exists {
			result.Skipped++
			_, _ = tx.Exec("ROLLBACK TO msg")
			_, _ = tx.Exec("RELEASE msg")
			return nil
		}
	}
	exists, err := contentHashExists(tx, stmts, contentHash)
	if err != nil {
		_, _ = tx.Exec("ROLLBACK TO msg")
		_, _ = tx.Exec("RELEASE msg")
		return err
	}
	if exists {
		result.Skipped++
		_, _ = tx.Exec("ROLLBACK TO msg")
		_, _ = tx.Exec("RELEASE msg")
		return nil
	}

	storedAttachments := []StoredAttachment{}
	if parseErr == nil {
		_, err := ParseEmail(raw, func(meta AttachmentMeta, content io.Reader) (StoredAttachment, error) {
			stored, err := imp.storeAttachment(meta, content)
			if err == nil {
				storedAttachments = append(storedAttachments, stored)
			}
			return stored, err
		})
		if err != nil {
			_, _ = tx.Exec("ROLLBACK TO msg")
			_, _ = tx.Exec("RELEASE msg")
			return err
		}
	}

	emlHash := sha256Hex(raw)
	emlRel := vault.EmlRelPath(emlHash)
	if _, err := imp.Vault.WriteBlob(emlRel, bytesReader(raw)); err != nil {
		_, _ = tx.Exec("ROLLBACK TO msg")
		_, _ = tx.Exec("RELEASE msg")
		return err
	}

	createdUTC := time.Now().UTC()
	dateUTC := parsed.DateUTC
	if dateUTC.IsZero() {
		dateUTC = createdUTC
	}

	parseFailed := parseErr != nil
	res, err := stmts.insertMessage.Exec(
		nullableString(parsed.MessageID),
		dateUTC.Unix(),
		nullableString(parsed.FromName),
		nullableString(parsed.FromEmail),
		nullableString(parsed.ToName),
		nullableString(parsed.ToEmail),
		nullableString(parsed.Subject),
		nullableString(parsed.Snippet),
		emlRel,
		len(raw),
		boolToInt(len(storedAttachments) > 0),
		boolToInt(parseFailed),
		batchID,
		createdUTC.Unix(),
	)
	if err != nil {
		_, _ = tx.Exec("ROLLBACK TO msg")
		_, _ = tx.Exec("RELEASE msg")
		return err
	}

	msgID, err := res.LastInsertId()
	if err != nil {
		_, _ = tx.Exec("ROLLBACK TO msg")
		_, _ = tx.Exec("RELEASE msg")
		return err
	}

	if _, err := stmts.insertDedupe.Exec(msgID, contentHash); err != nil {
		_, _ = tx.Exec("ROLLBACK TO msg")
		_, _ = tx.Exec("RELEASE msg")
		return err
	}

	if _, err := stmts.insertFts.Exec(parsed.Subject, formatFromText(parsed.FromName, parsed.FromEmail), parsed.BodyText); err != nil {
		_, _ = tx.Exec("ROLLBACK TO msg")
		_, _ = tx.Exec("RELEASE msg")
		return err
	}
	if _, err := stmts.insertFtsMap.Exec(msgID); err != nil {
		_, _ = tx.Exec("ROLLBACK TO msg")
		_, _ = tx.Exec("RELEASE msg")
		return err
	}

	for _, att := range storedAttachments {
		if _, err := stmts.insertAttachment.Exec(
			msgID,
			nullableString(att.Filename),
			nullableString(att.MimeType),
			att.SizeBytes,
			att.SHA256,
			att.StoredPath,
		); err != nil {
			_, _ = tx.Exec("ROLLBACK TO msg")
			_, _ = tx.Exec("RELEASE msg")
			return err
		}
		result.Attachment++
	}

	if _, err := tx.Exec("RELEASE msg"); err != nil {
		return err
	}

	result.Imported++
	return nil
}

func computeContentHash(fromEmail string, dateUTC time.Time, subject string, bodyText string, sizeBytes int64) string {
	payload := strings.Join([]string{
		strings.TrimSpace(fromEmail),
		fmt.Sprintf("%d", dateUTC.Unix()),
		strings.TrimSpace(subject),
		truncateRunes(bodyText, 8192),
		fmt.Sprintf("%d", sizeBytes),
	}, "|")
	return sha256Hex([]byte(payload))
}

func sha256Hex(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

func formatFromText(name, email string) string {
	name = strings.TrimSpace(name)
	email = strings.TrimSpace(email)
	if name == "" {
		return email
	}
	if email == "" {
		return name
	}
	return fmt.Sprintf("%s <%s>", name, email)
}

func bytesReader(data []byte) io.Reader {
	return bytes.NewReader(data)
}

func nullableString(value string) interface{} {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return value
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

type ErrorLogFormat int

const (
	ErrorLogJSONL ErrorLogFormat = iota
)

type ErrorMeta struct {
	MessageIndex int
	ByteStart    int64
	ByteEnd      int64
	MessageID    string
	Subject      string
	From         string
	DateUTC      time.Time
}

type errorLogger struct {
	file   *os.File
	format ErrorLogFormat
	counts map[string]int
	topK   int
}

func newErrorLogger(vaultRoot string, batchID int64, opts Options) (*errorLogger, error) {
	path := opts.ErrorLogPath
	if path == "" {
		dir := filepath.Join(vaultRoot, "import-errors")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
		path = filepath.Join(dir, fmt.Sprintf("batch-%d.jsonl", batchID))
	} else {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return nil, err
		}
	}

	file, err := os.Create(path)
	if err != nil {
		return nil, err
	}

	topK := opts.ErrorLogTopK
	if topK <= 0 {
		topK = 3
	}

	return &errorLogger{
		file:   file,
		format: opts.ErrorLogFormat,
		counts: map[string]int{},
		topK:   topK,
	}, nil
}

func (el *errorLogger) Log(meta ErrorMeta, err error) {
	if el == nil || el.file == nil || err == nil {
		return
	}

	errText := err.Error()
	el.counts[errText]++

	switch el.format {
	case ErrorLogJSONL:
		payload := map[string]interface{}{
			"type":          "error",
			"message_index": meta.MessageIndex,
			"byte_start":    meta.ByteStart,
			"byte_end":      meta.ByteEnd,
			"message_id":    meta.MessageID,
			"from":          meta.From,
			"subject":       meta.Subject,
			"date_utc":      meta.DateUTC.UTC().Format(time.RFC3339),
			"error":         errText,
		}
		data, _ := json.Marshal(payload)
		_, _ = el.file.Write(append(data, '\n'))
	}
}

func (el *errorLogger) PrintSummary() {
	if el == nil || len(el.counts) == 0 {
		return
	}

	type entry struct {
		err   string
		count int
	}
	entries := make([]entry, 0, len(el.counts))
	for errText, count := range el.counts {
		entries = append(entries, entry{err: errText, count: count})
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].count == entries[j].count {
			return entries[i].err < entries[j].err
		}
		return entries[i].count > entries[j].count
	})

	limit := el.topK
	if limit > len(entries) {
		limit = len(entries)
	}
	fmt.Printf("Top %d error reasons:\n", limit)
	for i := 0; i < limit; i++ {
		fmt.Printf("%d. %s (x%d)\n", i+1, entries[i].err, entries[i].count)
	}
}

func (el *errorLogger) Close() {
	if el == nil || el.file == nil {
		return
	}
	_ = el.file.Close()
}

type countingReader struct {
	r     io.Reader
	count int64
}

func (cr *countingReader) Read(p []byte) (int, error) {
	n, err := cr.r.Read(p)
	if n > 0 {
		cr.count += int64(n)
	}
	return n, err
}

func (cr *countingReader) Count() int64 {
	if cr == nil {
		return 0
	}
	return cr.count
}

func logProgress(result Result, bytesRead, total int64) {
	if total > 0 {
		pct := float64(bytesRead) / float64(total) * 100
		fmt.Printf("Progress: %s/%s (%.1f%%) Imported=%d Skipped=%d Errors=%d Attachments=%d\n",
			humanBytes(bytesRead),
			humanBytes(total),
			pct,
			result.Imported,
			result.Skipped,
			result.Errors,
			result.Attachment,
		)
		return
	}

	fmt.Printf("Progress: %s Imported=%d Skipped=%d Errors=%d Attachments=%d\n",
		humanBytes(bytesRead),
		result.Imported,
		result.Skipped,
		result.Errors,
		result.Attachment,
	)
}

func humanBytes(value int64) string {
	const unit = 1024
	if value < unit {
		return fmt.Sprintf("%d B", value)
	}
	div, exp := int64(unit), 0
	for n := value / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(value)/float64(div), "KMGTPE"[exp])
}

var ErrBadAttachment = errors.New("bad attachment encoding")

func drainAttachment(meta AttachmentMeta, content io.Reader) (StoredAttachment, error) {
	n, err := io.Copy(io.Discard, content)
	if err != nil {
		if isDecodeError(err) {
			return StoredAttachment{}, ErrBadAttachment
		}
		return StoredAttachment{}, err
	}
	return StoredAttachment{
		Filename:  meta.Filename,
		MimeType:  meta.MimeType,
		SizeBytes: n,
	}, nil
}

type importStatements struct {
	insertMessage    *sql.Stmt
	insertDedupe     *sql.Stmt
	insertAttachment *sql.Stmt
	insertFts        *sql.Stmt
	insertFtsMap     *sql.Stmt
	selectMessageID  *sql.Stmt
	selectDedupe     *sql.Stmt
}

func (s *importStatements) close() error {
	var err error
	if s == nil {
		return nil
	}
	stmts := []*sql.Stmt{s.insertMessage, s.insertDedupe, s.insertAttachment, s.insertFts, s.insertFtsMap, s.selectMessageID, s.selectDedupe}
	for _, stmt := range stmts {
		if stmt == nil {
			continue
		}
		if closeErr := stmt.Close(); closeErr != nil && err == nil {
			err = closeErr
		}
	}
	return err
}

func beginImportTx(db *sql.DB) (*sql.Tx, *importStatements, error) {
	tx, err := db.Begin()
	if err != nil {
		return nil, nil, err
	}

	stmts := &importStatements{}
	if stmts.insertMessage, err = tx.Prepare(`
		INSERT INTO messages (
			message_id, date_utc, from_name, from_email, to_name, to_email, subject, snippet, eml_path,
			size_bytes, has_attachments, parse_failed, import_batch_id, created_utc
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`); err != nil {
		_ = tx.Rollback()
		return nil, nil, err
	}

	if stmts.insertDedupe, err = tx.Prepare(`INSERT INTO message_dedupe (message_fk, content_hash) VALUES (?, ?)`); err != nil {
		_ = tx.Rollback()
		return nil, nil, err
	}

	if stmts.insertAttachment, err = tx.Prepare(`
		INSERT INTO attachments (message_fk, filename, mime_type, size_bytes, sha256, stored_path)
		VALUES (?, ?, ?, ?, ?, ?)
	`); err != nil {
		_ = tx.Rollback()
		return nil, nil, err
	}

	if stmts.insertFts, err = tx.Prepare(`INSERT INTO messages_fts (subject, from_text, body_text) VALUES (?, ?, ?)`); err != nil {
		_ = tx.Rollback()
		return nil, nil, err
	}

	if stmts.insertFtsMap, err = tx.Prepare(`INSERT INTO messages_fts_map (message_fk, fts_rowid) VALUES (?, last_insert_rowid())`); err != nil {
		_ = tx.Rollback()
		return nil, nil, err
	}

	if stmts.selectMessageID, err = tx.Prepare(`SELECT id FROM messages WHERE message_id = ? LIMIT 1`); err != nil {
		_ = tx.Rollback()
		return nil, nil, err
	}

	if stmts.selectDedupe, err = tx.Prepare(`SELECT message_fk FROM message_dedupe WHERE content_hash = ? LIMIT 1`); err != nil {
		_ = tx.Rollback()
		return nil, nil, err
	}

	return tx, stmts, nil
}

func messageIDExists(tx *sql.Tx, stmts *importStatements, messageID string) (bool, error) {
	if messageID == "" {
		return false, nil
	}
	row := stmts.selectMessageID.QueryRow(messageID)
	var id int64
	if err := row.Scan(&id); err != nil {
		if err == sql.ErrNoRows {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func contentHashExists(tx *sql.Tx, stmts *importStatements, hash string) (bool, error) {
	row := stmts.selectDedupe.QueryRow(hash)
	var id int64
	if err := row.Scan(&id); err != nil {
		if err == sql.ErrNoRows {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func startBatch(db *sql.DB, filename string) (int64, error) {
	res, err := db.Exec(`INSERT INTO import_batches (filename, started_utc) VALUES (?, ?)`, filename, time.Now().UTC().Unix())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func finishBatch(db *sql.DB, batchID int64, imported int, errorsCount int) error {
	_, err := db.Exec(`
		UPDATE import_batches
		SET finished_utc = ?, messages_imported = ?, errors_count = ?
		WHERE id = ?
	`, time.Now().UTC().Unix(), imported, errorsCount, batchID)
	return err
}

func applyImportPragmas(db *sql.DB) error {
	pragmas := []string{
		"PRAGMA journal_mode=WAL;",
		"PRAGMA synchronous=NORMAL;",
		"PRAGMA temp_store=MEMORY;",
	}
	for _, pragma := range pragmas {
		if _, err := db.Exec(pragma); err != nil {
			return err
		}
	}
	return nil
}

func (imp *Importer) storeAttachment(meta AttachmentMeta, content io.Reader) (StoredAttachment, error) {
	if imp == nil || imp.Vault == nil {
		return StoredAttachment{}, fmt.Errorf("vault is required")
	}

	tmpDir := filepath.Join(imp.Vault.Root, "blobs", "att", "tmp")
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		return StoredAttachment{}, err
	}

	tmpFile, err := os.CreateTemp(tmpDir, "att-*")
	if err != nil {
		return StoredAttachment{}, err
	}

	hash := sha256.New()
	size, copyErr := io.Copy(io.MultiWriter(tmpFile, hash), content)
	closeErr := tmpFile.Close()
	if copyErr != nil {
		_ = os.Remove(tmpFile.Name())
		if isDecodeError(copyErr) {
			return StoredAttachment{}, ErrBadAttachment
		}
		return StoredAttachment{}, copyErr
	}
	if closeErr != nil {
		_ = os.Remove(tmpFile.Name())
		return StoredAttachment{}, closeErr
	}

	sha := hex.EncodeToString(hash.Sum(nil))
	storedPath := vault.AttachmentRelPath(sha, filepath.Ext(meta.Filename))
	finalAbs := imp.Vault.AbsPath(storedPath)

	if st, err := os.Stat(finalAbs); err == nil {
		_ = os.Remove(tmpFile.Name())
		size = st.Size()
	} else {
		if err := os.MkdirAll(filepath.Dir(finalAbs), 0o755); err != nil {
			_ = os.Remove(tmpFile.Name())
			return StoredAttachment{}, err
		}
		if err := os.Rename(tmpFile.Name(), finalAbs); err != nil {
			_ = os.Remove(tmpFile.Name())
			return StoredAttachment{}, err
		}
	}

	return StoredAttachment{
		Filename:   meta.Filename,
		MimeType:   meta.MimeType,
		SizeBytes:  size,
		SHA256:     sha,
		StoredPath: storedPath,
	}, nil
}

func isDecodeError(err error) bool {
	if err == nil {
		return false
	}
	var corrupt base64.CorruptInputError
	if errors.As(err, &corrupt) {
		return true
	}
	if errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	return false
}
