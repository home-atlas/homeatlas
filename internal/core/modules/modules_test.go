package modules_test

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/home-atlas/homeatlas/internal/core/access"
	"github.com/home-atlas/homeatlas/internal/core/database"
	"github.com/home-atlas/homeatlas/internal/core/modules"
)

// A second in-memory module proves the Core contract independently of Documents.
type example struct{ manifest modules.Manifest }

func (e example) Manifest() modules.Manifest                                     { return e.manifest }
func (e example) Migrations() []database.Migration                               { return nil }
func (e example) SearchPermission() string                                       { return "example.read" }
func (e example) Search(context.Context, string) ([]modules.SearchResult, error) { return nil, nil }
func (e example) Routes() []modules.Route {
	return []modules.Route{{Method: "POST", Path: "/", Permission: "example.write", Handler: func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusCreated) }}}
}

type dependent struct {
	id           string
	dependencies []string
}

func (m dependent) Manifest() modules.Manifest {
	return modules.Manifest{ID: m.id, Name: m.id, Version: "1.0.0", ContractVersion: modules.ContractVersion, Capabilities: []string{m.id + ".read"}, Dependencies: m.dependencies, Navigation: modules.Navigation{Path: "/" + m.id}}
}
func (m dependent) Migrations() []database.Migration                               { return nil }
func (m dependent) Routes() []modules.Route                                        { return nil }
func (m dependent) SearchPermission() string                                       { return m.id + ".read" }
func (m dependent) Search(context.Context, string) ([]modules.SearchResult, error) { return nil, nil }

func TestHouseholdDependenciesAuthorizationAndInstallationBounds(t *testing.T) {
	db, err := database.Open(filepath.Join(t.TempDir(), "modules.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	r, err := modules.New(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range []dependent{{"base", nil}, {"dependent", []string{"base"}}} {
		if err := r.Register(context.Background(), m); err != nil {
			t.Fatal(err)
		}
	}
	ctx := access.WithPrincipal(context.Background(), access.Principal{UserID: "operator", HouseholdID: "home-a"})
	allow := func(*sql.Tx) error { return nil }
	denied := errors.New("denied")
	if err := r.SetHouseholdEnabled(ctx, "dependent", false, func(*sql.Tx) error { return denied }); !errors.Is(err, denied) {
		t.Fatal("authorization bypassed")
	}
	if err := r.SetHouseholdEnabled(context.Background(), "dependent", false, allow); !errors.Is(err, modules.ErrHouseholdRequired) {
		t.Fatal("missing household accepted")
	}
	if err := r.SetHouseholdEnabled(ctx, "base", false, allow); !errors.Is(err, modules.ErrDependency) {
		t.Fatal("active consumer lost dependency")
	}
	if err := r.SetHouseholdEnabled(ctx, "dependent", false, allow); err != nil {
		t.Fatal(err)
	}
	if err := r.SetHouseholdEnabled(ctx, "base", false, allow); err != nil {
		t.Fatal(err)
	}
	if err := r.SetHouseholdEnabled(ctx, "dependent", true, allow); !errors.Is(err, modules.ErrDependency) {
		t.Fatal("consumer enabled before dependency")
	}
	states, err := r.ListFor(ctx, "home-b")
	if err != nil || !states[0].Enabled || !states[1].Enabled {
		t.Fatal("other household was affected", err)
	}
	again, err := modules.New(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range []dependent{{"base", nil}, {"dependent", []string{"base"}}} {
		if err := again.Register(context.Background(), m); err != nil {
			t.Fatal(err)
		}
	}
	states, err = again.ListFor(ctx, "home-a")
	if err != nil || states[0].Enabled || states[1].Enabled {
		t.Fatal("household state lost on restart", err)
	}
	if err := again.SetEnabled(ctx, "dependent", false); err != nil {
		t.Fatal(err)
	}
	if err := again.SetEnabled(ctx, "base", false); err != nil {
		t.Fatal(err)
	}
	if err := again.SetHouseholdEnabled(ctx, "base", true, allow); !errors.Is(err, modules.ErrUnavailable) {
		t.Fatal("household enabled an installation-blocked module")
	}
	for _, id := range []string{"settings", "household", "family", "invitations"} {
		if err := again.Register(ctx, dependent{id: id}); err == nil {
			t.Fatal("module replaced Core namespace", id)
		}
	}
}

func TestContractValidatesCapabilitiesAndGuardsEveryMethod(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	r, err := modules.New(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	manifest := modules.Manifest{ID: "example", Name: "Example", Version: "0.1.0", ContractVersion: modules.ContractVersion,
		Navigation: modules.Navigation{Path: "/example"}, Capabilities: []string{"example.read"}}
	if err := r.Register(ctx, example{manifest}); err == nil {
		t.Fatal("undeclared write permission accepted")
	}
	manifest.Capabilities = append(manifest.Capabilities, "example.write")
	manifest.Dependencies = []string{"missing"}
	if err := r.Register(ctx, example{manifest}); err == nil {
		t.Fatal("missing dependency accepted")
	}
	manifest.Dependencies = nil
	if err := r.Register(ctx, example{manifest}); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	r.Mount(mux)
	for _, test := range []struct {
		principal access.Principal
		status    int
	}{
		{access.Principal{}, 401},
		{access.Principal{UserID: "alice", HouseholdID: "home", Permissions: map[string]bool{"example.read": true}}, 403},
		{access.Principal{UserID: "alice", HouseholdID: "home", Permissions: map[string]bool{"example.write": true}}, 201},
	} {
		req := httptest.NewRequest("POST", "/api/example", nil)
		req = req.WithContext(access.WithPrincipal(ctx, test.principal))
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		if w.Code != test.status {
			t.Fatalf("got %d, want %d", w.Code, test.status)
		}
	}
}
