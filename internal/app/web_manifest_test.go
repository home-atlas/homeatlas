package app_test

import (
	"bytes"
	"encoding/json"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

func TestWebManifestIsPublicAndAccountDataIsPrivate(t *testing.T) {
	f := newCoreFixture(t, true)
	assets := f.assets.(fstest.MapFS)
	for _, name := range []string{"manifest.webmanifest", "icons/homeatlas-192.png", "icons/homeatlas-512.png", "icons/homeatlas-180.png"} {
		raw, err := os.ReadFile(filepath.Join("../../apps/web/public", name))
		if err != nil {
			t.Fatal(err)
		}
		assets[name] = &fstest.MapFile{Data: raw}
	}
	w := perform(f.app, "GET", "/manifest.webmanifest", nil, nil)
	if w.Code != 200 || w.Header().Get("Content-Type") != "application/manifest+json" {
		t.Fatal(w.Code, w.Header())
	}
	var manifest struct {
		ID, Name, Scope, Display string
		StartURL                 string `json:"start_url"`
		Icons                    []struct{ Src, Sizes string }
	}
	if err := json.Unmarshal(w.Body.Bytes(), &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.ID != "/" || manifest.StartURL != "/" || manifest.Scope != "/" || manifest.Name != "HomeAtlas" || manifest.Display != "standalone" || len(manifest.Icons) != 2 {
		t.Fatal(manifest)
	}
	for i, icon := range manifest.Icons {
		w := perform(f.app, "GET", icon.Src, nil, nil)
		if w.Code != 200 {
			t.Fatal("missing install icon", w.Code)
		}
		config, err := png.DecodeConfig(bytes.NewReader(w.Body.Bytes()))
		size := 192
		if i == 1 {
			size = 512
		}
		if err != nil || config.Width != size || config.Height != size {
			t.Fatal("icon dimensions", config, err)
		}
	}
	for _, path := range []string{"/api/auth/profile", "/api/auth/sessions"} {
		if w := perform(f.app, "GET", path, nil, nil); w.Code != 401 {
			t.Fatal("account data exposed", path, w.Code)
		}
		if w := perform(f.app, "GET", path, nil, f.cookie); w.Code != 200 || !strings.Contains(w.Header().Get("Cache-Control"), "no-store") {
			t.Fatal("account response can be cached", path, w.Code, w.Header())
		}
	}
}
