package collections

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
	"github.com/home-atlas/homeatlas/internal/core/backup"
	"github.com/home-atlas/homeatlas/internal/core/objectstore"
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
func uploadFailure(code string, err error) error { return &UploadError{code, err} }

// Upload streams one original and publishes its metadata atomically. It never
// owns the input reader; its private staging file is removed on failure.
func (m *Module) Upload(ctx context.Context, input UploadInput) (Item, error) {
	u, err := m.beginUpload(ctx)
	if err != nil {
		return Item{}, err
	}
	defer u.close()
	if input.File == nil {
		return Item{}, uploadFailure("invalid_upload", nil)
	}
	if err := u.copyFile(input.Filename, input.File); err != nil {
		return Item{}, err
	}
	return u.commit(ctx, input.Metadata)
}

type uploadState struct {
	module                 *Module
	id, filename, checksum string
	file                   *os.File
	size                   int64
	remote                 *objectstore.Pending
	kept                   bool
}

func (m *Module) beginUpload(ctx context.Context) (*uploadState, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if _, ok := access.Current(ctx); !ok || m.directory == nil || !access.Allows(ctx, m.permission("read")) || !access.Allows(ctx, m.permission("write")) {
		return nil, uploadFailure("permission_denied", nil)
	}
	select {
	case m.uploads <- struct{}{}:
		return &uploadState{module: m, id: rand.Text()}, nil
	default:
		return nil, uploadFailure("uploads_busy", nil)
	}
}

func (u *uploadState) copyFile(filename string, source io.Reader) error {
	if !safeFilename(filename) {
		return uploadFailure("invalid_filename", nil)
	}
	u.filename = filename
	var err error
	u.file, err = u.module.files.OpenFile(u.id, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err != nil {
		return uploadFailure("storage_unavailable", err)
	}
	digest := sha256.New()
	u.size, err = io.Copy(io.MultiWriter(u.file, digest), io.LimitReader(source, u.module.config.MaxFileBytes+1))
	if err != nil {
		return err
	}
	if u.size > u.module.config.MaxFileBytes {
		return uploadFailure("file_too_large", nil)
	}
	if u.size == 0 {
		return uploadFailure("empty_file", nil)
	}
	u.checksum = fmt.Sprintf("%x", digest.Sum(nil))
	return nil
}

func (u *uploadState) commit(ctx context.Context, raw []byte) (Item, error) {
	m := u.module
	if u.file == nil || len(raw) == 0 {
		return Item{}, uploadFailure("invalid_upload", nil)
	}
	value, err := m.parseMetadata(ctx, raw)
	if err != nil {
		code := "invalid_metadata"
		if errors.Is(err, ErrValidatorUnavailable) {
			code = "plugin_unavailable"
		}
		return Item{}, uploadFailure(code, err)
	}
	mediaType, err := fileType(u.file, u.filename, u.size)
	if err != nil || !slices.Contains(m.config.MediaTypes, mediaType) {
		return Item{}, uploadFailure("unsupported_file_type", err)
	}
	if err := u.file.Sync(); err != nil {
		return Item{}, uploadFailure("storage_unavailable", err)
	}
	backup.DiscardCache(u.file)
	directory, err := m.files.Open(".")
	if err != nil {
		return Item{}, uploadFailure("storage_unavailable", err)
	}
	err = directory.Sync()
	directory.Close()
	if err != nil {
		return Item{}, uploadFailure("storage_unavailable", err)
	}
	p, _ := access.Current(ctx)
	if m.config.Storage != nil {
		u.remote, err = m.config.Storage.Upload(ctx, p.HouseholdID, m.config.ID, u.id, u.file, u.size, u.checksum)
		if err != nil {
			return Item{}, uploadFailure("remote_storage_unavailable", err)
		}
	}
	if err := u.file.Close(); err != nil {
		return Item{}, uploadFailure("storage_unavailable", err)
	}
	item := Item{OwnerID: p.UserID, ID: u.id, Title: value.Title, Category: value.Category, Description: value.Description, Tags: value.Tags, Filename: u.filename, MediaType: mediaType, Size: u.size, SHA256: u.checksum, HasFile: true, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	if err := m.repository.publish(ctx, p, item, u.remote, func(tx unitofwork.Transaction) error { return m.authorizeMutation(ctx, tx, "write") }); err != nil {
		return Item{}, err
	}
	u.kept = true
	return item, nil
}

func (u *uploadState) close() {
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
