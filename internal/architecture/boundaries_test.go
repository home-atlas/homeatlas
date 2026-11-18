package architecture_test

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"io/fs"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

func internalRoot() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Dir(filepath.Dir(file))
}
func layer(path string) string {
	for _, name := range []string{"domain", "application"} {
		if strings.Contains(path, "/"+name+"/") {
			return name
		}
	}
	return ""
}
func forbiddenDependency(path string, kind string) bool {
	if path == "database/sql" || path == "net/http" || path == "github.com/home-atlas/homeatlas/internal/core/database" || strings.HasPrefix(path, "github.com/aws/") || strings.HasPrefix(path, "github.com/tetratelabs/wazero") {
		return true
	}
	if strings.Contains(path, "/adapters/") {
		return true
	}
	if kind == "domain" && strings.Contains(path, "/application") {
		return true
	}
	return false
}

// Package boundaries, not filename suffixes, enforce the dependency direction.
func TestLayerDependencies(t *testing.T) {
	cmd := exec.Command("go", "list", "-deps", "-json", "./internal/core/...")
	cmd.Dir = filepath.Dir(internalRoot())
	output, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	type pkg struct {
		ImportPath    string
		Imports, Deps []string
	}
	decoder := json.NewDecoder(strings.NewReader(string(output)))
	checked := 0
	for {
		var p pkg
		err := decoder.Decode(&p)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(p.ImportPath, "github.com/home-atlas/homeatlas/internal/core/") {
			continue
		}
		kind := layer(p.ImportPath + "/")
		if kind == "" {
			continue
		}
		checked++
		for _, dependency := range p.Deps {
			if forbiddenDependency(dependency, kind) {
				t.Errorf("%s depends on %s", p.ImportPath, dependency)
			}
		}
		for _, dependency := range p.Imports {
			if strings.HasPrefix(dependency, "github.com/home-atlas/homeatlas/internal/core/") && layer(dependency+"/") == "" {
				switch dependency {
				case "github.com/home-atlas/homeatlas/internal/core/access", "github.com/home-atlas/homeatlas/internal/core/fault", "github.com/home-atlas/homeatlas/internal/core/unitofwork", "github.com/home-atlas/homeatlas/internal/core/backup/stream":
				default:
					t.Errorf("%s imports composition/infrastructure package %s", p.ImportPath, dependency)
				}
			}
		}
	}
	if checked < 10 {
		t.Fatalf("layer packages missing from dependency validation: %d", checked)
	}
}

func TestPersistenceBoundaries(t *testing.T) {
	root := internalRoot()
	composition := map[string]bool{"app/app.go": true, "app/builtin_modules.go": true, "core/backup/provider.go": true, "core/collections/collections.go": true, "core/identity/identity.go": true, "core/modules/modules.go": true, "core/objectstore/objectstore.go": true, "core/plugins/plugins.go": true}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "testdata" || d.Name() == "testsupport" {
				return filepath.SkipDir
			}
			return nil
		}
		relative, _ := filepath.Rel(root, path)
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		adapter := strings.Contains(relative, "/adapters/") || strings.HasPrefix(relative, "core/database/") || strings.HasPrefix(relative, "app/") && strings.HasSuffix(relative, "_sql.go")
		tree, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			return err
		}
		for _, i := range tree.Imports {
			value, _ := strconv.Unquote(i.Path.Value)
			if value == "database/sql" && !adapter && !composition[relative] {
				t.Errorf("%s imports SQL outside an adapter/composition", relative)
			}
		}
		if adapter {
			return nil
		}
		ast.Inspect(tree, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			switch selector.Sel.Name {
			case "QueryContext", "QueryRowContext", "ExecContext", "BeginTx", "QueryRow", "Exec":
				t.Errorf("%s calls SQL outside an adapter", relative)
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
