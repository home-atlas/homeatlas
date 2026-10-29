//go:build !linux

package backup

import (
	"io"
	"os"
)

// DiscardCache is optional platform advice; other systems keep their cache policy.
func DiscardCache(f *os.File) {}

func StreamWriter(f *os.File) io.Writer     { return f }
func StreamReader(f *os.File) io.ReadSeeker { return f }
