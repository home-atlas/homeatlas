package application

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"slices"
	"time"

	"github.com/home-atlas/homeatlas/internal/core/access"
	backup "github.com/home-atlas/homeatlas/internal/core/backup/stream"
	"github.com/home-atlas/homeatlas/internal/core/collections/domain"
	"github.com/home-atlas/homeatlas/internal/core/unitofwork"
)

// UploadInput is transport independent. Trusted application callers supply an
// authenticated principal and coordinate the operation with the backup guard.
type UploadInput struct {
	Filename string
	Metadata []byte
	File     io.Reader
}

type UploadError struct {
	Code string
	Err  error
}

func (e *UploadError) Error() string             { return e.Code }
func (e *UploadError) Unwrap() error             { return e.Err }
func UploadFailure(code string, err error) error { return &UploadError{code, err} }

// Upload streams one original and publishes its metadata atomically. It never
// owns the input reader; its private staging file is removed on failure.
func (m *Module) Upload(ctx context.Context, input UploadInput) (Item, error) {
	u, err := m.BeginUpload(ctx)
	if err != nil {
		return Item{}, err
	}
	defer u.Close()
	if input.File == nil {
		return Item{}, UploadFailure("invalid_upload", nil)
	}
	if err := u.CopyFile(input.Filename, input.File); err != nil {
		return Item{}, err
	}
	return u.Commit(ctx, input.Metadata)
}

type UploadFlow struct {
	module                 *Module
	id, filename, checksum string
	file                   *os.File
	size                   int64
	remote                 PendingOriginal
	kept                   bool
}

func (m *Module) BeginUpload(ctx context.Context) (*UploadFlow, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if _, ok := access.Current(ctx); !ok || m.directory == nil || !access.Allows(ctx, m.Permission("read")) || !access.Allows(ctx, m.Permission("write")) {
		return nil, UploadFailure("permission_denied", nil)
	}
	select {
	case m.uploads <- struct{}{}:
		return &UploadFlow{module: m, id: rand.Text()}, nil
	default:
		return nil, UploadFailure("uploads_busy", nil)
	}
}

func (u *UploadFlow) CopyFile(filename string, source io.Reader) error {
	if !domain.SafeFilename(filename) {
		return UploadFailure("invalid_filename", nil)
	}
	u.filename = filename
	var err error
	u.file, err = u.module.files.Create(u.id)
	if err != nil {
		return UploadFailure("storage_unavailable", err)
	}
	digest := sha256.New()
	u.size, err = io.Copy(io.MultiWriter(u.file, digest), io.LimitReader(source, u.module.config.MaxFileBytes+1))
	if err != nil {
		return err
	}
	if u.size > u.module.config.MaxFileBytes {
		return UploadFailure("file_too_large", nil)
	}
	if u.size == 0 {
		return UploadFailure("empty_file", nil)
	}
	u.checksum = fmt.Sprintf("%x", digest.Sum(nil))
	return nil
}

func (u *UploadFlow) Commit(ctx context.Context, raw []byte) (Item, error) {
	m := u.module
	if u.file == nil || len(raw) == 0 {
		return Item{}, UploadFailure("invalid_upload", nil)
	}
	value, err := m.parseMetadata(ctx, raw)
	if err != nil {
		code := "invalid_metadata"
		if errors.Is(err, ErrValidatorUnavailable) {
			code = "plugin_unavailable"
		}
		return Item{}, UploadFailure(code, err)
	}
	mediaType, err := m.files.Inspect(u.file, u.filename, u.size)
	if err != nil || !slices.Contains(m.config.MediaTypes, mediaType) {
		return Item{}, UploadFailure("unsupported_file_type", err)
	}
	if err := u.file.Sync(); err != nil {
		return Item{}, UploadFailure("storage_unavailable", err)
	}
	backup.DiscardCache(u.file)
	if err := m.files.SyncDirectory(); err != nil {
		return Item{}, UploadFailure("storage_unavailable", err)
	}
	p, _ := access.Current(ctx)
	if m.config.Storage != nil {
		u.remote, err = m.config.Storage.Upload(ctx, p.HouseholdID, m.config.ID, u.id, u.file, u.size, u.checksum)
		if err != nil {
			return Item{}, UploadFailure("remote_storage_unavailable", err)
		}
	}
	if err := u.file.Close(); err != nil {
		return Item{}, UploadFailure("storage_unavailable", err)
	}
	item := Item{OwnerID: p.UserID, ID: u.id, Title: value.Title, Category: value.Category, Description: value.Description, Tags: value.Tags, Filename: u.filename, MediaType: mediaType, Size: u.size, SHA256: u.checksum, HasFile: true, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	if err := m.repository.Publish(ctx, p, item, u.remote, func(tx unitofwork.Transaction) error { return m.authorizeMutation(ctx, tx, "write") }); err != nil {
		return Item{}, err
	}
	u.kept = true
	return item, nil
}

func (u *UploadFlow) Close() {
	defer func() { <-u.module.uploads }()
	if u.remote != nil && !u.kept {
		u.remote.Abort()
	}
	if u.file != nil {
		u.file.Close()
		if !u.kept || u.remote != nil {
			if err := u.module.files.Remove(u.id); err != nil && !errors.Is(err, os.ErrNotExist) {
				slog.Error("remove uncommitted item", "id", u.id, "error", err)
			}
		}
	}
}

func (u *UploadFlow) HasFile() bool { return u.file != nil }
