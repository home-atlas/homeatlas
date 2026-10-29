package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	web "github.com/home-atlas/homeatlas/apps/web"
	"github.com/home-atlas/homeatlas/internal/app"
	"github.com/home-atlas/homeatlas/internal/core/database"
	"github.com/home-atlas/homeatlas/internal/core/identity"
	"github.com/home-atlas/homeatlas/internal/core/mailer"
	"github.com/home-atlas/homeatlas/internal/core/plugins"
)

func main() {
	if err := run(); err != nil {
		slog.Error("homeatlas stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	if len(os.Args) > 1 && os.Args[1] == "plugins" {
		return pluginCommand(context.Background(), os.Args[2:], os.Stdout, os.Stderr)
	}
	if len(os.Args) > 1 && os.Args[1] == "recover" {
		return recoveryCommand(context.Background(), os.Args[2:], os.Stdout, os.Stderr)
	}
	addr := flag.String("addr", "127.0.0.1:8080", "HTTP listen address")
	data := flag.String("data", "./data", "Private data directory")
	publicURL := flag.String("public-url", "http://127.0.0.1:8080", "Browser origin; HTTPS is required outside loopback")
	selectedModules := flag.String("modules", "none", "Built-in modules, comma-separated; use none for Core only (existing data is preserved)")
	externalPlugins := flag.String("plugins", "installed", "External plugin execution: installed or none (packages/data are preserved)")
	flag.Parse()
	if flag.NArg() != 0 {
		return fmt.Errorf("unexpected arguments; use homeatlas -help or homeatlas recover -help")
	}
	if *externalPlugins != "installed" && *externalPlugins != "none" {
		return fmt.Errorf("plugins must be installed or none")
	}
	names := []string{}
	if strings.TrimSpace(*selectedModules) != "none" {
		for _, name := range strings.Split(*selectedModules, ",") {
			names = append(names, strings.TrimSpace(name))
		}
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	client, err := mailer.FromEnv(os.Getenv)
	if err != nil {
		return err
	}
	var sender identity.Mailer
	if client != nil {
		sender = client
	}
	db, err := database.Open(filepath.Join(*data, "homeatlas.db"))
	if err != nil {
		return err
	}
	defer db.Close()
	application, err := app.NewWithOptions(ctx, db, web.FS, *publicURL, *data, sender, app.Options{Modules: names, DisableExternalPlugins: *externalPlugins == "none"})
	if err != nil {
		return err
	}
	defer application.Close()
	if _, err := os.Stat(filepath.Join(*data, "setup-code")); err == nil {
		slog.Info("initial setup requires the private code file", "file", filepath.Join(*data, "setup-code"))
	}
	server := &http.Server{Addr: *addr, Handler: application.Handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 2 * time.Minute, WriteTimeout: 2 * time.Minute, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 1 << 20}
	result := make(chan error, 1)
	go func() { result <- server.ListenAndServe() }()
	slog.Info("homeatlas", "address", *addr, "public_url", *publicURL, "authentication", "server-side sessions")
	select {
	case err := <-result:
		return err
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return server.Shutdown(shutdown)
	}
}

func pluginCommand(ctx context.Context, args []string, out, stderr io.Writer) error {
	if len(args) == 0 || (args[0] != "install" && args[0] != "list") {
		return fmt.Errorf("usage: homeatlas plugins install|list -data <directory> (install requires -file, -sha256 and -allow)")
	}
	f := flag.NewFlagSet("plugins "+args[0], flag.ContinueOnError)
	f.SetOutput(stderr)
	data := f.String("data", "./data", "Private data directory; stop the server before installation")
	file := f.String("file", "", "Local .hap ZIP package")
	digest := f.String("sha256", "", "Expected SHA-256 of the complete package")
	allow := f.String("allow", "", "Exact comma-separated permissions reviewed and approved for this package")
	if err := f.Parse(args[1:]); err != nil {
		return err
	}
	if f.NArg() != 0 {
		return fmt.Errorf("unexpected plugin arguments")
	}
	dir := filepath.Join(*data, "plugins")
	if args[0] == "install" {
		p, err := plugins.Install(ctx, dir, *file, *digest, strings.Split(*allow, ","))
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(out, "Installed %s %s (SHA-256 %s). Restart HomeAtlas to load it.\n", p.Manifest.ID, p.Manifest.Version, p.Digest)
		return err
	}
	packages, err := plugins.Load(dir)
	if err != nil {
		return err
	}
	for _, p := range packages {
		if _, err := fmt.Fprintf(out, "%s\t%s\t%s\n", p.Manifest.ID, p.Manifest.Version, p.Digest); err != nil {
			return err
		}
	}
	return nil
}

func recoveryCommand(ctx context.Context, args []string, out, stderr io.Writer) error {
	flags := flag.NewFlagSet("homeatlas recover", flag.ContinueOnError)
	flags.SetOutput(stderr)
	dataDir := flags.String("data", "./data", "Existing private data directory")
	email := flags.String("email", "", "Email of the existing local account (legacy alias for -account)")
	account := flags.String("account", "", "Username or email of the existing local account")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *account == "" {
		*account = *email
	}
	if *account == "" || (*email != "" && *email != *account) || flags.NArg() != 0 {
		return fmt.Errorf("usage: homeatlas recover -data <directory> -account <username-or-email>")
	}
	path := filepath.Join(*dataDir, "homeatlas.db")
	if info, err := os.Stat(path); err != nil || !info.Mode().IsRegular() {
		return fmt.Errorf("existing homeatlas.db not found in the selected data directory")
	}
	db, err := database.Open(path)
	if err != nil {
		return err
	}
	defer db.Close()
	code, expires, err := identity.IssueRecovery(ctx, db, *account)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(out, "Código de recuperación (privado, un solo uso):\n%s\nCaduca: %s\nAbre /forgot-password en tu instalación e introduce el código. No lo envíes por una URL.\n", code, expires.Format(time.RFC3339))
	return err
}
