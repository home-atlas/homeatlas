package collections

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"log/slog"
	"mime"
	"net/http"
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
	"github.com/home-atlas/homeatlas/internal/core/modules"
	"github.com/home-atlas/homeatlas/internal/core/objectstore"
)

const MaxFileSize int64 = 20 << 20
const metadataLimit = 16 << 10

var storageKey = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

type metadata struct {
	Title       string   `json:"title"`
	Category    string   `json:"category"`
	Description string   `json:"description"`
	Tags        []string `json:"tags"`
}

func (m *Module) parseMetadata(ctx context.Context, data []byte) (metadata, error) {
	var value metadata
	if len(data) > metadataLimit {
		return value, errors.New("metadata too large")
	}
	normalized, err := m.config.Validate(ctx, data)
	if err != nil {
		return value, fmt.Errorf("%w: %v", ErrValidatorUnavailable, err)
	}
	if len(normalized) > metadataLimit {
		return value, errors.New("invalid plugin metadata")
	}
	data = normalized
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return value, err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return value, errors.New("trailing data")
	}
	value.Title = strings.TrimSpace(value.Title)
	value.Description = strings.TrimSpace(value.Description)
	if !text(value.Title, m.config.TitleMaxLength, false) || value.Title == "" || !m.config.category(value.Category) || !text(value.Description, m.config.DescriptionMaxLength, true) || len(value.Tags) > m.config.MaxTags {
		return value, errors.New("invalid metadata")
	}
	tags := []string{}
	seen := map[string]bool{}
	for _, tag := range value.Tags {
		tag = strings.TrimSpace(tag)
		if tag == "" || !text(tag, m.config.TagMaxLength, false) {
			return value, errors.New("invalid tag")
		}
		key := strings.ToLower(tag)
		if !seen[key] {
			tags = append(tags, tag)
			seen[key] = true
		}
	}
	value.Tags = tags
	return value, nil
}

func text(value string, max int, multiline bool) bool {
	if !utf8.ValidString(value) || utf8.RuneCountInString(value) > max {
		return false
	}
	for _, char := range value {
		if unicode.IsControl(char) && !(multiline && (char == '\n' || char == '\r' || char == '\t')) {
			return false
		}
	}
	return true
}

func safeFilename(value string) bool {
	return value != "" && len(value) <= 255 && text(value, 180, false) && !strings.ContainsAny(value, "/\\:") && value == strings.TrimSpace(value)
}

func (m *Module) upload(w http.ResponseWriter, r *http.Request) {
	select {
	case m.uploads <- struct{}{}:
		defer func() { <-m.uploads }()
	default:
		w.Header().Set("Retry-After", "1")
		fail(w, 429, "uploads_busy")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, m.config.MaxFileBytes+(128<<10))
	reader, err := r.MultipartReader()
	if err != nil {
		fail(w, 415, "multipart_required")
		return
	}
	id := rand.Text()
	var file *os.File
	var raw []byte
	var filename string
	var size int64
	var checksum string
	// shortcut: a process crash can leave a private orphan; add reconciliation with backup/restore.
	kept := false
	var remote *objectstore.Pending
	defer func() {
		if remote != nil && !kept {
			remote.Abort()
		}
	}()
	defer func() {
		if file != nil {
			file.Close()
			if !kept || remote != nil {
				if err := m.files.Remove(id); err != nil && !errors.Is(err, os.ErrNotExist) {
					slog.Error("remove uncommitted item", "id", id, "error", err)
				}
			}
		}
	}()
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			uploadError(w, err)
			return
		}
		switch part.FormName() {
		case "metadata":
			if raw != nil || part.FileName() != "" {
				fail(w, 400, "invalid_upload")
				return
			}
			raw, err = io.ReadAll(io.LimitReader(part, metadataLimit+1))
			if err != nil {
				uploadError(w, err)
				return
			}
			if len(raw) > metadataLimit {
				fail(w, 413, "metadata_too_large")
				return
			}
		case "file":
			if file != nil {
				fail(w, 400, "invalid_upload")
				return
			}
			_, disposition, err := mime.ParseMediaType(part.Header.Get("Content-Disposition"))
			if err != nil || !safeFilename(disposition["filename"]) {
				fail(w, 400, "invalid_filename")
				return
			}
			filename = disposition["filename"]
			file, err = m.files.OpenFile(id, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
			if err != nil {
				fail(w, 500, "storage_unavailable")
				return
			}
			digest := sha256.New()
			size, err = io.Copy(io.MultiWriter(file, digest), io.LimitReader(part, m.config.MaxFileBytes+1))
			if err != nil {
				uploadError(w, err)
				return
			}
			if size > m.config.MaxFileBytes {
				fail(w, 413, "file_too_large")
				return
			}
			if size == 0 {
				fail(w, 400, "empty_file")
				return
			}
			checksum = fmt.Sprintf("%x", digest.Sum(nil))
		default:
			fail(w, 400, "invalid_upload")
			return
		}
		part.Close()
	}
	if file == nil || len(raw) == 0 {
		fail(w, 400, "invalid_upload")
		return
	}
	value, err := m.parseMetadata(r.Context(), raw)
	if err != nil {
		metadataError(w, err)
		return
	}
	mediaType, err := fileType(file, filename, size)
	if err != nil || !slices.Contains(m.config.MediaTypes, mediaType) {
		fail(w, 415, "unsupported_file_type")
		return
	}
	if err := file.Sync(); err != nil {
		fail(w, 500, "storage_unavailable")
		return
	}
	// Keep large collections from retaining every newly uploaded original in RAM.
	backup.DiscardCache(file)
	// Flush the directory entry before publishing its metadata in SQLite.
	directory, err := m.files.Open(".")
	if err != nil {
		fail(w, 500, "storage_unavailable")
		return
	}
	err = directory.Sync()
	directory.Close()
	if err != nil {
		fail(w, 500, "storage_unavailable")
		return
	}
	p, _ := access.Current(r.Context())
	if m.config.Storage != nil {
		remote, err = m.config.Storage.Upload(r.Context(), p.HouseholdID, m.config.ID, id, file, size, checksum)
		if err != nil {
			fail(w, 503, "storage_unavailable")
			return
		}
	}
	if err := file.Close(); err != nil {
		fail(w, 500, "storage_unavailable")
		return
	}
	tags, _ := json.Marshal(value.Tags)
	d := Item{OwnerID: p.UserID, ID: id, Title: value.Title, Category: value.Category, Description: value.Description, Tags: value.Tags, Filename: filename, MediaType: mediaType, Size: size, SHA256: checksum, HasFile: true, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	tx, err := m.db.BeginTx(r.Context(), nil)
	if err != nil {
		fail(w, 500, "items_unavailable")
		return
	}
	defer tx.Rollback()
	if err := m.authorizeMutation(r.Context(), tx.Tx, "write"); err != nil {
		fail(w, 403, "permission_denied")
		return
	}
	_, err = tx.ExecContext(r.Context(), `INSERT INTO collection_items
		(id,household_id,owner_id,title,category,created_at,description,tags,original_filename,media_type,size_bytes,sha256,file_key)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`, id, p.HouseholdID, p.UserID, d.Title, d.Category, d.CreatedAt, d.Description, string(tags), filename, mediaType, size, checksum, id)
	if err != nil {
		fail(w, 500, "items_unavailable")
		return
	}
	if remote != nil {
		if err := remote.Publish(r.Context(), tx.Tx); err != nil {
			fail(w, 500, "storage_unavailable")
			return
		}
	}
	if err := tx.Commit(); err != nil {
		fail(w, 500, "items_unavailable")
		return
	}
	kept = true
	w.Header().Set("Location", "/api/"+m.config.ID+"/"+id)
	modules.JSON(w, 201, d)
}

func fileType(file *os.File, filename string, size int64) (string, error) {
	var header [512]byte
	n, err := file.ReadAt(header[:], 0)
	if err != nil && err != io.EOF {
		return "", err
	}
	detected := strings.Split(http.DetectContentType(header[:n]), ";")[0]
	ext := strings.ToLower(filepath.Ext(filename))
	switch {
	case ext == ".pdf" && detected == "application/pdf":
		return detected, nil
	case (ext == ".jpg" || ext == ".jpeg") && detected == "image/jpeg", ext == ".png" && detected == "image/png":
		if _, err := file.Seek(0, io.SeekStart); err != nil {
			return "", err
		}
		config, _, err := image.DecodeConfig(file)
		if err != nil || config.Width < 1 || config.Height < 1 || config.Width > 25_000_000/config.Height {
			return "", errors.New("invalid image")
		}
		return detected, nil
	case ext == ".txt" && detected == "text/plain":
		if _, err := file.Seek(0, io.SeekStart); err != nil {
			return "", err
		}
		data, err := io.ReadAll(io.LimitReader(file, size))
		if err != nil || !utf8.Valid(data) {
			return "", errors.New("invalid text")
		}
		for _, char := range string(data) {
			if unicode.IsControl(char) && char != '\r' && char != '\n' && char != '\t' {
				return "", errors.New("invalid text")
			}
		}
		return detected, nil
	default:
		return "", errors.New("unsupported file")
	}
}

func (m *Module) lookup(r *http.Request, ownerOnly bool) (Item, string, error) {
	p, _ := access.Current(r.Context())
	id := r.PathValue("id")
	if !storageKey.MatchString(id) {
		return Item{}, "", sql.ErrNoRows
	}
	scope := readable
	args := readArgs(p)
	if ownerOnly {
		scope = "d.household_id=? AND d.owner_id=?"
		args = []any{p.HouseholdID, p.UserID}
	}
	d, err := scanItem(m.db.QueryRowContext(r.Context(), "SELECT "+columns+" FROM collection_items d WHERE d.id=? AND "+scope, append([]any{id}, args...)...))
	return d, d.fileKey, err
}
func (m *Module) owned(r *http.Request) (Item, string, error) { return m.lookup(r, true) }

func (m *Module) detail(w http.ResponseWriter, r *http.Request) {
	d, _, err := m.lookup(r, false)
	if err != nil {
		itemError(w, err)
		return
	}
	if m.directory != nil {
		p, _ := access.Current(r.Context())
		owner, err := m.directory.Member(r.Context(), nil, p.HouseholdID, d.OwnerID)
		if err == nil {
			d.OwnerName = owner.Name
		} else if !errors.Is(err, sql.ErrNoRows) {
			fail(w, 503, "members_unavailable")
			return
		}
	}
	modules.JSON(w, 200, d)
}

func (m *Module) update(w http.ResponseWriter, r *http.Request) {
	if _, _, err := m.owned(r); err != nil {
		itemError(w, err)
		return
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		fail(w, 415, "json_required")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, metadataLimit)
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		uploadError(w, err)
		return
	}
	value, err := m.parseMetadata(r.Context(), raw)
	if err != nil {
		metadataError(w, err)
		return
	}
	p, _ := access.Current(r.Context())
	tags, _ := json.Marshal(value.Tags)
	tx, err := m.db.BeginTx(r.Context(), nil)
	if err != nil {
		fail(w, 500, "items_unavailable")
		return
	}
	defer tx.Rollback()
	if err := m.authorizeMutation(r.Context(), tx.Tx, "write"); err != nil {
		fail(w, 403, "permission_denied")
		return
	}
	row := tx.QueryRowContext(r.Context(), `UPDATE collection_items SET title=?,category=?,description=?,tags=?
		WHERE id=? AND household_id=? AND owner_id=? RETURNING `+strings.ReplaceAll(columns, "d.", ""), value.Title, value.Category, value.Description, string(tags), r.PathValue("id"), p.HouseholdID, p.UserID)
	d, err := scanItem(row)
	if err != nil {
		itemError(w, err)
		return
	}
	if err := tx.Commit(); err != nil {
		fail(w, 500, "items_unavailable")
		return
	}
	modules.JSON(w, 200, d)
}

func (m *Module) download(w http.ResponseWriter, r *http.Request) {
	d, key, err := m.lookup(r, false)
	if err != nil {
		itemError(w, err)
		return
	}
	if !storageKey.MatchString(key) {
		fail(w, 404, "item_not_found")
		return
	}
	file, cleanup, err := m.openFile(r.Context(), key)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			fail(w, 404, "item_not_found")
		} else {
			fail(w, 503, "file_unavailable")
		}
		return
	}
	defer cleanup()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() != d.Size {
		fail(w, 409, "file_unavailable")
		return
	}
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": d.Filename}))
	w.Header().Set("Content-Type", d.MediaType)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'")
	defer backup.DiscardCache(file)
	http.ServeContent(w, r, d.Filename, time.Time{}, backup.StreamReader(file))
}

func itemError(w http.ResponseWriter, err error) {
	if errors.Is(err, sql.ErrNoRows) {
		fail(w, 404, "item_not_found")
	} else {
		fail(w, 500, "items_unavailable")
	}
}

func uploadError(w http.ResponseWriter, err error) {
	var limit *http.MaxBytesError
	if errors.As(err, &limit) {
		fail(w, 413, "file_too_large")
	} else if errors.As(err, new(*os.PathError)) {
		fail(w, 500, "storage_unavailable")
	} else {
		fail(w, 400, "invalid_upload")
	}
}

func fail(w http.ResponseWriter, status int, code string) {
	modules.JSON(w, status, map[string]string{"error": code})
}

var ErrValidatorUnavailable = errors.New("collection validator unavailable")

func metadataError(w http.ResponseWriter, err error) {
	if errors.Is(err, ErrValidatorUnavailable) {
		w.Header().Set("Retry-After", "1")
		fail(w, 503, "plugin_unavailable")
	} else {
		fail(w, 400, "invalid_metadata")
	}
}
func (m *Module) authorizeMutation(ctx context.Context, tx *sql.Tx, capability string) error {
	p, ok := access.Current(ctx)
	if !ok {
		return errors.New("missing identity")
	}
	member, err := m.directory.Member(ctx, tx, p.HouseholdID, p.UserID)
	if err != nil {
		return err
	}
	if member.MembershipID != p.MembershipID || !slices.Contains(member.Permissions, m.permission("read")) || !slices.Contains(member.Permissions, m.permission(capability)) {
		return errors.New("revoked permission")
	}
	return nil
}

func (m *Module) openFile(ctx context.Context, key string) (*os.File, func(), error) {
	if m.config.Storage != nil {
		f, cleanup, err := m.config.Storage.Open(ctx, m.config.ID, key)
		if !errors.Is(err, sql.ErrNoRows) {
			return f, cleanup, err
		}
	}
	f, err := m.files.Open(key)
	return f, func() {
		if f != nil {
			f.Close()
		}
	}, err
}
