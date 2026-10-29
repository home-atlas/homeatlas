//go:build !production

package web

import (
	"io/fs"
	"os"
)

// FS serves a local build during development; Vite handles hot reload separately.
var FS fs.FS = os.DirFS("apps/web/dist")
