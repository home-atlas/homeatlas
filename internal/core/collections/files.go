package collections

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
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
	u, err := m.beginUpload(r.Context())
	if err != nil {
		writeUploadError(w, err)
		return
	}
	defer u.close()
	r.Body = http.MaxBytesReader(w, r.Body, m.config.MaxFileBytes+(128<<10))
	reader, err := r.MultipartReader()
	if err != nil {
		fail(w, 415, "multipart_required")
		return
	}
	var raw []byte
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
			if u.file != nil {
				fail(w, 400, "invalid_upload")
				return
			}
			_, disposition, err := mime.ParseMediaType(part.Header.Get("Content-Disposition"))
			if err != nil {
				fail(w, 400, "invalid_filename")
				return
			}
			if err := u.copyFile(disposition["filename"], part); err != nil {
				writeUploadError(w, err)
				return
			}
		default:
			fail(w, 400, "invalid_upload")
			return
		}
		part.Close()
	}
	item, err := u.commit(r.Context(), raw)
	if err != nil {
		writeUploadError(w, err)
		return
	}
	w.Header().Set("Location", "/api/"+m.config.ID+"/"+item.ID)
	modules.JSON(w, 201, item)
}

func writeUploadError(w http.ResponseWriter, err error) {
	var failure *UploadError
	if !errors.As(err, &failure) {
		uploadError(w, err)
		return
	}
	status, code := 500, failure.Code
	switch code {
	case "uploads_busy":
		status = 429
		w.Header().Set("Retry-After", "1")
	case "invalid_upload", "invalid_filename", "empty_file", "invalid_metadata":
		status = 400
	case "permission_denied":
		status = 403
	case "file_too_large":
		status = 413
	case "unsupported_file_type":
		status = 415
	case "remote_storage_unavailable":
		status, code = 503, "storage_unavailable"
	case "plugin_unavailable":
		status = 503
		w.Header().Set("Retry-After", "1")
	}
	fail(w, status, code)
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
