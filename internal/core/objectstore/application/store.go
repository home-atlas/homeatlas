// Package objectstore keeps file locations and credentials in trusted host state.
package application

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"time"

	"github.com/home-atlas/homeatlas/internal/core/objectstore/domain"
	"github.com/home-atlas/homeatlas/internal/core/unitofwork"
)

type Store struct {
	repository Repository
	remote     Remote
	cipher     Cipher
}

func New(repository Repository, remote Remote, cipher Cipher) *Store {
	return &Store{repository: repository, remote: remote, cipher: cipher}
}

func (s *Store) Profile(ctx context.Context, ID string) (string, S3Config, error) {
	house, sealed, err := s.repository.Profile(ctx, ID)
	if err != nil {
		return house, S3Config{}, err
	}
	return s.decodeProfile(house, ID, sealed)
}
func (s *Store) decodeProfile(house, ID string, sealed []byte) (string, S3Config, error) {
	var config S3Config
	raw, err := s.cipher.Crypt(house, ID, sealed, false)
	if err != nil {
		return house, config, err
	}
	if err = json.Unmarshal(raw, &config); err != nil {
		return house, config, Unavailable
	}
	if err = domain.Validate(config); err != nil {
		return house, config, Unavailable
	}
	return house, config, nil
}
func (s *Store) current(ctx context.Context, house string) (Settings, string, error) {
	result := Settings{Provider: "local", S3: S3Config{Region: "us-east-1", PathStyle: true}}
	ID, revision, err := s.repository.Settings(ctx, nil, house)
	result.Revision = revision
	if errors.Is(err, unitofwork.ErrNotFound) {
		return result, "", nil
	}
	if err != nil {
		return result, "", err
	}
	if ID != "" {
		h, c, err := s.Profile(ctx, ID)
		if err != nil {
			return result, "", err
		}
		if h != house {
			return result, "", Unavailable
		}
		result.Provider = "s3"
		result.S3 = c
		result.CredentialsConfigured = true
	}
	return result, ID, nil
}
func (s *Store) Settings(ctx context.Context, house string) (Settings, error) {
	result, _, err := s.current(ctx, house)
	result.S3.AccessKey = ""
	result.S3.SecretKey = ""
	return result, err
}

// Save tests new S3 credentials before publishing; authorize runs again in the commit transaction.
func (s *Store) Save(ctx context.Context, house string, input Settings, authorize func(unitofwork.Transaction) error) error {
	if input.Provider != "local" && input.Provider != "s3" {
		return Invalid
	}
	current, ID, err := s.current(ctx, house)
	if err != nil {
		return err
	}
	if current.Revision != input.Revision {
		return Conflict
	}
	var sealed []byte
	refresh := map[string][]byte{}
	if input.Provider == "local" {
		ID = ""
	} else {
		c := input.S3
		c.Endpoint = strings.TrimRight(strings.TrimSpace(c.Endpoint), "/")
		c.Prefix = strings.Trim(c.Prefix, "/")
		if c.AccessKey == "" && c.SecretKey == "" && current.Provider == "s3" {
			old := current.S3
			if !domain.SameDestination(c, old) {
				return Invalid
			}
			c.AccessKey = old.AccessKey
			c.SecretKey = old.SecretKey
		}
		if err = domain.Validate(c); err != nil {
			return err
		}
		probeCtx, cancel := context.WithTimeout(ctx, 25*time.Second)
		defer cancel()
		if err = s.remote.Probe(probeCtx, house, c); err != nil {
			return Unavailable
		}
		// Refresh credentials for prior profiles of the same destination, preserving object locations.
		oldProfiles, readErr := s.repository.Profiles(ctx, house)
		if readErr != nil {
			return readErr
		}
		for Profile, oldSealed := range oldProfiles {
			raw, openErr := s.cipher.Crypt(house, Profile, oldSealed, false)
			if openErr != nil {
				return openErr
			}
			var old S3Config
			if json.Unmarshal(raw, &old) != nil {
				return Unavailable
			}
			if domain.SameDestination(old, c) {
				raw, _ = json.Marshal(c)
				updated, sealErr := s.cipher.Crypt(house, Profile, raw, true)
				if sealErr != nil {
					return sealErr
				}
				refresh[Profile] = updated
			}
		}
		ID = rand.Text()
		raw, _ := json.Marshal(c)
		sealed, err = s.cipher.Crypt(house, ID, raw, true)
		if err != nil {
			return err
		}
	}
	tx, err := s.repository.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = authorize(tx); err != nil {
		return err
	}
	_, fresh, err := s.repository.Settings(ctx, tx, house)
	if err != nil && !errors.Is(err, unitofwork.ErrNotFound) {
		return err
	}
	if fresh != input.Revision {
		return Conflict
	}
	if err = s.repository.SaveSettings(ctx, tx, house, ID, sealed, refresh, rand.Text()); err != nil {
		return err
	}
	return tx.Commit()
}

// Pending is published alongside collection metadata, so failed uploads have no live locator.
type Pending struct {
	store *Store
	domain.Location
	config S3Config
}

func (p *Pending) Publish(ctx context.Context, tx unitofwork.Transaction) error {
	return p.store.repository.Publish(ctx, tx, p.Location)
}
func (p *Pending) Abort() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	p.store.remote.Delete(ctx, p.config, p.Object)
}
func (s *Store) Upload(ctx context.Context, house, Module, Key string, file *os.File, Size int64, Digest string) (*Pending, error) {
	if !domain.SafeID.MatchString(house) || !domain.SafeID.MatchString(Module) || !domain.SafeID.MatchString(Key) || Size < 1 || Size > 20<<20 {
		return nil, Invalid
	}
	current, ID, err := s.current(ctx, house)
	if err != nil {
		return nil, err
	}
	if ID == "" {
		return nil, nil
	}
	p := &Pending{store: s, Location: domain.Location{Profile: ID, Module: Module, Key: Key, Object: domain.ObjectKey(current.S3, house, Module, Key), Size: Size, Digest: Digest}, config: current.S3}
	if _, err = file.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	if err = s.remote.Put(ctx, current.S3, p.Object, file, Size); err != nil {
		p.Abort()
		return nil, Unavailable
	}
	return p, nil
}

// Open downloads remote originals into a bounded private temporary file for ranges and backups.
// An absent locator means the original remains in its existing local collection directory.
func (s *Store) Open(ctx context.Context, Module, Key string) (*os.File, func(), error) {
	location, err := s.repository.Location(ctx, Module, Key)
	if err != nil {
		return nil, func() {}, err
	}
	_, c, err := s.Profile(ctx, location.Profile)
	if err != nil {
		return nil, func() {}, Unavailable
	}
	return s.openLocation(ctx, location, c)
}
func (s *Store) openLocation(ctx context.Context, location Location, c S3Config) (*os.File, func(), error) {
	if location.Size < 1 || location.Size > 20<<20 {
		return nil, func() {}, Unavailable
	}
	return s.remote.Fetch(ctx, c, location.Object, location.Size, location.Digest)
}

func (s *Store) OpenSnapshot(ctx context.Context, location domain.Location, house, Profile string, sealed []byte) (*os.File, func(), error) {
	_, config, err := s.decodeProfile(house, Profile, sealed)
	if err != nil {
		return nil, func() {}, Unavailable
	}
	return s.openLocation(ctx, location, config)
}
