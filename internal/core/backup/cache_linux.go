package backup

import (
	"io"
	"os"

	"golang.org/x/sys/unix"
)

// DiscardCache advises the kernel that backup streaming no longer needs these
// pages. Writers must sync first; this never removes file contents. Unsupported
// filesystems may ignore the hint without affecting backup correctness.
func DiscardCache(f *os.File) {
	_ = unix.Fadvise(int(f.Fd()), 0, 0, unix.FADV_DONTNEED)
}

const cacheWindow = 8 << 20

// StreamWriter bounds dirty backup cache before the next scrypt allocation.
// The caller must still sync the final partial window before publishing.
func StreamWriter(f *os.File) io.Writer { return &cacheWriter{file: f} }

type cacheWriter struct {
	file    *os.File
	pending int
}

func (w *cacheWriter) Write(p []byte) (int, error) {
	n, err := w.file.Write(p)
	w.pending += n
	if err == nil && w.pending >= cacheWindow {
		err = w.file.Sync()
		if err == nil {
			DiscardCache(w.file)
			w.pending = 0
		}
	}
	return n, err
}

// StreamReader discards consumed file pages during backup transfer, while
// retaining Seek for HTTP range requests and content detection.
func StreamReader(f *os.File) io.ReadSeeker { return &cacheReader{file: f} }

type cacheReader struct {
	file    *os.File
	pending int
}

func (r *cacheReader) Read(p []byte) (int, error) {
	n, err := r.file.Read(p)
	r.pending += n
	if r.pending >= cacheWindow {
		DiscardCache(r.file)
		r.pending = 0
	}
	return n, err
}

func (r *cacheReader) Seek(offset int64, whence int) (int64, error) {
	r.pending = 0
	return r.file.Seek(offset, whence)
}
