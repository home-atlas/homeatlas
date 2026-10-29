package app

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"testing"

	"filippo.io/age"
)

type backupMemoryProbe struct {
	context.Context
	peak uint64
}

func (p *backupMemoryProbe) Err() error {
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	if used := stats.HeapSys - stats.HeapReleased; used > p.peak {
		p.peak = used
	}
	return p.Context.Err()
}

func TestBackupCryptoReleasesWorkspace(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "source.zip")
	content := bytes.Repeat([]byte("private backup memory regression fixture"), 4096)
	if err := os.WriteFile(source, content, 0600); err != nil {
		t.Fatal(err)
	}
	debug.FreeOSMemory()
	var baseline runtime.MemStats
	runtime.ReadMemStats(&baseline)
	checkReleased := func() {
		t.Helper()
		var after runtime.MemStats
		runtime.ReadMemStats(&after)
		// Check heap not returned to the OS, without triggering GC in the assertion.
		beforeBytes := baseline.HeapSys - baseline.HeapReleased
		afterBytes := after.HeapSys - after.HeapReleased
		t.Logf("heap not released: before=%d after=%d; allocated=%d GC=%d", beforeBytes, afterBytes, after.HeapAlloc, after.NumGC)
		if afterBytes > beforeBytes+128<<20 {
			t.Fatalf("scrypt workspace retained: %d bytes above baseline", afterBytes-beforeBytes)
		}
	}
	password := "Backup memory regression password 2026"
	for i := 0; i < 3; i++ {
		probe := &backupMemoryProbe{Context: context.Background()}
		encrypted := filepath.Join(t.TempDir(), "backup.age")
		if _, err := encryptBackup(probe, source, encrypted, password); err != nil {
			t.Fatal(err)
		}
		checkReleased()
		ciphertext, err := os.ReadFile(encrypted)
		if err != nil || !bytes.Contains(ciphertext, []byte(" 18\n")) {
			t.Fatal("backup must retain scrypt work factor 18", err)
		}
		if _, err := decryptBackup(context.Background(), encrypted, t.TempDir(), "incorrect password", false); err != errBackupDecrypt {
			t.Fatalf("incorrect password accepted: %v", err)
		}
		checkReleased()
		plain, err := decryptBackup(probe, encrypted, t.TempDir(), password, false)
		if err != nil {
			t.Fatal(err)
		}
		checkReleased()
		if probe.peak > baseline.HeapSys-baseline.HeapReleased+128<<20 {
			t.Fatalf("KDF workspace retained during streaming: %d bytes", probe.peak)
		}
		restored, err := os.ReadFile(plain)
		if err != nil || !bytes.Equal(restored, content) {
			t.Fatal("backup round trip changed content", err)
		}
	}
}

// Measure the full cleanup (GC plus OS reclamation), excluding scrypt itself.
func BenchmarkBackupCryptoCleanup(b *testing.B) {
	b.StopTimer()
	for i := 0; i < b.N; i++ {
		recipient, err := age.NewScryptRecipient("Disposable cleanup benchmark password")
		if err != nil {
			b.Fatal(err)
		}
		recipient.SetWorkFactor(backupWorkFactor)
		backupCryptoSlot <- struct{}{}
		stream, err := age.Encrypt(io.Discard, recipient)
		if err != nil {
			releaseBackupCryptoSlot()
			b.Fatal(err)
		}
		if err := stream.Close(); err != nil {
			releaseBackupCryptoSlot()
			b.Fatal(err)
		}
		b.StartTimer()
		releaseBackupCryptoSlot()
		b.StopTimer()
	}
}
