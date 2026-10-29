package app

import (
	"database/sql"
	"net/http"
	"os"
	"path/filepath"

	"github.com/home-atlas/homeatlas/internal/core/access"
	"github.com/home-atlas/homeatlas/internal/core/identity"
	"github.com/home-atlas/homeatlas/internal/core/modules"
)

func (b *householdBackups) recoverInstallation(w http.ResponseWriter, r *http.Request) {
	code := r.Header.Get("X-HomeAtlas-Setup-Code")
	if !b.accounts.AuthorizeInstallationRecovery(w, r, code) {
		return
	}
	if r.Header.Get("X-HomeAtlas-Confirm-Restore") != "initialize-installation" {
		backupFail(w, 400, "restore_confirmation_required")
		return
	}
	if err := backupResponseDeadline(w); err != nil {
		backupError(w, err)
		return
	}
	dir, err := b.temp()
	if err != nil {
		backupError(w, err)
		return
	}
	defer os.RemoveAll(dir)
	path, _, err := receiveBackup(w, r, dir)
	if err != nil {
		backupError(w, err)
		return
	}
	s, err := b.extractBackup(r.Context(), path, filepath.Join(dir, "validated"), "")
	if err != nil {
		backupError(w, err)
		return
	}
	if s.version < 2 {
		backupFail(w, 400, "backup_identity_required")
		return
	}
	err = b.apply(r.Context(), s, filepath.Join(dir, "validated"), false, func(tx *sql.Tx) (access.Principal, error) {
		var occupied bool
		if err := tx.QueryRowContext(r.Context(), "SELECT EXISTS(SELECT 1 FROM household_module_states)").Scan(&occupied); err != nil {
			return access.Principal{}, err
		}
		if occupied {
			return access.Principal{}, identity.ErrInstallationNotEmpty
		}
		for _, p := range b.participants {
			present, err := p.Present(r.Context(), tx)
			if err != nil {
				return access.Principal{}, err
			}
			if !present {
				continue
			}
			empty, err := p.Empty(r.Context(), tx)
			if err != nil {
				return access.Principal{}, err
			}
			if !empty {
				return access.Principal{}, identity.ErrInstallationNotEmpty
			}
		}
		return b.accounts.ImportInstallation(r.Context(), tx, s.household, s.members, code)
	})
	if err != nil {
		backupError(w, err)
		return
	}
	b.accounts.FinishInstallationRecovery()
	report := b.restoreReport(s)
	report["householdName"] = s.household.Name
	report["members"] = len(s.members)
	modules.JSON(w, 201, report)
}
