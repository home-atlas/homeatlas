package app

import (
	"bufio"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"unicode/utf8"

	"filippo.io/age"
	"github.com/home-atlas/homeatlas/internal/core/backup"
)

const backupWorkFactor = 18 // scrypt N=2^18, r=8, p=1; about 256 MiB, bounded for untrusted imports.
var backupCryptoSlot = make(chan struct{}, 1)

type backupResourcesKey struct{}

func withBackupResources(ctx context.Context, pause func() func()) context.Context {
	return context.WithValue(ctx, backupResourcesKey{}, pause)
}

func acquireBackupResources(ctx context.Context) func() {
	if pause, ok := ctx.Value(backupResourcesKey{}).(func() func()); ok {
		return pause()
	}
	return func() {}
}

func releaseBackupCryptoSlot() {
	// scrypt's 256 MiB workspace is no longer needed. Return it before the next
	// operation can allocate another one, rather than waiting for automatic GC.
	debug.FreeOSMemory()
	<-backupCryptoSlot
}

const agePrefix = "age-encryption.org/v1\n"

var errBackupPasswordRequired = errors.New("backup password required")
var errBackupPasswordInvalid = errors.New("invalid backup password")
var errBackupDecrypt = errors.New("backup password incorrect or ciphertext damaged")
var errBackupTooLarge = errors.New("backup too large")

// Base64 transports UTF-8 passwords in HTTP headers without trimming or normalization.
func backupPassword(r *http.Request) (string, error) {
	value := r.Header.Get("X-HomeAtlas-Backup-Password")
	if value == "" {
		return "", nil
	}
	if len(value) > 344 {
		return "", errBackupPasswordInvalid
	}
	raw, err := base64.StdEncoding.Strict().DecodeString(value)
	if err != nil || len(raw) == 0 || len(raw) > 256 || !utf8.Valid(raw) {
		return "", errBackupPasswordInvalid
	}
	return string(raw), nil
}
func requireBackupPassword(password string) error {
	if password == "" {
		return errBackupPasswordRequired
	}
	if utf8.RuneCountInString(password) < 12 || strings.TrimSpace(password) == "" {
		return errBackupPasswordInvalid
	}
	return nil
}

type backupContextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r backupContextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(p)
}

func encryptBackup(ctx context.Context, path, encrypted, password string) (string, error) {
	if err := requireBackupPassword(password); err != nil {
		return "", err
	}
	select {
	case backupCryptoSlot <- struct{}{}:
	case <-ctx.Done():
		return "", ctx.Err()
	}
	resume := acquireBackupResources(ctx)
	defer func() { releaseBackupCryptoSlot(); resume() }()
	src, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer src.Close()
	defer backup.DiscardCache(src)
	recipient, err := age.NewScryptRecipient(password)
	if err != nil {
		return "", err
	}
	recipient.SetWorkFactor(backupWorkFactor)
	dst, err := os.OpenFile(encrypted, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return "", err
	}
	defer dst.Close()
	complete := false
	defer func() {
		if !complete {
			os.Remove(encrypted)
		}
	}()
	stream, err := age.Encrypt(backup.StreamWriter(dst), recipient)
	// Drop the KDF workspace before streaming can allocate or fill the file cache.
	debug.FreeOSMemory()
	if err != nil {
		return "", err
	}
	if _, err = io.Copy(stream, backupContextReader{ctx, backup.StreamReader(src)}); err != nil {
		return "", err
	}
	if err = stream.Close(); err != nil {
		return "", err
	}
	if err = dst.Sync(); err != nil {
		return "", err
	}
	backup.DiscardCache(dst)
	info, err := dst.Stat()
	if err != nil {
		return "", err
	}
	if info.Size() > maxBackupBytes {
		return "", errBackupTooLarge
	}
	if err = dst.Close(); err != nil {
		return "", err
	}
	complete = true
	return encrypted, nil
}

// Authenticate the entire stream, including its final chunk, before ZIP validation or live writes.
func decryptBackup(ctx context.Context, path, dir, password string, allowLegacy bool) (string, error) {
	src, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer src.Close()
	defer backup.DiscardCache(src)
	reader := bufio.NewReader(backupContextReader{ctx, backup.StreamReader(src)})
	prefix, _ := reader.Peek(4)
	if string(prefix) == "PK\x03\x04" {
		if !allowLegacy {
			return "", errBackupDecrypt
		}
		return path, nil // Explicit historical v1/v2 ZIP import.
	}
	if password == "" {
		return "", errBackupPasswordRequired
	}
	select {
	case backupCryptoSlot <- struct{}{}:
	case <-ctx.Done():
		return "", ctx.Err()
	}
	resume := acquireBackupResources(ctx)
	defer func() { releaseBackupCryptoSlot(); resume() }()
	identity, err := age.NewScryptIdentity(password)
	if err != nil {
		return "", errBackupDecrypt
	}
	identity.SetMaxWorkFactor(backupWorkFactor)
	stream, err := age.Decrypt(reader, identity)
	debug.FreeOSMemory()
	if err != nil {
		return "", errBackupDecrypt
	}
	decrypted := filepath.Join(dir, "decrypted.zip")
	dst, err := os.OpenFile(decrypted, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return "", err
	}
	defer dst.Close()
	complete := false
	defer func() {
		if !complete {
			os.Remove(decrypted)
		}
	}()
	n, err := io.Copy(backup.StreamWriter(dst), io.LimitReader(stream, maxBackupBytes+1))
	if err != nil {
		return "", errBackupDecrypt
	}
	if n > maxBackupBytes {
		return "", errBackupTooLarge
	}
	if err = dst.Sync(); err != nil {
		return "", err
	}
	backup.DiscardCache(dst)
	if err = dst.Close(); err != nil {
		return "", err
	}
	complete = true
	return decrypted, nil
}

func receiveBackup(w http.ResponseWriter, r *http.Request, dir string) (string, string, error) {
	password, err := backupPassword(r)
	if err != nil {
		return "", "", err
	}
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || (media != "application/zip" && media != "application/octet-stream") {
		return "", "", errInvalidBackup
	}
	if r.ContentLength > maxBackupBytes {
		return "", "", errBackupTooLarge
	}
	path := filepath.Join(dir, "incoming.backup")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return "", "", err
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBackupBytes)
	_, err = io.Copy(backup.StreamWriter(f), backupContextReader{r.Context(), r.Body})
	var syncErr error
	if err == nil {
		syncErr = f.Sync()
		backup.DiscardCache(f)
	}
	closeErr := f.Close()
	if err != nil {
		var limit *http.MaxBytesError
		if errors.As(err, &limit) {
			return "", "", errBackupTooLarge
		}
		return "", "", errInvalidBackup
	}
	if syncErr != nil {
		return "", "", syncErr
	}
	if closeErr != nil {
		return "", "", closeErr
	}
	path, err = decryptBackup(r.Context(), path, dir, password, r.Header.Get("X-HomeAtlas-Allow-Legacy-Backup") == "true")
	return path, password, err
}
