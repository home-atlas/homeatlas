package httpadapter

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/home-atlas/homeatlas/internal/core/access"
	"github.com/home-atlas/homeatlas/internal/core/fault"
	"github.com/home-atlas/homeatlas/internal/core/identity/application"
	"github.com/home-atlas/homeatlas/internal/core/identity/domain"
	"github.com/home-atlas/homeatlas/internal/core/modules"
	"github.com/home-atlas/homeatlas/internal/core/unitofwork"
)

type Transport struct {
	*application.Application
	host           string
	modulesEnabled bool
	secure         bool
	cookieName     string
	csrf           *http.CrossOriginProtection
}

func (s *Transport) client(r *http.Request) Client {
	c := Client{RemoteAddress: r.RemoteAddr}
	if cookie, err := r.Cookie(s.cookieName); err == nil {
		c.SessionToken = cookie.Value
		c.HasSession = true
	}
	if cookie, err := r.Cookie(s.pinCookieName()); err == nil {
		c.DeviceToken = cookie.Value
		c.HasDevice = true
	}
	return c
}
func writeFailure(w http.ResponseWriter, err error) {
	f := fault.From(err)
	if f.RetryAfter > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(f.RetryAfter))
	}
	status := map[fault.Kind]int{fault.Invalid: 400, fault.Unauthenticated: 401, fault.Denied: 403, fault.Missing: 404, fault.Conflict: 409, fault.Expired: 410, fault.TooLarge: 413, fault.Unsupported: 415, fault.Locked: 423, fault.Busy: 429, fault.Internal: 500, fault.Unavailable: 503}[f.Kind]
	fail(w, status, f.Code)
}
func (s *Transport) writeResult(w http.ResponseWriter, r *http.Request, out Result, err error, status int) {
	if out.DeviceToken != "" {
		s.setPINCookie(w, out.DeviceToken)
	}
	if out.EndSession {
		s.clearCookie(w)
	}
	if err != nil {
		writeFailure(w, err)
		return
	}
	if out.SessionToken != "" {
		s.respondSession(w, r, out.SessionToken, status)
		return
	}
	if out.Data != nil {
		modules.JSON(w, status, out.Data)
	} else {
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(status)
	}
}
func (s *Transport) Mount(mux *http.ServeMux) {
	s.mountAccount(mux)
	s.mountMembers(mux)
	s.mountAlternativeAccess(mux)
	mux.HandleFunc("GET /api/household/settings", s.householdSettings)
	mux.HandleFunc("PATCH /api/household/settings", s.updateHouseholdSettings)
	if s.modulesEnabled {
		mux.HandleFunc("PATCH /api/household/modules/{id}", s.updateModule)
	}
	mux.HandleFunc("GET /api/auth/session", s.session)
	mux.HandleFunc("POST /api/setup", s.setup)
	mux.HandleFunc("POST /api/auth/login", s.login)
	mux.HandleFunc("POST /api/auth/logout", s.logout)
	mux.HandleFunc("POST /api/auth/recover", s.recoverAccess)
	mux.HandleFunc("POST /api/auth/email/verification", s.requestVerification)
	mux.HandleFunc("POST /api/auth/email/verify", s.verifyEmail)
	mux.HandleFunc("POST /api/auth/recovery/request", s.requestRecoveryEmail)
}

func (s *Transport) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/") {
			next.ServeHTTP(w, r)
			return
		}
		// Do not trust Forwarded headers or an attacker-controlled Host (including DNS rebinding).
		if r.Host != s.host {
			fail(w, 421, "unexpected_host")
			return
		}
		if err := s.csrf.Check(r); err != nil {
			fail(w, 403, "cross_origin_request")
			return
		}
		ctx := access.WithPrincipal(r.Context(), access.Principal{})
		if cookie, err := r.Cookie(s.cookieName); err == nil {
			authenticated, err := s.Authenticate(ctx, cookie.Value)
			switch {
			case errors.Is(err, unitofwork.ErrNotFound):
				s.clearCookie(w)
			case err != nil:
				fail(w, 503, "session_unavailable")
				return
			default:
				ctx = authenticated
			}
		}
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (s *Transport) respondSession(w http.ResponseWriter, r *http.Request, token string, status int) {
	current, err := s.LoadSession(r.Context(), token)
	if err != nil {
		fail(w, 500, "session_unavailable")
		return
	}
	http.SetCookie(w, &http.Cookie{Name: s.cookieName, Value: token, Path: "/", HttpOnly: true, Secure: s.secure,
		SameSite: http.SameSiteStrictMode, Expires: current.ExpiresAt, MaxAge: int(domain.SessionLifetime.Seconds())})
	modules.JSON(w, status, State{Session: current, SMTPEnabled: s.SMTPEnabled()})
}

func (s *Transport) clearCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: s.cookieName, Value: "", Path: "/", HttpOnly: true, Secure: s.secure,
		SameSite: http.SameSiteStrictMode, Expires: time.Unix(1, 0), MaxAge: -1})
}

func fail(w http.ResponseWriter, status int, code string) {
	modules.JSON(w, status, map[string]string{"error": code})
}

func decode(w http.ResponseWriter, r *http.Request, target any) bool {
	contentType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || contentType != "application/json" {
		fail(w, 415, "json_required")
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		fail(w, 400, "invalid_request")
		return false
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		fail(w, 400, "invalid_request")
		return false
	}
	return true
}

func (s *Transport) setPINCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{Name: s.pinCookieName(), Value: token, Path: "/", HttpOnly: true, Secure: s.secure, SameSite: http.SameSiteStrictMode, MaxAge: int(domain.PINDeviceLifetime.Seconds()), Expires: time.Now().Add(domain.PINDeviceLifetime)})
}

func (s *Transport) pinCookieName() string {
	if s.secure {
		return "__Host-homeatlas_pin_device"
	}
	return "homeatlas_pin_device"
}

func (s *Transport) mountMembers(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/household/members", s.members)
	mux.HandleFunc("PATCH /api/household/members/{user}", s.updateMember)
	mux.HandleFunc("DELETE /api/household/members/{user}", s.removeMember)
	mux.HandleFunc("GET /api/household/invitations", s.invitations)
	mux.HandleFunc("POST /api/household/invitations", s.invite)
	mux.HandleFunc("DELETE /api/household/invitations/{id}", s.revokeInvitation)
	mux.HandleFunc("POST /api/invitations/inspect", s.inspectInvitation)
	mux.HandleFunc("POST /api/invitations/accept", s.acceptInvitation)
}

func (s *Transport) mountAlternativeAccess(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/household/assisted-members", s.createAssistedMember)
	mux.HandleFunc("POST /api/household/members/{user}/enrollment", s.renewAssistedEnrollment)
	mux.HandleFunc("POST /api/assisted/inspect", s.inspectAssistedEnrollment)
	mux.HandleFunc("POST /api/assisted/complete", s.completeAssistedEnrollment)
	mux.HandleFunc("GET /api/auth/pin", s.pinStatus)
	mux.HandleFunc("POST /api/auth/pin/login", s.pinLogin)
	mux.HandleFunc("POST /api/auth/pin/device", s.registerPINDevice)
	mux.HandleFunc("GET /api/auth/access", s.accessSettings)
	mux.HandleFunc("PATCH /api/auth/access", s.updateUsername)
	mux.HandleFunc("DELETE /api/auth/pin/devices/{id}", s.revokePINDevice)
}

func (s *Transport) session(w http.ResponseWriter, r *http.Request) {
	out, err := s.Session(r.Context(), s.client(r))
	s.writeResult(w, r, out, err, 200)
}

func (s *Transport) setup(w http.ResponseWriter, r *http.Request) {
	ready, err := s.Initialized(r.Context())
	if err != nil {
		fail(w, 503, "setup_unavailable")
		return
	}
	if ready {
		fail(w, 409, "setup_completed")
		return
	}
	var input SetupInput
	if !decode(w, r, &input) {
		return
	}
	out, err := s.Setup(r.Context(), s.client(r), input)
	s.writeResult(w, r, out, err, 201)
}

func (s *Transport) login(w http.ResponseWriter, r *http.Request) {
	var input LoginInput
	if !decode(w, r, &input) {
		return
	}
	out, err := s.Login(r.Context(), s.client(r), input)
	s.writeResult(w, r, out, err, 200)
}

func (s *Transport) logout(w http.ResponseWriter, r *http.Request) {
	out, err := s.Logout(r.Context(), s.client(r))
	s.writeResult(w, r, out, err, 204)
}

func (s *Transport) recoverAccess(w http.ResponseWriter, r *http.Request) {
	var input RecoverAccessInput
	if !decode(w, r, &input) {
		return
	}
	out, err := s.RecoverAccess(r.Context(), s.client(r), input)
	s.writeResult(w, r, out, err, 204)
}

func (s *Transport) requestVerification(w http.ResponseWriter, r *http.Request) {
	var input struct{}
	if !decode(w, r, &input) {
		return
	}
	out, err := s.RequestVerification(r.Context(), s.client(r))
	s.writeResult(w, r, out, err, 202)
}

func (s *Transport) verifyEmail(w http.ResponseWriter, r *http.Request) {
	var input VerifyEmailInput
	if !decode(w, r, &input) {
		return
	}
	out, err := s.VerifyEmail(r.Context(), s.client(r), input)
	s.writeResult(w, r, out, err, 204)
}

func (s *Transport) requestRecoveryEmail(w http.ResponseWriter, r *http.Request) {
	var input RequestRecoveryEmailInput
	if !decode(w, r, &input) {
		return
	}
	out, err := s.RequestRecoveryEmail(r.Context(), s.client(r), input)
	s.writeResult(w, r, out, err, 202)
}

func (s *Transport) householdSettings(w http.ResponseWriter, r *http.Request) {
	out, err := s.HouseholdSettings(r.Context(), s.client(r))
	s.writeResult(w, r, out, err, 200)
}

func (s *Transport) updateHouseholdSettings(w http.ResponseWriter, r *http.Request) {
	var input UpdateHouseholdSettingsInput
	if !decode(w, r, &input) {
		return
	}
	out, err := s.UpdateHouseholdSettings(r.Context(), s.client(r), input)
	s.writeResult(w, r, out, err, 204)
}

func (s *Transport) updateModule(w http.ResponseWriter, r *http.Request) {
	var input UpdateModuleInput
	if !decode(w, r, &input) {
		return
	}
	out, err := s.UpdateModule(r.Context(), s.client(r), r.PathValue("id"), input)
	s.writeResult(w, r, out, err, 204)
}

func (s *Transport) invitations(w http.ResponseWriter, r *http.Request) {
	out, err := s.Invitations(r.Context(), s.client(r))
	s.writeResult(w, r, out, err, 200)
}

func (s *Transport) invite(w http.ResponseWriter, r *http.Request) {
	var input InviteInput
	if !decode(w, r, &input) {
		return
	}
	out, err := s.Invite(r.Context(), s.client(r), input)
	s.writeResult(w, r, out, err, 201)
}

func (s *Transport) revokeInvitation(w http.ResponseWriter, r *http.Request) {
	out, err := s.RevokeInvitation(r.Context(), s.client(r), r.PathValue("id"))
	s.writeResult(w, r, out, err, 204)
}

func (s *Transport) inspectInvitation(w http.ResponseWriter, r *http.Request) {
	var input InspectInvitationInput
	if !decode(w, r, &input) {
		return
	}
	out, err := s.InspectInvitation(r.Context(), s.client(r), input)
	s.writeResult(w, r, out, err, 200)
}

func (s *Transport) acceptInvitation(w http.ResponseWriter, r *http.Request) {
	var input AcceptInvitationInput
	if !decode(w, r, &input) {
		return
	}
	out, err := s.AcceptInvitation(r.Context(), s.client(r), input)
	s.writeResult(w, r, out, err, 201)
}

func (s *Transport) members(w http.ResponseWriter, r *http.Request) {
	out, err := s.Members(r.Context(), s.client(r))
	s.writeResult(w, r, out, err, 200)
}

func (s *Transport) createAssistedMember(w http.ResponseWriter, r *http.Request) {
	var in AssistedInput
	if !decode(w, r, &in) {
		return
	}
	out, err := s.CreateAssistedMember(r.Context(), s.client(r), in)
	s.writeResult(w, r, out, err, 201)
}

func (s *Transport) renewAssistedEnrollment(w http.ResponseWriter, r *http.Request) {
	var in RenewAssistedEnrollmentInput
	if !decode(w, r, &in) {
		return
	}
	out, err := s.RenewAssistedEnrollment(r.Context(), s.client(r), r.PathValue("user"), in)
	s.writeResult(w, r, out, err, 200)
}

func (s *Transport) inspectAssistedEnrollment(w http.ResponseWriter, r *http.Request) {
	var in InspectAssistedEnrollmentInput
	if !decode(w, r, &in) {
		return
	}
	out, err := s.InspectAssistedEnrollment(r.Context(), s.client(r), in)
	s.writeResult(w, r, out, err, 200)
}

func (s *Transport) completeAssistedEnrollment(w http.ResponseWriter, r *http.Request) {
	var in CompleteAssistedEnrollmentInput
	if !decode(w, r, &in) {
		return
	}
	out, err := s.CompleteAssistedEnrollment(r.Context(), s.client(r), in)
	s.writeResult(w, r, out, err, 204)
}

func (s *Transport) pinStatus(w http.ResponseWriter, r *http.Request) {
	out, err := s.PinStatus(r.Context(), s.client(r))
	s.writeResult(w, r, out, err, 200)
}

func (s *Transport) pinLogin(w http.ResponseWriter, r *http.Request) {
	var in PINLoginInput
	if !decode(w, r, &in) {
		return
	}
	out, err := s.PinLogin(r.Context(), s.client(r), in)
	s.writeResult(w, r, out, err, 200)
}

func (s *Transport) registerPINDevice(w http.ResponseWriter, r *http.Request) {
	var in RegisterPINDeviceInput
	if !decode(w, r, &in) {
		return
	}
	out, err := s.RegisterPINDevice(r.Context(), s.client(r), in)
	s.writeResult(w, r, out, err, 204)
}

func (s *Transport) accessSettings(w http.ResponseWriter, r *http.Request) {
	out, err := s.AccessSettings(r.Context(), s.client(r))
	s.writeResult(w, r, out, err, 200)
}

func (s *Transport) updateUsername(w http.ResponseWriter, r *http.Request) {
	var in UpdateUsernameInput
	if !decode(w, r, &in) {
		return
	}
	out, err := s.UpdateUsername(r.Context(), s.client(r), in)
	s.writeResult(w, r, out, err, 204)
}

func (s *Transport) revokePINDevice(w http.ResponseWriter, r *http.Request) {
	out, err := s.RevokePINDevice(r.Context(), s.client(r), r.PathValue("id"))
	s.writeResult(w, r, out, err, 204)
}

func (s *Transport) updateMember(w http.ResponseWriter, r *http.Request) {
	var input GrantInput
	if !decode(w, r, &input) {
		return
	}
	out, err := s.ChangeMember(r.Context(), s.client(r), r.PathValue("user"), input, false)
	s.writeResult(w, r, out, err, 204)
}

func (s *Transport) removeMember(w http.ResponseWriter, r *http.Request) {
	var input GrantInput
	out, err := s.ChangeMember(r.Context(), s.client(r), r.PathValue("user"), input, true)
	s.writeResult(w, r, out, err, 204)
}

func (s *Transport) AuthorizeInstallationRecovery(w http.ResponseWriter, r *http.Request, code string) bool {
	err := s.CheckInstallationRecovery(r.Context(), s.client(r), code)
	if err != nil {
		writeFailure(w, err)
		return false
	}
	return true
}
