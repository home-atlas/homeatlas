package app

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"io"
	"os"

	"github.com/home-atlas/homeatlas/internal/core/access"
)

var errScheduleKey = errors.New("backup schedule key unavailable")

// Only automation stores a recoverable password. The wrapping key is private installation state, never in a household export.
func (b *householdBackups) scheduleWrappingKey(create bool) ([]byte, error) {
	root, err := os.OpenRoot(b.dataDir)
	if err != nil {
		return nil, errScheduleKey
	}
	defer root.Close()
	f, err := root.Open("backup-schedule.key")
	if os.IsNotExist(err) && create {
		f, err = root.OpenFile("backup-schedule.key", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return nil, errScheduleKey
		}
		key := make([]byte, 32)
		rand.Read(key)
		_, writeErr := f.Write(key)
		syncErr := f.Sync()
		closeErr := f.Close()
		if writeErr != nil || syncErr != nil || closeErr != nil {
			root.Remove("backup-schedule.key")
			return nil, errScheduleKey
		}
		if err = syncDirectory(b.dataDir); err != nil {
			return nil, errScheduleKey
		}
		return key, nil
	}
	if err != nil {
		return nil, errScheduleKey
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return nil, errScheduleKey
	}
	key, err := io.ReadAll(io.LimitReader(f, 33))
	if err != nil || len(key) != 32 {
		return nil, errScheduleKey
	}
	return key, nil
}
func scheduleAAD(p access.Principal) []byte {
	return []byte("homeatlas-backup-schedule/v1/" + p.HouseholdID + "/" + p.UserID + "/" + p.MembershipID)
}
func (b *householdBackups) sealSchedulePassword(p access.Principal, password string) ([]byte, error) {
	key, err := b.scheduleWrappingKey(true)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, errScheduleKey
	}
	aead, err := cipher.NewGCMWithRandomNonce(block)
	if err != nil {
		return nil, errScheduleKey
	}
	return aead.Seal(nil, nil, []byte(password), scheduleAAD(p)), nil
}
func (b *householdBackups) openSchedulePassword(p access.Principal, sealed []byte) (string, error) {
	key, err := b.scheduleWrappingKey(false)
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", errScheduleKey
	}
	aead, err := cipher.NewGCMWithRandomNonce(block)
	if err != nil {
		return "", errScheduleKey
	}
	password, err := aead.Open(nil, nil, sealed, scheduleAAD(p))
	if err != nil || len(password) > 256 || requireBackupPassword(string(password)) != nil {
		return "", errScheduleKey
	}
	return string(password), nil
}
