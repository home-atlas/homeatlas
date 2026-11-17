// Package objectstore keeps file locations and credentials in trusted host state.
package objectstore

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/home-atlas/homeatlas/internal/core/unitofwork"
)

var Invalid = errors.New("invalid storage configuration")
var Conflict = errors.New("storage configuration changed")
var Unavailable = errors.New("storage unavailable")
var safeID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
var bucketName = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$`)
var regionName = regexp.MustCompile(`^[a-z0-9-]{1,63}$`)
var safePrefix = regexp.MustCompile(`^[A-Za-z0-9/_-]{0,200}$`)

type S3Config struct {
	Endpoint  string `json:"endpoint"`
	Region    string `json:"region"`
	Bucket    string `json:"bucket"`
	Prefix    string `json:"prefix"`
	PathStyle bool   `json:"pathStyle"`
	AccessKey string `json:"accessKey,omitempty"`
	SecretKey string `json:"secretKey,omitempty"`
}
type Settings struct {
	Provider              string   `json:"provider"`
	Revision              string   `json:"revision"`
	S3                    S3Config `json:"s3"`
	CredentialsConfigured bool     `json:"credentialsConfigured"`
}
type Store struct {
	repository *sqliteRepository
	dir        string
	mu         sync.Mutex
	// HTTPClient is injectable for TLS integration tests; defaults never follow redirects.
	HTTPClient aws.HTTPClient
}

func validate(c S3Config) error {
	if !regionName.MatchString(c.Region) || !bucketName.MatchString(c.Bucket) || strings.Contains(c.Bucket, "..") || !safePrefix.MatchString(c.Prefix) || strings.Contains(c.Prefix, "//") || len(c.AccessKey) < 1 || len(c.AccessKey) > 256 || len(c.SecretKey) < 1 || len(c.SecretKey) > 512 {
		return Invalid
	}
	if strings.ContainsAny(c.AccessKey+c.SecretKey, "\r\n\x00") {
		return Invalid
	}
	if c.Endpoint != "" {
		u, err := url.Parse(c.Endpoint)
		if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
			return Invalid
		}
	}
	return nil
}
func (s *Store) key(create bool) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	root, err := os.OpenRoot(s.dir)
	if err != nil {
		return nil, Unavailable
	}
	defer root.Close()
	f, err := root.Open("file-storage.key")
	if os.IsNotExist(err) && create {
		f, err = root.OpenFile("file-storage.key", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return nil, Unavailable
		}
		key := make([]byte, 32)
		rand.Read(key)
		_, e1 := f.Write(key)
		e2 := f.Sync()
		e3 := f.Close()
		if e1 != nil || e2 != nil || e3 != nil {
			root.Remove("file-storage.key")
			return nil, Unavailable
		}
		directory, e := root.Open(".")
		if e != nil {
			return nil, Unavailable
		}
		e = directory.Sync()
		directory.Close()
		if e != nil {
			return nil, Unavailable
		}
		return key, nil
	}
	if err != nil {
		return nil, Unavailable
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return nil, Unavailable
	}
	key, err := io.ReadAll(io.LimitReader(f, 33))
	if err != nil || len(key) != 32 {
		return nil, Unavailable
	}
	return key, nil
}
func (s *Store) crypt(house, id string, raw []byte, seal bool) ([]byte, error) {
	key, err := s.key(seal)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, Unavailable
	}
	aead, err := cipher.NewGCMWithRandomNonce(block)
	if err != nil {
		return nil, Unavailable
	}
	aad := []byte("homeatlas-file-storage/v1/" + house + "/" + id)
	if seal {
		return aead.Seal(nil, nil, raw, aad), nil
	}
	raw, err = aead.Open(nil, nil, raw, aad)
	if err != nil {
		return nil, Unavailable
	}
	return raw, nil
}
func (s *Store) profile(ctx context.Context, id string) (string, S3Config, error) {
	house, sealed, err := s.repository.profile(ctx, id)
	if err != nil {
		return house, S3Config{}, err
	}
	return s.decodeProfile(house, id, sealed)
}
func (s *Store) decodeProfile(house, id string, sealed []byte) (string, S3Config, error) {
	var config S3Config
	raw, err := s.crypt(house, id, sealed, false)
	if err != nil {
		return house, config, err
	}
	if err = json.Unmarshal(raw, &config); err != nil {
		return house, config, Unavailable
	}
	if err = validate(config); err != nil {
		return house, config, Unavailable
	}
	return house, config, nil
}
func (s *Store) current(ctx context.Context, house string) (Settings, string, error) {
	result := Settings{Provider: "local", S3: S3Config{Region: "us-east-1", PathStyle: true}}
	id, revision, err := s.repository.settings(ctx, nil, house)
	result.Revision = revision
	if errors.Is(err, unitofwork.ErrNotFound) {
		return result, "", nil
	}
	if err != nil {
		return result, "", err
	}
	if id != "" {
		h, c, err := s.profile(ctx, id)
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
	return result, id, nil
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
	current, id, err := s.current(ctx, house)
	if err != nil {
		return err
	}
	if current.Revision != input.Revision {
		return Conflict
	}
	var sealed []byte
	refresh := map[string][]byte{}
	if input.Provider == "local" {
		id = ""
	} else {
		c := input.S3
		c.Endpoint = strings.TrimRight(strings.TrimSpace(c.Endpoint), "/")
		c.Prefix = strings.Trim(c.Prefix, "/")
		if c.AccessKey == "" && c.SecretKey == "" && current.Provider == "s3" {
			old := current.S3
			if !sameDestination(c, old) {
				return Invalid
			}
			c.AccessKey = old.AccessKey
			c.SecretKey = old.SecretKey
		}
		if err = validate(c); err != nil {
			return err
		}
		probeCtx, cancel := context.WithTimeout(ctx, 25*time.Second)
		defer cancel()
		if err = s.probe(probeCtx, house, c); err != nil {
			return Unavailable
		}
		// Refresh credentials for prior profiles of the same destination, preserving object locations.
		oldProfiles, readErr := s.repository.profiles(ctx, house)
		if readErr != nil {
			return readErr
		}
		for profile, oldSealed := range oldProfiles {
			raw, openErr := s.crypt(house, profile, oldSealed, false)
			if openErr != nil {
				return openErr
			}
			var old S3Config
			if json.Unmarshal(raw, &old) != nil {
				return Unavailable
			}
			if sameDestination(old, c) {
				raw, _ = json.Marshal(c)
				updated, sealErr := s.crypt(house, profile, raw, true)
				if sealErr != nil {
					return sealErr
				}
				refresh[profile] = updated
			}
		}
		id = rand.Text()
		raw, _ := json.Marshal(c)
		sealed, err = s.crypt(house, id, raw, true)
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
	_, fresh, err := s.repository.settings(ctx, tx, house)
	if err != nil && !errors.Is(err, unitofwork.ErrNotFound) {
		return err
	}
	if fresh != input.Revision {
		return Conflict
	}
	if err = s.repository.saveSettings(ctx, tx, house, id, sealed, refresh, rand.Text()); err != nil {
		return err
	}
	return tx.Commit()
}

// Pending is published alongside collection metadata, so failed uploads have no live locator.
type Pending struct {
	store                        *Store
	profile, module, key, object string
	size                         int64
	digest                       string
	config                       S3Config
}

func (p *Pending) Publish(ctx context.Context, tx unitofwork.Transaction) error {
	return p.store.repository.publish(ctx, tx, p)
}
func (p *Pending) Abort() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	p.store.deleteObject(ctx, p.config, p.object)
}
func (s *Store) Upload(ctx context.Context, house, module, key string, file *os.File, size int64, digest string) (*Pending, error) {
	if !safeID.MatchString(house) || !safeID.MatchString(module) || !safeID.MatchString(key) || size < 1 || size > 20<<20 {
		return nil, Invalid
	}
	current, id, err := s.current(ctx, house)
	if err != nil {
		return nil, err
	}
	if id == "" {
		return nil, nil
	}
	p := &Pending{store: s, profile: id, module: module, key: key, object: objectKey(current.S3, house, module, key), size: size, digest: digest, config: current.S3}
	if _, err = file.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	if err = s.put(ctx, current.S3, p.object, file, size); err != nil {
		p.Abort()
		return nil, Unavailable
	}
	return p, nil
}

// Open downloads remote originals into a bounded private temporary file for ranges and backups.
// An absent locator means the original remains in its existing local collection directory.
func (s *Store) Open(ctx context.Context, module, key string) (*os.File, func(), error) {
	location, err := s.repository.location(ctx, module, key)
	if err != nil {
		return nil, func() {}, err
	}
	_, c, err := s.profile(ctx, location.profile)
	if err != nil {
		return nil, func() {}, Unavailable
	}
	return s.openLocation(ctx, location, c)
}
func (s *Store) openLocation(ctx context.Context, location storedLocation, c S3Config) (*os.File, func(), error) {
	if location.size < 1 || location.size > 20<<20 {
		return nil, func() {}, Unavailable
	}
	return s.fetch(ctx, c, location.object, location.size, location.digest)
}
func (s *Store) temp() (*os.File, error) {
	dir := filepath.Join(s.dir, "file-staging")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	return os.CreateTemp(dir, "original-*")
}

func sameDestination(a, b S3Config) bool {
	return a.Endpoint == b.Endpoint && a.Region == b.Region && a.Bucket == b.Bucket && a.Prefix == b.Prefix && a.PathStyle == b.PathStyle
}
