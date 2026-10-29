package backup

import (
	"bytes"
	"io"
	"os"
	"testing"
)

func TestStreamWriterReportsCheckpointSyncError(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()
	done := make(chan struct{})
	go func() {
		io.Copy(io.Discard, r)
		close(done)
	}()
	// Pipe writes succeed, but the checkpoint cannot sync a pipe to disk.
	_, err = io.Copy(StreamWriter(w), bytes.NewReader(make([]byte, 16<<20)))
	if err == nil {
		t.Error("checkpoint sync failure was hidden")
	}
	w.Close()
	<-done
}
