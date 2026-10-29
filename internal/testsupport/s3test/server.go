// Package s3test provides a private TLS S3 protocol fixture for storage tests.
package s3test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
)

type Server struct {
	*httptest.Server
	mu         sync.Mutex
	objects    map[string][]byte
	deny       bool
	denyDelete bool
	corruptPut bool
}

func New(t *testing.T) *Server {
	t.Helper()
	s := &Server{objects: map[string][]byte{}}
	s.Server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		if !strings.HasPrefix(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256 Credential=fixture-access/") || r.Header.Get("X-Amz-Date") == "" || s.deny || (s.denyDelete && r.Method == "DELETE") {
			w.Header().Set("Content-Type", "application/xml")
			w.WriteHeader(403)
			io.WriteString(w, "<Error><Code>AccessDenied</Code><Message>Denied</Message></Error>")
			return
		}
		key := r.URL.Path
		switch r.Method {
		case "PUT":
			raw, err := io.ReadAll(io.LimitReader(r.Body, 21<<20))
			if err != nil {
				w.WriteHeader(400)
				return
			}
			if s.corruptPut && len(raw) > 0 {
				raw[0] ^= 1
			}
			s.objects[key] = raw
			w.Header().Set("ETag", `"fixture-etag"`)
			w.WriteHeader(200)
		case "GET":
			raw, ok := s.objects[key]
			if !ok {
				w.WriteHeader(404)
				return
			}
			w.Header().Set("Content-Length", strconv.Itoa(len(raw)))
			w.Write(raw)
		case "DELETE":
			delete(s.objects, key)
			w.WriteHeader(204)
		default:
			w.WriteHeader(405)
		}
	}))
	t.Cleanup(s.Close)
	return s
}
func (s *Server) Deny(value bool) { s.mu.Lock(); defer s.mu.Unlock(); s.deny = value }
func (s *Server) Count() int      { s.mu.Lock(); defer s.mu.Unlock(); return len(s.objects) }
func (s *Server) Corrupt() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for key, raw := range s.objects {
		if len(raw) > 0 {
			s.objects[key] = append([]byte("!"), raw[1:]...)
		}
	}
}

func (s *Server) DenyDelete(value bool) { s.mu.Lock(); defer s.mu.Unlock(); s.denyDelete = value }
func (s *Server) CorruptPut(value bool) { s.mu.Lock(); defer s.mu.Unlock(); s.corruptPut = value }
