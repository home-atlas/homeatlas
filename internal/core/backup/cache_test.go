package backup

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestBackupStreamsPreserveBytesSeekAndWriteErrors(t *testing.T) {
	f, err := os.OpenFile(filepath.Join(t.TempDir(), "stream"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	block := bytes.Repeat([]byte("backup-stream-block"), 4096)
	w := StreamWriter(f)
	for i := 0; i < 256; i++ {
		if n, err := w.Write(block); err != nil || n != len(block) {
			t.Fatal(n, err)
		}
	}
	if err := f.Sync(); err != nil {
		t.Fatal(err)
	}
	DiscardCache(f)
	r := StreamReader(f)
	if _, err := r.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, len(block))
	for i := 0; i < 256; i++ {
		if _, err := io.ReadFull(r, got); err != nil || !bytes.Equal(got, block) {
			t.Fatal("stream changed data", err)
		}
	}
	if offset, err := r.Seek(7, io.SeekStart); err != nil || offset != 7 {
		t.Fatal("range seek failed", offset, err)
	}
	if _, err := io.ReadFull(r, got[:15]); err != nil || !bytes.Equal(got[:15], block[7:22]) {
		t.Fatal("range read failed", err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(block); err == nil {
		t.Fatal("write error was hidden")
	}
}
