package localcrypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"io"
	"os"
	"sync"

	"github.com/home-atlas/homeatlas/internal/core/objectstore/domain"
)

type Cipher struct {
	dir string
	mu  sync.Mutex
}

func New(dir string) *Cipher { return &Cipher{dir: dir} }
func (s *Cipher) key(create bool) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	root, err := os.OpenRoot(s.dir)
	if err != nil {
		return nil, domain.Unavailable
	}
	defer root.Close()
	f, err := root.Open("file-storage.key")
	if os.IsNotExist(err) && create {
		f, err = root.OpenFile("file-storage.key", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return nil, domain.Unavailable
		}
		key := make([]byte, 32)
		rand.Read(key)
		_, e1 := f.Write(key)
		e2 := f.Sync()
		e3 := f.Close()
		if e1 != nil || e2 != nil || e3 != nil {
			root.Remove("file-storage.key")
			return nil, domain.Unavailable
		}
		directory, e := root.Open(".")
		if e != nil {
			return nil, domain.Unavailable
		}
		e = directory.Sync()
		directory.Close()
		if e != nil {
			return nil, domain.Unavailable
		}
		return key, nil
	}
	if err != nil {
		return nil, domain.Unavailable
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return nil, domain.Unavailable
	}
	key, err := io.ReadAll(io.LimitReader(f, 33))
	if err != nil || len(key) != 32 {
		return nil, domain.Unavailable
	}
	return key, nil
}
func (s *Cipher) Crypt(house, id string, raw []byte, seal bool) ([]byte, error) {
	key, err := s.key(seal)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, domain.Unavailable
	}
	aead, err := cipher.NewGCMWithRandomNonce(block)
	if err != nil {
		return nil, domain.Unavailable
	}
	aad := []byte("homeatlas-file-storage/v1/" + house + "/" + id)
	if seal {
		return aead.Seal(nil, nil, raw, aad), nil
	}
	raw, err = aead.Open(nil, nil, raw, aad)
	if err != nil {
		return nil, domain.Unavailable
	}
	return raw, nil
}
