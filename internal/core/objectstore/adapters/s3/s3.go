package s3adapter

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/home-atlas/homeatlas/internal/core/backup"
	"github.com/home-atlas/homeatlas/internal/core/objectstore/domain"
)

func (s *Client) client(c S3Config) *s3.Client {
	cfg := aws.Config{Region: c.Region, Credentials: credentials.NewStaticCredentialsProvider(c.AccessKey, c.SecretKey, ""), HTTPClient: s.HTTPClient, RetryMaxAttempts: 2, RequestChecksumCalculation: aws.RequestChecksumCalculationWhenRequired, ResponseChecksumValidation: aws.ResponseChecksumValidationWhenRequired}
	return s3.NewFromConfig(cfg, func(o *s3.Options) {
		o.UsePathStyle = c.PathStyle
		if c.Endpoint != "" {
			o.BaseEndpoint = aws.String(c.Endpoint)
		}
	})
}
func (s *Client) Put(ctx context.Context, c S3Config, key string, body io.ReadSeeker, size int64) error {
	_, err := s.client(c).PutObject(ctx, &s3.PutObjectInput{Bucket: aws.String(c.Bucket), Key: aws.String(key), Body: body, ContentLength: aws.Int64(size), ContentType: aws.String("application/octet-stream")})
	return err
}
func (s *Client) Delete(ctx context.Context, c S3Config, key string) error {
	_, err := s.client(c).DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String(c.Bucket), Key: aws.String(key)})
	return err
}
func (s *Client) Probe(ctx context.Context, house string, c S3Config) error {
	key := domain.ObjectKey(c, house, "connection-check", rand.Text())
	raw := []byte(rand.Text())
	if err := s.Put(ctx, c, key, bytes.NewReader(raw), int64(len(raw))); err != nil {
		return err
	}
	// Cleanup gets its own bounded budget even when the probe context was canceled.
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		s.Delete(cleanup, c, key)
	}()
	result, err := s.client(c).GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(c.Bucket), Key: aws.String(key)})
	if err != nil {
		return err
	}
	got, err := io.ReadAll(io.LimitReader(result.Body, int64(len(raw)+1)))
	result.Body.Close()
	if err != nil || !bytes.Equal(raw, got) {
		return domain.Unavailable
	}
	return s.Delete(ctx, c, key)
}
func (s *Client) Fetch(ctx context.Context, c S3Config, key string, size int64, digest string) (*os.File, func(), error) {
	noop := func() {}
	result, err := s.client(c).GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(c.Bucket), Key: aws.String(key)})
	if err != nil {
		return nil, noop, domain.Unavailable
	}
	defer result.Body.Close()
	if result.ContentLength != nil && *result.ContentLength != size {
		return nil, noop, domain.Unavailable
	}
	f, err := s.temp()
	if err != nil {
		return nil, noop, err
	}
	cleanup := func() { backup.DiscardCache(f); f.Close(); os.Remove(f.Name()) }
	hash := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, hash), io.LimitReader(result.Body, size+1))
	if err != nil || n != size || hex.EncodeToString(hash.Sum(nil)) != digest {
		cleanup()
		return nil, noop, domain.Unavailable
	}
	if _, err = f.Seek(0, io.SeekStart); err != nil {
		cleanup()
		return nil, noop, err
	}
	return f, cleanup, nil
}

type S3Config = domain.S3Config
type Client struct {
	HTTPClient aws.HTTPClient
	dir        string
}

func New(dir string) *Client {
	return &Client{dir: dir, HTTPClient: &http.Client{Timeout: 60 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}
func (s *Client) temp() (*os.File, error) {
	dir := filepath.Join(s.dir, "file-staging")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	return os.CreateTemp(dir, "original-*")
}
