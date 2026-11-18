package collections

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/home-atlas/homeatlas/internal/core/access"
	"github.com/home-atlas/homeatlas/internal/core/database"
	"github.com/home-atlas/homeatlas/internal/core/modules"
	"github.com/home-atlas/homeatlas/internal/core/unitofwork"
)

type operationDirectory map[string]access.Member

func (d operationDirectory) Member(_ context.Context, _ unitofwork.Transaction, home, user string) (access.Member, error) {
	if m, ok := d[user]; ok && home == "home" {
		return m, nil
	}
	return access.Member{}, unitofwork.ErrNotFound
}
func operationPrincipal(user, member, home string) context.Context {
	return access.WithPrincipal(context.Background(), access.Principal{UserID: user, MembershipID: member, HouseholdID: home, Permissions: map[string]bool{"ext-files.read": true, "ext-files.write": true, "ext-files.share": true}})
}

func TestCollectionOperationsWithoutHTTP(t *testing.T) {
	for _, scenario := range []string{"success", "revoked-during-validation", "disabled-during-validation", "write-fails"} {
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
			directory := operationDirectory{"owner": {ID: "owner", MembershipID: "owner-member", Permissions: []string{"ext-files.read", "ext-files.write", "ext-files.share"}}, "reader": {ID: "reader", MembershipID: "reader-member", Permissions: []string{"ext-files.read"}}}
			updating := false
			config := testConfig("ext-files")
			config.Validate = func(_ context.Context, raw []byte) ([]byte, error) {
				if updating {
					switch scenario {
					case "revoked-during-validation":
						m := directory["owner"]
						m.Permissions = []string{"ext-files.read"}
						directory["owner"] = m
					case "disabled-during-validation":
						if err := registry.SetEnabled(context.Background(), "ext-files", false); err != nil {
							t.Fatal(err)
						}
					}
				}
				return raw, nil
			}
			module, err := New(db, t.TempDir(), directory, config)
			if err != nil {
				t.Fatal(err)
			}
			defer module.Close()
			ctx := operationPrincipal("owner", "owner-member", "home")
			if err := registry.Register(ctx, module); err != nil {
				t.Fatal(err)
			}
			item, err := module.Upload(ctx, UploadInput{Filename: "original.txt", Metadata: []byte(`{"title":"Before","category":"shares"}`), File: strings.NewReader("original")})
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "write-fails" {
				if _, err := db.Exec(querySQL(config, `CREATE TRIGGER reject_update BEFORE UPDATE ON collection_items BEGIN SELECT RAISE(ABORT,'failure'); END`)); err != nil {
					t.Fatal(err)
				}
			}
			updating = true
			_, err = module.Update(ctx, item.ID, []byte(`{"title":"After","category":"shares"}`))
			if scenario != "success" {
				if err == nil {
					t.Fatal("failed/unauthorized update succeeded")
				}
				stored, _, readErr := module.Lookup(ctx, item.ID, true)
				if readErr != nil || stored.Title != "Before" {
					t.Fatal("failed update persisted", stored, readErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			matches, err := module.List(ctx, ListInput{Query: "After"})
			if err != nil || len(matches) != 1 {
				t.Fatal("search after update", matches, err)
			}
			if err := module.ChangeShare(ctx, item.ID, "reader", false); err != nil {
				t.Fatal(err)
			}
			reader := operationPrincipal("reader", "reader-member", "home")
			if _, _, err := module.Lookup(reader, item.ID, false); err != nil {
				t.Fatal("shared item unreadable", err)
			}
			if _, _, err := module.Lookup(operationPrincipal("reader", "reader-member", "neighbor"), item.ID, false); !errors.Is(err, unitofwork.ErrNotFound) {
				t.Fatal("cross-household access", err)
			}
			directory["reader"] = access.Member{ID: "reader", MembershipID: "rejoined", Permissions: []string{"ext-files.read"}}
			if _, _, err := module.Lookup(operationPrincipal("reader", "rejoined", "home"), item.ID, false); !errors.Is(err, unitofwork.ErrNotFound) {
				t.Fatal("old grant revived", err)
			}
			shares, err := module.Shares(ctx, item.ID)
			if err != nil || len(shares) != 0 {
				t.Fatal("stale grant exposed", shares, err)
			}
		})
	}
}
