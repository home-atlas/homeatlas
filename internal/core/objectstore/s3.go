package objectstore

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/home-atlas/homeatlas/internal/core/backup"
)

func (s *Store) client(c S3Config) *s3.Client {
	cfg := aws.Config{Region: c.Region, Credentials: credentials.NewStaticCredentialsProvider(c.AccessKey, c.SecretKey, ""), HTTPClient: s.HTTPClient, RetryMaxAttempts: 2, RequestChecksumCalculation: aws.RequestChecksumCalculationWhenRequired, ResponseChecksumValidation: aws.ResponseChecksumValidationWhenRequired}
	return s3.NewFromConfig(cfg, func(o *s3.Options) {
		o.UsePathStyle = c.PathStyle
		if c.Endpoint != "" {
			o.BaseEndpoint = aws.String(c.Endpoint)
		}
	})
}
func objectKey(c S3Config, house, module, key string) string {
	return strings.TrimPrefix(c.Prefix+"/"+house+"/"+module+"/"+key, "/")
}
func (s *Store) put(ctx context.Context, c S3Config, key string, body io.ReadSeeker, size int64) error {
	_, err := s.client(c).PutObject(ctx, &s3.PutObjectInput{Bucket: aws.String(c.Bucket), Key: aws.String(key), Body: body, ContentLength: aws.Int64(size), ContentType: aws.String("application/octet-stream")})
	return err
}
func (s *Store) deleteObject(ctx context.Context, c S3Config, key string) error {
	_, err := s.client(c).DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String(c.Bucket), Key: aws.String(key)})
	return err
}
func (s *Store) probe(ctx context.Context, house string, c S3Config) error {
	key := objectKey(c, house, "connection-check", rand.Text())
	raw := []byte(rand.Text())
	if err := s.put(ctx, c, key, bytes.NewReader(raw), int64(len(raw))); err != nil {
		return err
	}
	// Cleanup gets its own bounded budget even when the probe context was canceled.
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		s.deleteObject(cleanup, c, key)
	}()
	result, err := s.client(c).GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(c.Bucket), Key: aws.String(key)})
	if err != nil {
		return err
	}
	got, err := io.ReadAll(io.LimitReader(result.Body, int64(len(raw)+1)))
	result.Body.Close()
	if err != nil || !bytes.Equal(raw, got) {
		return Unavailable
	}
	return s.deleteObject(ctx, c, key)
}
func (s *Store) fetch(ctx context.Context, c S3Config, key string, size int64, digest string) (*os.File, func(), error) {
	noop := func() {}
	result, err := s.client(c).GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(c.Bucket), Key: aws.String(key)})
	if err != nil {
		return nil, noop, Unavailable
	}
	defer result.Body.Close()
	if result.ContentLength != nil && *result.ContentLength != size {
		return nil, noop, Unavailable
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
		return nil, noop, Unavailable
	}
	if _, err = f.Seek(0, io.SeekStart); err != nil {
		cleanup()
		return nil, noop, err
	}
	return f, cleanup, nil
}
