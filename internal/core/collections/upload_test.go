package collections

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/home-atlas/homeatlas/internal/core/access"
	"github.com/home-atlas/homeatlas/internal/core/database"
	"github.com/home-atlas/homeatlas/internal/core/modules"
)

type uploadDirectory struct{ permissions []string }

func (d *uploadDirectory) Member(context.Context, *sql.Tx, string, string) (access.Member, error) {
	return access.Member{MembershipID: "membership", Permissions: d.permissions}, nil
}

func TestUploadUseCaseWithoutHTTPPreservesAuthorizationAndCleanup(t *testing.T) {
	for _, scenario := range []string{"success", "revoked-during-validation", "disabled-during-validation", "old-membership", "oversized", "invalid-metadata", "unsafe-filename"} {
		t.Run(scenario, func(t *testing.T) {
			db, err := database.Open(filepath.Join(t.TempDir(), "test.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			registry, err := modules.New(context.Background(), db)
			if err != nil {
				t.Fatal(err)
			}
			config := testConfig("ext-files")
			members := &uploadDirectory{[]string{"ext-files.read", "ext-files.write"}}
			ctx := access.WithPrincipal(context.Background(), access.Principal{UserID: "owner", HouseholdID: "home", MembershipID: "membership", Permissions: map[string]bool{"ext-files.read": true, "ext-files.write": true}})
			if scenario == "old-membership" {
				ctx = access.WithPrincipal(ctx, access.Principal{UserID: "owner", HouseholdID: "home", MembershipID: "previous", Permissions: map[string]bool{"ext-files.read": true, "ext-files.write": true}})
			}
			config.Validate = func(_ context.Context, raw []byte) ([]byte, error) {
				if scenario == "revoked-during-validation" {
					members.permissions = []string{"ext-files.read"}
				}
				if scenario == "disabled-during-validation" {
					if err := registry.SetEnabled(context.Background(), "ext-files", false); err != nil {
						t.Fatal(err)
					}
				}
				return raw, nil
			}
			directory := t.TempDir()
			module, err := New(db, directory, members, config)
			if err != nil {
				t.Fatal(err)
			}
			defer module.Close()
			if err := registry.Register(ctx, module); err != nil {
				t.Fatal(err)
			}
			content, filename, raw := "A private original", "original.txt", `{"title":"My file","category":"shares","description":"","tags":[]}`
			switch scenario {
			case "oversized":
				content = strings.Repeat("x", 1025)
			case "invalid-metadata":
				raw = `{"title":"My file","category":"unknown"}`
			case "unsafe-filename":
				filename = "../escape.txt"
			}
			item, err := module.Upload(ctx, UploadInput{Filename: filename, Metadata: []byte(raw), File: strings.NewReader(content)})
			if scenario == "success" {
				if err != nil {
					t.Fatal(err)
				}
				if item.OwnerID != "owner" || item.Size != int64(len(content)) || item.SHA256 != fmt.Sprintf("%x", sha256.Sum256([]byte(content))) {
					t.Fatal(item)
				}
				stored, err := os.ReadFile(filepath.Join(directory, item.ID))
				if err != nil || string(stored) != content {
					t.Fatal("original not preserved", err)
				}
				var owner, home string
				if err := module.db.QueryRowContext(ctx, "SELECT owner_id,household_id FROM collection_items WHERE id=?", item.ID).Scan(&owner, &home); err != nil || owner != "owner" || home != "home" {
					t.Fatal("scope lost", err)
				}
				return
			}
			var failure *UploadError
			if !errors.As(err, &failure) {
				t.Fatal("expected a domain failure", err)
			}
			expected := map[string]string{"revoked-during-validation": "permission_denied", "disabled-during-validation": "permission_denied", "old-membership": "permission_denied", "oversized": "file_too_large", "invalid-metadata": "invalid_metadata", "unsafe-filename": "invalid_filename"}[scenario]
			if failure.Code != expected {
				t.Fatal(failure.Code, expected)
			}
			entries, err := os.ReadDir(directory)
			if err != nil || len(entries) != 0 {
				t.Fatal("failed upload left an original", err)
			}
			var count int
			if err := module.db.QueryRowContext(ctx, "SELECT count(*) FROM collection_items").Scan(&count); err != nil || count != 0 {
				t.Fatal("failed upload published metadata", err)
			}
			if len(module.uploads) != 0 {
				t.Fatal("upload admission leaked")
			}
		})
	}
}
