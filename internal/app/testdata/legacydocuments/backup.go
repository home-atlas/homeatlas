package documents

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/home-atlas/homeatlas/internal/core/access"
	"github.com/home-atlas/homeatlas/internal/core/backup"
)

var backupKey = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
var errInvalidBackup = backup.Invalid
var errBackupConflict = backup.Conflict
var errBackupOriginal = backup.OriginalUnavailable

type backupDocument struct {
	id, owner, title, category, created, description, tags, filename, mediaType, digest, key string
	size                                                                                     int64
}
type backupShare struct{ document, user, member string }
type documentSnapshot struct {
	documents   []backupDocument
	shares      []backupShare
	unavailable map[string]bool
}

type documentBackup struct {
	dataDir   string
	directory access.Directory
}

func NewBackup(dataDir string, directory access.Directory) backup.Provider {
	return &documentBackup{filepath.Join(dataDir, "documents"), directory}
}
func (*documentBackup) ID() string          { return "documents" }
func (*documentBackup) Version() int        { return 1 }
func (*documentBackup) MaxFileBytes() int64 { return MaxFileSize }
func (*documentBackup) Tables() []string {
	return []string{
		`CREATE TABLE documents (id TEXT PRIMARY KEY, owner_id TEXT NOT NULL, title TEXT NOT NULL, category TEXT NOT NULL, created_at TEXT NOT NULL, description TEXT NOT NULL, tags TEXT NOT NULL, original_filename TEXT NOT NULL, media_type TEXT NOT NULL, size_bytes INTEGER NOT NULL, sha256 TEXT NOT NULL, file_key TEXT NOT NULL)`,
		`CREATE TABLE shares (document_id TEXT NOT NULL, user_id TEXT NOT NULL, member_id TEXT NOT NULL, PRIMARY KEY(document_id,user_id))`,
	}
}
func (*documentBackup) Present(ctx context.Context, q backup.Queryer) (bool, error) {
	var count int
	err := q.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_schema WHERE type='table' AND name IN ('documents','document_shares')").Scan(&count)
	if err == nil && count != 0 && count != 2 {
		err = fmt.Errorf("incomplete Documents schema")
	}
	return count == 2, err
}
func (*documentBackup) Empty(ctx context.Context, q backup.Queryer) (bool, error) {
	var occupied bool
	err := q.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM documents)").Scan(&occupied)
	return !occupied, err
}
func readDocumentSnapshot(ctx context.Context, q backup.Queryer, live bool, household string) (documentSnapshot, error) {
	s := documentSnapshot{unavailable: map[string]bool{}}
	docSQL := `SELECT id,owner_id,title,category,created_at,description,tags,original_filename,media_type,size_bytes,sha256,file_key FROM documents`
	shareSQL := "SELECT document_id,user_id,member_id FROM shares"
	var args []any
	if live {
		docSQL += " WHERE household_id=?"
		shareSQL = "SELECT document_id,user_id,member_id FROM document_shares WHERE household_id=?"
		args = []any{household}
	}
	rows, err := q.QueryContext(ctx, docSQL, args...)
	if err != nil {
		return s, err
	}
	for rows.Next() {
		var d backupDocument
		err = rows.Scan(&d.id, &d.owner, &d.title, &d.category, &d.created, &d.description, &d.tags, &d.filename, &d.mediaType, &d.size, &d.digest, &d.key)
		if err != nil {
			break
		}
		s.documents = append(s.documents, d)
		if len(s.documents) > 10000 {
			err = errInvalidBackup
			break
		}
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return s, err
	}
	rows, err = q.QueryContext(ctx, shareSQL, args...)
	if err != nil {
		return s, err
	}
	for rows.Next() {
		var v backupShare
		err = rows.Scan(&v.document, &v.user, &v.member)
		if err != nil {
			break
		}
		s.shares = append(s.shares, v)
		if len(s.shares) > 100000 {
			err = errInvalidBackup
			break
		}
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return s, err
	}
	for _, d := range s.documents {
		if d.key == "" && d.size > 0 {
			s.unavailable[d.id] = true
		}
	}
	return s, nil
}
func (b *documentBackup) Export(ctx context.Context, q backup.Queryer, out *sql.Tx, household string, recoverable bool) error {
	s, err := readDocumentSnapshot(ctx, q, true, household)
	if err != nil {
		return err
	}
	if recoverable {
		for i, d := range s.documents {
			if err := ctx.Err(); err != nil {
				return err
			}
			if d.key == "" {
				continue
			}
			src, openErr := b.OpenFile(d.key)
			intact := false
			if openErr == nil {
				info, statErr := src.Stat()
				if statErr == nil && info.Mode().IsRegular() && info.Size() == d.size {
					hash := sha256.New()
					n, copyErr := io.Copy(hash, io.LimitReader(src, MaxFileSize+1))
					intact = copyErr == nil && n == d.size && hex.EncodeToString(hash.Sum(nil)) == d.digest
				}
				backup.DiscardCache(src)
				src.Close()
			}
			if !intact {
				s.unavailable[d.id] = true
				s.documents[i].key = ""
			}
		}
	} else if len(s.unavailable) > 0 {
		return errBackupOriginal
	}
	if err := validateDocumentSnapshot(s); err != nil {
		return err
	}
	for _, d := range s.documents {
		if _, err := out.ExecContext(ctx, "INSERT INTO documents VALUES(?,?,?,?,?,?,?,?,?,?,?,?)", d.id, d.owner, d.title, d.category, d.created, d.description, d.tags, d.filename, d.mediaType, d.size, d.digest, d.key); err != nil {
			return err
		}
	}
	for _, v := range s.shares {
		if _, err := out.ExecContext(ctx, "INSERT INTO shares VALUES(?,?,?)", v.document, v.user, v.member); err != nil {
			return err
		}
	}
	return nil
}
func (*documentBackup) Inspect(ctx context.Context, q backup.Queryer, household string) (backup.Contents, error) {
	s, err := readDocumentSnapshot(ctx, q, false, household)
	if err != nil {
		return backup.Contents{}, err
	}
	if err := validateDocumentSnapshot(s); err != nil {
		return backup.Contents{}, err
	}
	contents := backup.Contents{Records: len(s.documents)}
	for id := range s.unavailable {
		contents.Unavailable = append(contents.Unavailable, id)
	}
	for _, d := range s.documents {
		if d.key != "" {
			contents.Files = append(contents.Files, backup.File{Key: d.key, SHA256: d.digest, Size: d.size})
		}
	}
	return contents, nil
}
func (b *documentBackup) OpenFile(key string) (*os.File, error) {
	if !backupKey.MatchString(key) {
		return nil, errInvalidBackup
	}
	root, err := os.OpenRoot(b.dataDir)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	return root.Open(key)
}
func (b *documentBackup) Prepare(ctx context.Context, q backup.Queryer, dir, household string) (backup.Plan, error) {
	s, err := readDocumentSnapshot(ctx, q, false, household)
	if err != nil {
		return nil, err
	}
	files, err := os.OpenRoot(filepath.Join(b.dataDir))
	if err != nil {
		return nil, err
	}
	plan := &documentRestore{files: files, snapshot: s, directory: b.directory}
	ready := false
	defer func() {
		if !ready {
			plan.Finish(false, false)
		}
	}()
	for i, d := range s.documents {
		if d.key == "" {
			continue
		}
		src, err := os.Open(filepath.Join(dir, d.key))
		if err != nil {
			return nil, err
		}
		key := rand.Text()
		dst, err := files.OpenFile(key, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			src.Close()
			return nil, err
		}
		plan.created = append(plan.created, key)
		_, err = io.Copy(dst, src)
		backup.DiscardCache(src)
		src.Close()
		syncErr := dst.Sync()
		backup.DiscardCache(dst)
		closeErr := dst.Close()
		if err != nil || syncErr != nil || closeErr != nil {
			return nil, fmt.Errorf("write restored original: %v %v %v", err, syncErr, closeErr)
		}
		s.documents[i].key = key
	}
	if err = syncDirectory(filepath.Join(b.dataDir)); err != nil {
		return nil, err
	}
	plan.snapshot = s
	ready = true
	return plan, nil
}

type documentRestore struct {
	files             *os.Root
	snapshot          documentSnapshot
	directory         access.Directory
	created, obsolete []string
}

func (p *documentRestore) Apply(ctx context.Context, tx *sql.Tx, rc backup.RestoreContext) error {
	s := p.snapshot
	var err error
	oldRows, err := tx.QueryContext(ctx, "SELECT file_key FROM documents WHERE household_id=? AND file_key!=''", rc.HouseholdID)
	if err != nil {
		return err
	}
	for oldRows.Next() {
		var key string
		if err = oldRows.Scan(&key); err != nil {
			break
		}
		if backupKey.MatchString(key) {
			p.obsolete = append(p.obsolete, key)
		}
	}
	if err == nil {
		err = oldRows.Err()
	}
	oldRows.Close()
	if err != nil {
		return err
	}
	// Current identity and authorization always win over history: never import accounts or resurrect revoked grants.
	activeShares := map[string]bool{}
	rows, err := tx.QueryContext(ctx, "SELECT document_id,user_id,member_id FROM document_shares WHERE household_id=?", rc.HouseholdID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var v backupShare
		if err = rows.Scan(&v.document, &v.user, &v.member); err != nil {
			break
		}
		activeShares[v.document+"/"+v.user+"/"+v.member] = true
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return err
	}
	for _, d := range s.documents {
		var other string
		err = tx.QueryRowContext(ctx, "SELECT household_id FROM documents WHERE id=?", d.id).Scan(&other)
		if err != nil && err != sql.ErrNoRows {
			return err
		}
		if err == nil && other != rc.HouseholdID {
			return errBackupConflict
		}
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM documents WHERE household_id=?", rc.HouseholdID); err != nil {
		return err
	}
	for _, d := range s.documents {
		_, err = tx.ExecContext(ctx, `INSERT INTO documents(id,household_id,owner_id,title,category,created_at,description,tags,original_filename,media_type,size_bytes,sha256,file_key) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`, d.id, rc.HouseholdID, d.owner, d.title, d.category, d.created, d.description, d.tags, d.filename, d.mediaType, d.size, d.digest, d.key)
		if err != nil {
			return err
		}
	}
	sourceMemberships := rc.RecoveryMemberships
	for _, v := range s.shares {
		if rc.RecoveryMemberships == nil && !activeShares[v.document+"/"+v.user+"/"+v.member] {
			continue
		}
		m, memberErr := p.directory.Member(ctx, tx, rc.HouseholdID, v.user)
		if errors.Is(memberErr, sql.ErrNoRows) {
			continue
		}
		if memberErr != nil {
			return memberErr
		}
		if rc.RecoveryMemberships != nil {
			if sourceMemberships[v.user] != v.member {
				continue
			}
			v.member = m.MembershipID
		}
		if m.MembershipID != v.member || !slices.Contains(m.Permissions, Read) {
			continue
		}
		if _, err = tx.ExecContext(ctx, "INSERT INTO document_shares(document_id,household_id,user_id,member_id) VALUES(?,?,?,?)", v.document, rc.HouseholdID, v.user, v.member); err != nil {
			return err
		}
	}

	return nil
}
func (p *documentRestore) Finish(committed, keepOld bool) {
	defer p.files.Close()
	keys := p.created
	if committed {
		if keepOld {
			return
		}
		keys = p.obsolete
	}
	for _, key := range keys {
		if err := p.files.Remove(key); err != nil && !errors.Is(err, os.ErrNotExist) {
			slog.Warn("remove private backup original", "error", err)
		}
	}
}
func syncDirectory(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}
func backupText(v string, n int) bool {
	if !utf8.ValidString(v) || utf8.RuneCountInString(v) > n {
		return false
	}
	for _, c := range v {
		if unicode.IsControl(c) {
			return false
		}
	}
	return true
}
func validateDocumentSnapshot(s documentSnapshot) error {
	ids := map[string]bool{}
	unavailableCount := 0
	for _, d := range s.documents {
		if !backupKey.MatchString(d.id) || !backupKey.MatchString(d.owner) || ids[d.id] || !backupText(d.title, 160) || strings.TrimSpace(d.title) == "" || len(d.description) > 8000 || !utf8.ValidString(d.description) {
			return errInvalidBackup
		}
		ids[d.id] = true
		var tags []string
		if json.Unmarshal([]byte(d.tags), &tags) != nil || len(tags) > 10 {
			return errInvalidBackup
		}
		for _, tag := range tags {
			if !backupText(tag, 40) {
				return errInvalidBackup
			}
		}
		if !slices.Contains([]string{"manuales", "recibos", "contratos", "garantias", "otros"}, d.category) {
			return errInvalidBackup
		}
		if _, err := time.Parse(time.RFC3339Nano, d.created); err != nil {
			return errInvalidBackup
		}
		if d.key != "" || d.size > 0 || d.digest != "" {
			digest, err := hex.DecodeString(d.digest)
			if err != nil || len(digest) != 32 || (d.key != "" && !backupKey.MatchString(d.key)) || d.size <= 0 || d.size > MaxFileSize || !backupText(d.filename, 180) || d.filename == "" || strings.ContainsAny(d.filename, "/\\:") || !slices.Contains([]string{"application/pdf", "image/png", "image/jpeg", "text/plain"}, d.mediaType) {
				return errInvalidBackup
			}
			if d.key == "" {
				if !s.unavailable[d.id] {
					return errInvalidBackup
				}
				unavailableCount++
			} else if s.unavailable[d.id] {
				return errInvalidBackup
			}
		}
	}
	if unavailableCount != len(s.unavailable) {
		return errInvalidBackup
	}
	for _, v := range s.shares {
		if !ids[v.document] || !backupKey.MatchString(v.user) || !backupKey.MatchString(v.member) {
			return errInvalidBackup
		}
	}
	return nil
}
