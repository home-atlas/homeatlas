package httpadapter

import (
	"github.com/home-atlas/homeatlas/internal/core/identity/application"
	"net/http"
)

func (s *Transport) mountAccount(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/auth/profile", func(w http.ResponseWriter, r *http.Request) {
		out, err := s.Profile(r.Context(), s.client(r))
		s.writeResult(w, r, out, err, 200)
	})
	mux.HandleFunc("PATCH /api/auth/profile", func(w http.ResponseWriter, r *http.Request) {
		var input application.UpdateProfileInput
		if !decode(w, r, &input) {
			return
		}
		out, err := s.UpdateProfile(r.Context(), s.client(r), input)
		s.writeResult(w, r, out, err, 200)
	})
	mux.HandleFunc("GET /api/auth/sessions", func(w http.ResponseWriter, r *http.Request) {
		out, err := s.AccountSessions(r.Context(), s.client(r))
		s.writeResult(w, r, out, err, 200)
	})
	mux.HandleFunc("DELETE /api/auth/sessions/{id}", func(w http.ResponseWriter, r *http.Request) {
		out, err := s.RevokeAccountSession(r.Context(), s.client(r), r.PathValue("id"), false)
		s.writeResult(w, r, out, err, 204)
	})
	mux.HandleFunc("POST /api/auth/sessions/revoke-others", func(w http.ResponseWriter, r *http.Request) {
		var input struct{}
		if !decode(w, r, &input) {
			return
		}
		out, err := s.RevokeAccountSession(r.Context(), s.client(r), "", true)
		s.writeResult(w, r, out, err, 204)
	})
}
