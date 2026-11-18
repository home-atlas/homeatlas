package httpadapter

import (
	"errors"
	"io"
	"mime"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/home-atlas/homeatlas/internal/core/access"
	"github.com/home-atlas/homeatlas/internal/core/backup"
	"github.com/home-atlas/homeatlas/internal/core/collections/application"
	"github.com/home-atlas/homeatlas/internal/core/collections/domain"
	"github.com/home-atlas/homeatlas/internal/core/fault"
	"github.com/home-atlas/homeatlas/internal/core/modules"
	"github.com/home-atlas/homeatlas/internal/core/unitofwork"
)

type Transport struct {
	*application.Module
	config application.Config
}

func New(module *application.Module, config application.Config) *Transport {
	return &Transport{module, config}
}
func (m *Transport) Routes() []modules.Route {
	return []modules.Route{
		{Method: http.MethodGet, Path: "/", Permission: m.Permission("read"), Handler: m.list},
		{Method: http.MethodPost, Path: "/", Permission: m.Permission("write"), Handler: m.upload},
		{Method: http.MethodGet, Path: "/{id}", Permission: m.Permission("read"), Handler: m.detail},
		{Method: http.MethodPatch, Path: "/{id}", Permission: m.Permission("write"), Handler: m.update},
		{Method: http.MethodGet, Path: "/{id}/download", Permission: m.Permission("read"), Handler: m.download},
		{Method: http.MethodGet, Path: "/{id}/shares", Permission: m.Permission("read"), Handler: m.shares},
		{Method: http.MethodPut, Path: "/{id}/shares/{user}", Permission: m.Permission("share"), Handler: m.share},
		{Method: http.MethodDelete, Path: "/{id}/shares/{user}", Permission: m.Permission("share"), Handler: m.unshare},
	}
}
func (m *Transport) list(w http.ResponseWriter, req *http.Request) {
	_, ok := access.Current(req.Context())
	if !ok || !access.Allows(req.Context(), m.Permission("read")) {
		modules.Authorize(w, req, m.Permission("read"))
		return
	}
	offset := 0
	if value := req.URL.Query().Get("offset"); value != "" {
		var err error
		offset, err = strconv.Atoi(value)
		if err != nil || offset < 0 || offset > 1_000_000 {
			fail(w, 400, "invalid_query")
			return
		}
	}
	query := strings.TrimSpace(req.URL.Query().Get("q"))
	category := req.URL.Query().Get("category")
	if len(query) > 200 || (category != "" && !m.config.Category(category)) {
		fail(w, 400, "invalid_query")
		return
	}
	result, err := m.List(req.Context(), ListInput{Query: query, Category: category, Offset: offset})
	if err != nil {
		itemError(w, err)
		return
	}
	modules.JSON(w, 200, result)
}
func (m *Transport) upload(w http.ResponseWriter, r *http.Request) {
	u, err := m.BeginUpload(r.Context())
	if err != nil {
		writeUploadError(w, err)
		return
	}
	defer u.Close()
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
			if u.HasFile() {
				fail(w, 400, "invalid_upload")
				return
			}
			_, disposition, err := mime.ParseMediaType(part.Header.Get("Content-Disposition"))
			if err != nil {
				fail(w, 400, "invalid_filename")
				return
			}
			if err := u.CopyFile(disposition["filename"], part); err != nil {
				writeUploadError(w, err)
				return
			}
		default:
			fail(w, 400, "invalid_upload")
			return
		}
		part.Close()
	}
	item, err := u.Commit(r.Context(), raw)
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
func (m *Transport) lookup(r *http.Request, owner bool) (Item, string, error) {
	return m.Lookup(r.Context(), r.PathValue("id"), owner)
}
func (m *Transport) owned(r *http.Request) (Item, string, error) { return m.lookup(r, true) }
func (m *Transport) detail(w http.ResponseWriter, r *http.Request) {
	d, err := m.Detail(r.Context(), r.PathValue("id"))
	if err != nil {
		collectionError(w, err)
		return
	}
	modules.JSON(w, 200, d)
}
func (m *Transport) update(w http.ResponseWriter, r *http.Request) {
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
	d, err := m.Update(r.Context(), r.PathValue("id"), raw)
	if err != nil {
		collectionError(w, err)
		return
	}

	modules.JSON(w, 200, d)
}
func (m *Transport) download(w http.ResponseWriter, r *http.Request) {
	d, key, err := m.lookup(r, false)
	if err != nil {
		itemError(w, err)
		return
	}
	if !domain.StorageKey.MatchString(key) {
		fail(w, 404, "item_not_found")
		return
	}
	file, cleanup, err := m.OpenFile(r.Context(), key)
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
	if errors.Is(err, unitofwork.ErrNotFound) {
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
func metadataError(w http.ResponseWriter, err error) {
	if errors.Is(err, ErrValidatorUnavailable) {
		w.Header().Set("Retry-After", "1")
		fail(w, 503, "plugin_unavailable")
	} else {
		fail(w, 400, "invalid_metadata")
	}
}
func collectionError(w http.ResponseWriter, err error) {
	var problem *fault.Error
	if errors.As(err, &problem) {
		if problem.RetryAfter > 0 {
			w.Header().Set("Retry-After", strconv.Itoa(problem.RetryAfter))
		}
		statuses := map[fault.Kind]int{fault.Invalid: 400, fault.Unauthenticated: 401, fault.Denied: 403, fault.Missing: 404, fault.Conflict: 409, fault.Internal: 500, fault.Unavailable: 503}
		fail(w, statuses[problem.Kind], problem.Code)
		return
	}
	itemError(w, err)
}
func (m *Transport) shares(w http.ResponseWriter, r *http.Request) {
	out, err := m.Shares(r.Context(), r.PathValue("id"))
	if err != nil {
		collectionError(w, err)
		return
	}
	modules.JSON(w, 200, out)
}
func (m *Transport) changeShare(w http.ResponseWriter, r *http.Request, remove bool) {
	err := m.ChangeShare(r.Context(), r.PathValue("id"), r.PathValue("user"), remove)
	if err != nil {
		collectionError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(204)
}
func (m *Transport) share(w http.ResponseWriter, r *http.Request)   { m.changeShare(w, r, false) }
func (m *Transport) unshare(w http.ResponseWriter, r *http.Request) { m.changeShare(w, r, true) }

const metadataLimit = domain.MetadataLimit

type Item = domain.Item
type ListInput = application.ListInput
type UploadError = application.UploadError

var ErrValidatorUnavailable = application.ErrValidatorUnavailable
