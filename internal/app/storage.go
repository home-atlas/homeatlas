package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"slices"
	"time"

	"github.com/home-atlas/homeatlas/internal/core/access"
	"github.com/home-atlas/homeatlas/internal/core/identity"
	"github.com/home-atlas/homeatlas/internal/core/modules"
	"github.com/home-atlas/homeatlas/internal/core/objectstore"
)

var errStorageDenied = errors.New("storage permission denied")

func mountStorage(mux *http.ServeMux, store *objectstore.Store, members access.Directory, collections []objectstore.Collection) {
	authorize := func(ctx context.Context, tx *sql.Tx) error {
		p, ok := access.Current(ctx)
		if !ok {
			return errStorageDenied
		}
		m, err := members.Member(ctx, tx, p.HouseholdID, p.UserID)
		if err != nil {
			return err
		}
		if m.MembershipID != p.MembershipID || m.Role != "owner" || !slices.Contains(m.Permissions, identity.Manage) {
			return errStorageDenied
		}
		return nil
	}
	handler := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		p, ok := access.Current(r.Context())
		if !ok {
			modules.JSON(w, 401, map[string]string{"error": "authentication_required"})
			return
		}
		if err := authorize(r.Context(), nil); err != nil {
			modules.JSON(w, 403, map[string]string{"error": "permission_denied"})
			return
		}
		budget := 35 * time.Second
		if r.URL.Path == "/api/household/storage/move" {
			budget = 90 * time.Second
		}
		ctx, cancel := context.WithTimeout(r.Context(), budget)
		defer cancel()
		if r.URL.Path == "/api/household/storage/move" {
			if r.Method == "GET" {
				stats, err := store.MovePreview(ctx, p.HouseholdID, collections)
				if err != nil {
					storageError(w, err)
					return
				}
				modules.JSON(w, 200, stats)
				return
			}
			var input struct {
				Revision string `json:"revision"`
				Confirm  string `json:"confirm"`
			}
			decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
			decoder.DisallowUnknownFields()
			if decoder.Decode(&input) != nil || decoder.Decode(new(any)) != io.EOF || input.Confirm != "move-and-remove-source" {
				storageError(w, objectstore.Invalid)
				return
			}
			result, err := store.MoveNext(ctx, p.HouseholdID, input.Revision, collections, func(tx *sql.Tx) error { return authorize(ctx, tx) })
			if err != nil {
				storageError(w, err)
				return
			}
			modules.JSON(w, 200, result)
			return
		}
		if r.Method == "PATCH" {
			var input objectstore.Settings
			decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
			decoder.DisallowUnknownFields()
			if err := decoder.Decode(&input); err != nil {
				storageError(w, objectstore.Invalid)
				return
			}
			if decoder.Decode(new(any)) != io.EOF {
				storageError(w, objectstore.Invalid)
				return
			}
			if err := store.Save(ctx, p.HouseholdID, input, func(tx *sql.Tx) error { return authorize(ctx, tx) }); err != nil {
				storageError(w, err)
				return
			}
		}
		settings, err := store.Settings(ctx, p.HouseholdID)
		if err != nil {
			storageError(w, err)
			return
		}
		modules.JSON(w, 200, settings)
	}
	mux.HandleFunc("GET /api/household/storage/move", handler)
	mux.HandleFunc("POST /api/household/storage/move", handler)
	mux.HandleFunc("GET /api/household/storage", handler)
	mux.HandleFunc("PATCH /api/household/storage", handler)
}
func storageError(w http.ResponseWriter, err error) {
	status, code := 503, "storage_unavailable"
	switch {
	case errors.Is(err, objectstore.Invalid):
		status, code = 400, "invalid_storage_settings"
	case errors.Is(err, objectstore.Conflict):
		status, code = 409, "storage_settings_changed"
	case errors.Is(err, errStorageDenied), errors.Is(err, sql.ErrNoRows):
		status, code = 403, "permission_denied"
	}
	modules.JSON(w, status, map[string]string{"error": code})
}
