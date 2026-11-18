package domain

import (
	"errors"
	"net/url"
	"regexp"
	"strings"
)

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
type Collection struct{ ID, Table, Directory string }
type MoveStats struct {
	Bucket         string `json:"bucket,omitempty"`
	Target         string `json:"target"`
	Revision       string `json:"revision"`
	Local          int    `json:"local"`
	Remote         int    `json:"remote"`
	LocalBytes     int64  `json:"localBytes"`
	RemoteBytes    int64  `json:"remoteBytes"`
	PendingCleanup int    `json:"pendingCleanup"`
}
type MoveResult struct {
	MoveStats
	Moved bool `json:"moved"`
}
type MoveItem struct {
	ID, Key, Digest string
	Size            int64
}
type Obsolete struct{ ID, Module, Key, Profile, Object string }
type Location struct {
	Module, Key             string
	Profile, Object, Digest string
	Size                    int64
}

func Validate(c S3Config) error {
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
func SameDestination(a, b S3Config) bool {
	return a.Endpoint == b.Endpoint && a.Region == b.Region && a.Bucket == b.Bucket && a.Prefix == b.Prefix && a.PathStyle == b.PathStyle
}
func ObjectKey(c S3Config, house, Module, Key string) string {
	return strings.TrimPrefix(c.Prefix+"/"+house+"/"+Module+"/"+Key, "/")
}

var Invalid = errors.New("invalid storage configuration")
var Conflict = errors.New("storage configuration changed")
var Unavailable = errors.New("storage unavailable")
var SafeID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
var bucketName = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$`)
var regionName = regexp.MustCompile(`^[a-z0-9-]{1,63}$`)
var safePrefix = regexp.MustCompile(`^[A-Za-z0-9/_-]{0,200}$`)
var TableName = regexp.MustCompile(`^[a-z][a-z0-9_]{0,100}$`)
