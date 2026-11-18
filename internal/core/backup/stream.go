package backup

import (
	"github.com/home-atlas/homeatlas/internal/core/backup/stream"
	"io"
	"os"
)

func DiscardCache(f *os.File)               { stream.DiscardCache(f) }
func StreamReader(f *os.File) io.ReadSeeker { return stream.StreamReader(f) }
func StreamWriter(f *os.File) io.Writer     { return stream.StreamWriter(f) }
