package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"path/filepath"
	"sort"
	"strings"

	"github.com/home-atlas/homeatlas/internal/core/access"
	"github.com/home-atlas/homeatlas/internal/core/backup"
	"github.com/home-atlas/homeatlas/internal/core/collections"
	"github.com/home-atlas/homeatlas/internal/core/database"
	"github.com/home-atlas/homeatlas/internal/core/identity"
	"github.com/home-atlas/homeatlas/internal/core/modules"
	"github.com/home-atlas/homeatlas/internal/core/objectstore"
	"github.com/home-atlas/homeatlas/internal/core/plugins"
)

type App struct {
	Handler http.Handler
	Modules *modules.Registry
	close   func() error
}

// New starts Core plus separately installed and approved packages.
func New(ctx context.Context, db *sql.DB, assets fs.FS, publicURL, dataDir string, mailer identity.Mailer) (*App, error) {
	return NewWithModules(ctx, db, assets, publicURL, dataDir, mailer, defaultModules)
}

// NewWithModules selects built-ins and loads approved packages from data/plugins.
func NewWithModules(ctx context.Context, db *sql.DB, assets fs.FS, publicURL, dataDir string, mailer identity.Mailer, names []string) (*App, error) {
	return NewWithOptions(ctx, db, assets, publicURL, dataDir, mailer, Options{Modules: names})
}

type Options struct {
	Modules                []string
	DisableExternalPlugins bool
	StorageHTTPClient      *http.Client
}

func NewWithOptions(ctx context.Context, db *sql.DB, assets fs.FS, publicURL, dataDir string, mailer identity.Mailer, options Options) (*App, error) {
	packages, err := plugins.Load(filepath.Join(dataDir, "plugins"))
	if err != nil {
		return nil, fmt.Errorf("load plugins: %w", err)
	}
	return newWithPlugins(ctx, db, assets, publicURL, dataDir, mailer, options, packages)
}

func newWithPlugins(ctx context.Context, db *sql.DB, assets fs.FS, publicURL, dataDir string, mailer identity.Mailer, options Options, packages []*plugins.Package) (*App, error) {
	names := options.Modules
	activePackages := packages
	if options.DisableExternalPlugins {
		activePackages = nil
	}
	selected := make([]moduleDefinition, 0, len(names))
	seen := map[string]bool{}
	ownerCapabilities := []string{identity.Manage, backupPermission}
	delegableCapabilities := []string{}
	for _, name := range names {
		definition, ok := builtInModules[name]
		if !ok || seen[name] {
			return nil, fmt.Errorf("unknown or duplicate built-in module: %q", name)
		}
		seen[name] = true
		selected = append(selected, definition)
		ownerCapabilities = append(ownerCapabilities, definition.ownerCapabilities...)
		delegableCapabilities = append(delegableCapabilities, definition.delegableCapabilities...)
	}
	for _, p := range activePackages {
		// Only packages whose complete permission list was approved by the local
		// installer reach this policy. Restarting never reapplies a revoked grant.
		ownerCapabilities = append(ownerCapabilities, p.Capabilities()...)
		delegableCapabilities = append(delegableCapabilities, p.Capabilities()...)
	}
	registry, err := modules.New(ctx, db)
	if err != nil {
		return nil, err
	}
	accounts, err := identity.New(ctx, db, identity.Config{PublicURL: publicURL, SetupCodePath: filepath.Join(dataDir, "setup-code"), Mailer: mailer, Modules: registry, OwnerCapabilities: ownerCapabilities, DelegableCapabilities: delegableCapabilities})
	if err != nil {
		return nil, err
	}
	ready := false
	defer func() {
		if !ready {
			accounts.Close()
		}
	}()
	var opened []io.Closer
	engine := &plugins.Engine{}
	for _, p := range packages {
		if p.Manifest.Storage != "" {
			store, err := plugins.NewStore(ctx, db)
			if err != nil {
				return nil, err
			}
			engine.Store = store
			break
		}
	}
	opened = append(opened, engine)
	defer func() {
		if !ready {
			for _, module := range opened {
				module.Close()
			}
		}
	}()
	for _, definition := range selected {
		module, err := definition.register(ctx, db, dataDir, accounts, registry)
		if err != nil {
			return nil, err
		}
		opened = append(opened, module)
	}
	storage, err := objectstore.New(ctx, db, dataDir)
	if err != nil {
		return nil, err
	}
	if options.StorageHTTPClient != nil {
		storage.HTTPClient = options.StorageHTTPClient
	}
	collectionHosts := map[string]*collections.Module{}
	for _, p := range activePackages {
		if p.Manifest.Collection == nil {
			continue
		}
		config := plugins.CollectionConfig(p, engine)
		config.Storage = storage
		host, err := collections.New(db, plugins.CollectionDirectory(dataDir, p), accounts, config)
		if err != nil {
			return nil, err
		}
		opened = append(opened, host)
		collectionHosts[p.Manifest.ID] = host
	}
	for _, p := range activePackages {
		var module modules.Module = &plugins.Module{Package: p, Engine: engine}
		if host := collectionHosts[p.Manifest.ID]; host != nil {
			module = host
		}
		policy := ""
		guard := ""
		if p.Manifest.Collection != nil {
			guard = " AND NOT EXISTS(SELECT 1 FROM module_states WHERE id='" + p.Manifest.ID + "')"
		}
		for _, permission := range p.Capabilities() {
			policy += "INSERT OR IGNORE INTO identity_permissions(user_id,household_id,capability) SELECT user_id,household_id,'" + permission + "' FROM identity_memberships WHERE role='owner'" + guard + ";"
		}
		if err := database.Apply(ctx, db, "app.install-"+p.Manifest.ID, []database.Migration{{Version: 1, SQL: policy}}); err != nil {
			return nil, err
		}
		if err := registry.Register(ctx, module); err != nil {
			return nil, err
		}

	}
	mux := http.NewServeMux()
	participants := []backup.Participant{}
	known := make([]string, 0, len(builtInModules))
	for name := range builtInModules {
		known = append(known, name)
	}
	sort.Strings(known)
	for _, name := range known {
		external := false
		for _, p := range packages {
			if p.Manifest.ID == name {
				external = true
			}
		}
		if external {
			continue
		}
		definition := builtInModules[name]
		if definition.backup != nil {
			participants = append(participants, definition.backup(dataDir, accounts))
		}
	}
	storageCollections := []objectstore.Collection{}
	for _, p := range packages {
		if p.Manifest.Collection != nil {
			config := plugins.CollectionConfig(p, engine)
			config.Storage = storage
			storageCollections = append(storageCollections, collections.StorageCollection(config, plugins.CollectionDirectory(dataDir, p)))
			provider := collections.NewBackup(filepath.Dir(plugins.CollectionDirectory(dataDir, p)), accounts, config)
			participant := backup.Participant{Provider: plugins.CollectionBackup{Provider: provider, Package: p}}
			if config.LegacySnapshot {
				participant.LegacyPrefix = p.Manifest.ID
				participant.LegacyCountField = p.Manifest.ID
			}
			participants = append(participants, participant)
		} else {
			participants = append(participants, backup.Participant{Provider: plugins.Backup{Package: p}})
		}
	}
	backups, err := newBackups(ctx, db, accounts, dataDir, participants...)
	if err != nil {
		return nil, err
	}
	// The WASI host forbids writes from these declared query actions, so they
	// need not queue behind snapshot mutation locks. Crypto admission stays bounded.
	backups.readOnlyActions = map[string]bool{}
	for _, p := range activePackages {
		for _, v := range p.Manifest.Views {
			if v.ReadOnly {
				backups.readOnlyActions["POST /api/"+p.Manifest.ID+"/actions/"+v.ID] = true
			}
		}
	}
	backups.mount(mux)
	mountStorage(mux, storage, accounts, storageCollections)
	accounts.Mount(mux)
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		if err := db.PingContext(r.Context()); err != nil {
			modules.JSON(w, 503, map[string]string{"status": "unavailable"})
			return
		}
		modules.JSON(w, 200, map[string]string{"status": "ok", "database": "sqlite", "authentication": "sessions"})
	})
	mux.HandleFunc("GET /api/modules", func(w http.ResponseWriter, r *http.Request) {
		p, ok := access.Current(r.Context())
		if !ok {
			modules.JSON(w, 401, map[string]string{"error": "authentication_required"})
			return
		}
		installed, err := registry.ListFor(r.Context(), p.HouseholdID)
		if err != nil {
			modules.JSON(w, 503, map[string]string{"error": "modules_unavailable"})
			return
		}
		modules.JSON(w, 200, installed)
	})
	mux.HandleFunc("GET /api/search", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := access.Current(r.Context()); !ok {
			modules.JSON(w, 401, map[string]string{"error": "authentication_required"})
			return
		}
		query := strings.TrimSpace(r.URL.Query().Get("q"))
		if len(query) > 200 {
			modules.JSON(w, 400, map[string]string{"error": "query_too_long"})
			return
		}
		matches, err := registry.Search(r.Context(), query)
		if err != nil {
			modules.JSON(w, 500, map[string]string{"error": "search_unavailable"})
			return
		}
		modules.JSON(w, 200, matches)
	})
	registry.Mount(mux)
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		modules.JSON(w, 404, map[string]string{"error": "not_found"})
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		path := strings.TrimPrefix(r.URL.Path, "/")
		if path == "" {
			path = "index.html"
		}
		if !fs.ValidPath(path) {
			http.NotFound(w, r)
			return
		}
		if _, err := fs.Stat(assets, path); err != nil {
			if strings.Contains(path, ".") {
				http.NotFound(w, r)
				return
			}
			path = "index.html"
		}
		if _, err := fs.Stat(assets, path); err != nil {
			http.Error(w, "Build the frontend with make build, or use the Vite dev server.", 503)
			return
		}
		if path == "index.html" {
			w.Header().Set("Cache-Control", "no-cache")
		}
		http.ServeFileFS(w, r, assets, path)
	})
	stopBackups := backups.startSchedules(withBackupResources(ctx, engine.Pause))
	guarded := backups.guard(mux)
	withResources := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		guarded.ServeHTTP(w, r.WithContext(withBackupResources(r.Context(), engine.Pause)))
	})
	ready = true
	return &App{close: func() error {
		stopBackups()
		accounts.Close()
		var closeErr error
		for _, module := range opened {
			closeErr = errors.Join(closeErr, module.Close())
		}
		return closeErr
	}, Handler: securityHeaders(accounts.Middleware(withResources)), Modules: registry}, nil
}

func (a *App) Close() error { return a.close() }

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; object-src 'none'; frame-ancestors 'none'; base-uri 'self'")
		next.ServeHTTP(w, r)
	})
}
