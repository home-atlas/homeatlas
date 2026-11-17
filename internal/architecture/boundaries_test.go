package architecture_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// SQL codecs, migrations and composition are deliberately allowed adapters.
// Application/HTTP code must use typed records and the shared transaction port.
func TestPersistenceBoundaries(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	root := filepath.Dir(filepath.Dir(file))
	exceptions := map[string]bool{"app/app.go": true, "app/builtin_modules.go": true, "core/backup/provider.go": true}
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
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") || strings.HasSuffix(path, "_sql.go") || strings.HasPrefix(relative, "core/database/") {
			return nil
		}
		tree, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			return err
		}
		for _, i := range tree.Imports {
			value, _ := strconv.Unquote(i.Path.Value)
			if value == "database/sql" && !exceptions[relative] {
				t.Errorf("%s imports SQL outside an adapter", relative)
			}
			if value == "net/http" && strings.HasPrefix(relative, "core/identity/") && relative != "core/identity/http.go" {
				t.Errorf("%s couples identity policy to HTTP", relative)
			}
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
