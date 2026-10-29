package identity

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestNewPasswordLengthBoundaries(t *testing.T) {
	for _, test := range []struct {
		name, password string
		valid          bool
	}{
		{"empty", "", false},
		{"seven", "pass123", false},
		{"eight", "pass1234", true},
		{"maximum", strings.Repeat("a", 128), true},
		{"too long", strings.Repeat("a", 129), false},
		{"seven unicode characters", strings.Repeat("🏠", 7), false},
		{"eight unicode characters", strings.Repeat("🏠", 8), true},
		{"maximum bytes", strings.Repeat("🏠", 128), true},
		{"invalid encoding", "1234567\xff", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := validNewPassword(test.password); got != test.valid {
				t.Fatalf("valid=%v, want %v", got, test.valid)
			}
		})
	}
}

func TestEightCharacterPasswordsAcrossAccountFlows(t *testing.T) {
	t.Run("setup", func(t *testing.T) {
		s, h, _ := newService(t, testOrigin)
		t.Cleanup(s.Close)
		body := setupBody()
		body["password"] = "pass123"
		if w := send(h, testOrigin, "POST", "/api/setup", body, nil); w.Code != 400 {
			t.Fatal("seven-character setup accepted", w.Code)
		}
		body["password"] = "pass1234"
		if w := send(h, testOrigin, "POST", "/api/setup", body, nil); w.Code != 201 {
			t.Fatal("eight-character setup rejected", w.Code, w.Body.String())
		}
		if w := send(h, testOrigin, "POST", "/api/auth/login", map[string]string{"identifier": "alice@example.test", "password": "pass1234"}, nil); w.Code != 200 {
			t.Fatal("setup password cannot log in", w.Code)
		}
	})
	t.Run("recovery", func(t *testing.T) {
		s, h, db := newService(t, testOrigin)
		t.Cleanup(s.Close)
		cookie := bootstrap(t, h, testOrigin)
		code, _, err := IssueRecovery(context.Background(), db, "alice@example.test")
		if err != nil {
			t.Fatal(err)
		}
		if w := send(h, testOrigin, "POST", "/api/auth/recover", map[string]string{"code": code, "password": "pass123"}, nil); w.Code != 400 {
			t.Fatal("seven-character recovery accepted", w.Code)
		}
		if w := send(h, testOrigin, "GET", "/api/probe", nil, cookie); w.Code != 200 {
			t.Fatal("rejected recovery changed the active session", w.Code)
		}
		if w := send(h, testOrigin, "POST", "/api/auth/recover", map[string]string{"code": code, "password": "pass1234"}, nil); w.Code != 204 {
			t.Fatal("eight-character recovery rejected", w.Code, w.Body.String())
		}
		if w := send(h, testOrigin, "GET", "/api/probe", nil, cookie); w.Code != 401 {
			t.Fatal("recovery kept the old session", w.Code)
		}
		if w := send(h, testOrigin, "POST", "/api/auth/login", map[string]string{"identifier": "alice@example.test", "password": "pass1234"}, nil); w.Code != 200 {
			t.Fatal("recovered password cannot log in", w.Code)
		}
	})
	t.Run("invitation", func(t *testing.T) {
		s, h, owner := assistedFixture(t)
		t.Cleanup(s.Close)
		w := send(h, testOrigin, "POST", "/api/household/invitations", map[string]any{"email": "member@example.test", "role": "restricted", "permissions": []string{"documents.read"}}, owner)
		if w.Code != 201 {
			t.Fatal(w.Code, w.Body.String())
		}
		var invitation struct{ URL string }
		if err := json.Unmarshal(w.Body.Bytes(), &invitation); err != nil {
			t.Fatal(err)
		}
		body := map[string]string{"token": strings.Split(invitation.URL, "#")[1], "name": "Member", "password": "pass123"}
		if w := send(h, testOrigin, "POST", "/api/invitations/accept", body, nil); w.Code != 400 {
			t.Fatal("seven-character invitation accepted", w.Code)
		}
		body["password"] = "pass1234"
		if w := send(h, testOrigin, "POST", "/api/invitations/accept", body, nil); w.Code != 201 {
			t.Fatal("eight-character invitation rejected", w.Code, w.Body.String())
		}
		if w := send(h, testOrigin, "POST", "/api/auth/login", map[string]string{"identifier": "member@example.test", "password": "pass1234"}, nil); w.Code != 200 {
			t.Fatal("invited password cannot log in", w.Code)
		}
	})
}
