package app_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/home-atlas/homeatlas/internal/app"
	"github.com/home-atlas/homeatlas/internal/core/identity"
)

const invitedPassword = "a secure passphrase for invited members"

type invitationLink struct {
	ID  string `json:"id"`
	URL string `json:"url"`
}

func inviteMember(t *testing.T, f fixture, email, role string, permissions []string, cookie *http.Cookie) invitationLink {
	t.Helper()
	w := perform(f.app, "POST", "/api/household/invitations", map[string]any{"email": email, "role": role, "permissions": permissions}, cookie)
	if w.Code != 201 {
		t.Fatalf("invite: %d %s", w.Code, w.Body.String())
	}
	var link invitationLink
	if err := json.Unmarshal(w.Body.Bytes(), &link); err != nil {
		t.Fatal(err)
	}
	return link
}
func invitationToken(link invitationLink) string { return strings.Split(link.URL, "#")[1] }
func acceptMember(t *testing.T, f fixture, link invitationLink) (identity.State, *http.Cookie) {
	t.Helper()
	w := perform(f.app, "POST", "/api/invitations/accept", map[string]string{"token": invitationToken(link), "name": "Invited member", "password": invitedPassword}, nil)
	if w.Code != 201 {
		t.Fatalf("accept: %d %s", w.Code, w.Body.String())
	}
	var state identity.State
	if err := json.Unmarshal(w.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	return state, w.Result().Cookies()[0]
}
func TestInvitationsMembershipAndSharingLifecycle(t *testing.T) {
	f := newFixture(t)
	link := inviteMember(t, f, "bob@example.test", "member", []string{"documents.read", "documents.write", "documents.share"}, f.cookie)
	var stored []byte
	if err := f.db.QueryRow(`SELECT token_hash FROM identity_invitations WHERE id=?`, link.ID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(invitationToken(link)))
	if !bytes.Equal(stored, sum[:]) {
		t.Fatal("raw invitation token stored")
	}
	listing := perform(f.app, "GET", "/api/household/invitations", nil, f.cookie)
	if strings.Contains(listing.Body.String(), invitationToken(link)) {
		t.Fatal("list exposed invitation token")
	}
	inspected := perform(f.app, "POST", "/api/invitations/inspect", map[string]string{"token": invitationToken(link)}, nil)
	if inspected.Code != 200 || !strings.Contains(inspected.Body.String(), `"needsAccount":true`) {
		t.Fatal(inspected.Body.String())
	}
	state, bob := acceptMember(t, f, link)
	if state.Session.Household.ID != f.state.Session.Household.ID || state.Session.Role != "member" {
		t.Fatal("accepted wrong household or role")
	}
	if replay := perform(f.app, "POST", "/api/invitations/accept", map[string]string{"token": invitationToken(link), "name": "Again", "password": invitedPassword}, nil); replay.Code != 410 {
		t.Fatalf("replay: %d", replay.Code)
	}
	d := decodeDocument(t, uploadRequest(t, f, "shared.txt", []byte("Shared original"), validMetadata, f.cookie))
	base := "/api/documents/" + d.ID
	if got := perform(f.app, "GET", base, nil, bob); got.Code != 404 {
		t.Fatal("private by default")
	}
	if got := perform(f.app, "PUT", base+"/shares/"+state.Session.User.ID, nil, f.cookie); got.Code != 204 {
		t.Fatalf("share: %d %s", got.Code, got.Body.String())
	}
	for _, path := range []string{base, base + "/download", "/api/documents?q=Factura", "/api/search?q=Factura"} {
		w := perform(f.app, "GET", path, nil, bob)
		if w.Code != 200 {
			t.Fatalf("shared %s: %d %s", path, w.Code, w.Body.String())
		}
		if path == base+"/download" && w.Body.String() != "Shared original" {
			t.Fatal("wrong shared original")
		}
		if path != base+"/download" && !strings.Contains(w.Body.String(), d.ID) {
			t.Fatal("missing shared document")
		}
	}
	if w := perform(f.app, "PATCH", base, map[string]string{"title": "Stolen", "category": "otros"}, bob); w.Code != 404 {
		t.Fatal("recipient modified shared file")
	}
	if w := perform(f.app, "PUT", base+"/shares/"+f.state.Session.User.ID, nil, bob); w.Code != 404 {
		t.Fatal("recipient reshared document")
	}
	if w := perform(f.app, "GET", base+"/shares", nil, bob); w.Code != 404 {
		t.Fatal("recipient viewed other grants")
	}
	if w := perform(f.app, "DELETE", base+"/shares/"+state.Session.User.ID, nil, f.cookie); w.Code != 204 {
		t.Fatal("unshare failed")
	}
	if w := perform(f.app, "GET", base+"/download", nil, bob); w.Code != 404 {
		t.Fatal("revoked grant still downloaded")
	}
	perform(f.app, "PUT", base+"/shares/"+state.Session.User.ID, nil, f.cookie)
	if w := perform(f.app, "PATCH", "/api/household/members/"+state.Session.User.ID, map[string]any{"role": "guest", "permissions": []string{}}, f.cookie); w.Code != 204 {
		t.Fatalf("member update %d %s", w.Code, w.Body.String())
	}
	if w := perform(f.app, "GET", base+"/download", nil, bob); w.Code != 403 {
		t.Fatal("permission revocation was stale")
	}
	perform(f.app, "PATCH", "/api/household/members/"+state.Session.User.ID, map[string]any{"role": "member", "permissions": []string{"documents.read"}}, f.cookie)
	if w := perform(f.app, "DELETE", "/api/household/members/"+state.Session.User.ID, nil, f.cookie); w.Code != 204 {
		t.Fatal("remove member")
	}
	if w := perform(f.app, "GET", base+"/download", nil, bob); w.Code != 401 {
		t.Fatal("removed member retained session")
	}
	rejoin := inviteMember(t, f, "bob@example.test", "member", []string{"documents.read"}, f.cookie)
	// Existing accounts must prove their identity; an invitation cannot overwrite their password.
	if w := perform(f.app, "POST", "/api/invitations/accept", map[string]string{"token": invitationToken(rejoin), "password": "wrong password"}, nil); w.Code != 401 {
		t.Fatal("invitation reset existing account")
	}
	newState, newBob := acceptMember(t, f, rejoin)
	if newState.Session.User.ID != state.Session.User.ID {
		t.Fatal("duplicated user on rejoin")
	}
	if w := perform(f.app, "GET", base, nil, newBob); w.Code != 404 {
		t.Fatal("old grant revived after rejoining")
	}
	if w := perform(f.app, "PUT", base+"/shares/"+state.Session.User.ID, nil, f.cookie); w.Code != 204 {
		t.Fatal("explicit reshare failed")
	}
	if w := perform(f.app, "GET", base+"/download", nil, newBob); w.Code != 200 {
		t.Fatal("explicit reshare unavailable")
	}
}

func TestRolesDelegationAndProtectedOwner(t *testing.T) {
	f := newFixture(t)
	adminState, admin := acceptMember(t, f, inviteMember(t, f, "admin@example.test", "admin", []string{"documents.read"}, f.cookie))
	memberState, member := acceptMember(t, f, inviteMember(t, f, "member@example.test", "member", []string{"documents.read"}, f.cookie))
	for _, test := range []struct {
		path, method string
		body         any
		cookie       *http.Cookie
	}{
		{"/api/household/invitations", "POST", map[string]any{"email": "bad@example.test", "role": "owner", "permissions": []string{}}, f.cookie},
		{"/api/household/invitations", "POST", map[string]any{"email": "bad@example.test", "role": "admin", "permissions": []string{"documents.read"}}, admin},
		{"/api/household/invitations", "POST", map[string]any{"email": "bad@example.test", "role": "member", "permissions": []string{"documents.read", "documents.write"}}, admin},
		{"/api/household/invitations", "POST", map[string]any{"email": "bad@example.test", "role": "guest", "permissions": []string{"documents.read", "documents.write"}}, f.cookie},
		{"/api/household/invitations", "POST", map[string]any{"email": "bad@example.test", "role": "member", "permissions": []string{"system.root"}}, f.cookie},
		{"/api/household/members/" + f.state.Session.User.ID, "DELETE", nil, f.cookie},
		{"/api/household/members/" + f.state.Session.User.ID, "PATCH", map[string]any{"role": "member", "permissions": []string{}}, admin},
		{"/api/household/members/" + adminState.Session.User.ID, "DELETE", nil, admin},
		{"/api/household/members/" + memberState.Session.User.ID, "PATCH", map[string]any{"role": "admin", "permissions": []string{}}, member},
	} {
		w := perform(f.app, test.method, test.path, test.body, test.cookie)
		if w.Code != 403 {
			t.Fatalf("delegation allowed %s %d %s", test.path, w.Code, w.Body.String())
		}
	}
	if w := perform(f.app, "GET", "/api/household/invitations", nil, member); w.Code != 403 {
		t.Fatal("member enumerated invitations")
	}
	list := perform(f.app, "GET", "/api/household/members", nil, member)
	if list.Code != 200 || strings.Contains(list.Body.String(), "alice@example.test") {
		t.Fatal("directory exposed other emails")
	}
	if w := perform(f.app, "GET", "/api/household/members", nil, nil); w.Code != 401 {
		t.Fatal("anonymous directory")
	}
	protected := inviteMember(t, f, "future-admin@example.test", "admin", []string{"documents.read"}, f.cookie)
	if w := perform(f.app, "DELETE", "/api/household/invitations/"+protected.ID, nil, admin); w.Code != 403 {
		t.Fatal("admin revoked peer invitation")
	}
	if w := perform(f.app, "POST", "/api/household/invitations", map[string]any{"email": "future-admin@example.test", "role": "member", "permissions": []string{"documents.read"}}, admin); w.Code != 403 {
		t.Fatal("admin replaced protected invitation")
	}
	pending := inviteMember(t, f, "another@example.test", "member", []string{"documents.read"}, admin)
	perform(f.app, "PATCH", "/api/household/members/"+adminState.Session.User.ID, map[string]any{"role": "member", "permissions": []string{"documents.read"}}, f.cookie)
	if w := perform(f.app, "POST", "/api/invitations/inspect", map[string]string{"token": invitationToken(pending)}, nil); w.Code != 410 {
		t.Fatal("downgraded issuer left active invite")
	}
}

func TestInvitationExpiryRevocationAndAtomicAcceptance(t *testing.T) {
	f := newFixture(t)
	for _, cause := range []string{"expire", "revoke", "replace"} {
		t.Run(cause, func(t *testing.T) {
			link := inviteMember(t, f, cause+"@example.test", "guest", []string{"documents.read"}, f.cookie)
			switch cause {
			case "expire":
				f.db.Exec(`UPDATE identity_invitations SET expires_at=? WHERE id=?`, time.Now().Add(-time.Minute).Unix(), link.ID)
			case "revoke":
				if w := perform(f.app, "DELETE", "/api/household/invitations/"+link.ID, nil, f.cookie); w.Code != 204 {
					t.Fatal(w.Body.String())
				}
			case "replace":
				inviteMember(t, f, cause+"@example.test", "guest", []string{"documents.read"}, f.cookie)
			}
			if w := perform(f.app, "POST", "/api/invitations/accept", map[string]string{"token": invitationToken(link), "name": "Test", "password": invitedPassword}, nil); w.Code != 410 {
				t.Fatalf("%s: %d", cause, w.Code)
			}
		})
	}
	link := inviteMember(t, f, "atomic@example.test", "member", []string{"documents.read"}, f.cookie)
	if _, err := f.db.Exec(`CREATE TRIGGER fail_accept BEFORE INSERT ON identity_memberships BEGIN SELECT RAISE(ABORT,'simulate failure'); END`); err != nil {
		t.Fatal(err)
	}
	if w := perform(f.app, "POST", "/api/invitations/accept", map[string]string{"token": invitationToken(link), "name": "Test", "password": invitedPassword}, nil); w.Code != 503 {
		t.Fatalf("rollback %d", w.Code)
	}
	var count int
	f.db.QueryRow(`SELECT count(*) FROM identity_users WHERE email='atomic@example.test'`).Scan(&count)
	if count != 0 {
		t.Fatal("partial user persisted")
	}
	f.db.Exec(`DROP TRIGGER fail_accept`)
	var wg sync.WaitGroup
	statuses := make(chan int, 2)
	for range 2 {
		wg.Go(func() {
			w := perform(f.app, "POST", "/api/invitations/accept", map[string]string{"token": invitationToken(link), "name": "Test", "password": invitedPassword}, nil)
			statuses <- w.Code
		})
	}
	wg.Wait()
	close(statuses)
	success := 0
	for status := range statuses {
		if status == 201 {
			success++
		} else if status != 410 {
			t.Fatalf("concurrent acceptance %d", status)
		}
	}
	if success != 1 {
		t.Fatal("invitation used multiple times")
	}
}

func TestHouseholdBoundariesAndPermissionDependencies(t *testing.T) {
	f := newFixture(t)
	outsider := otherMember(t, f, "outside", "foreign-home")
	d := decodeDocument(t, uploadRequest(t, f, "scope.txt", []byte("Scoped file"), validMetadata, f.cookie))
	if w := perform(f.app, "PUT", "/api/documents/"+d.ID+"/shares/outside", nil, f.cookie); w.Code != 404 {
		t.Fatal("cross household sharing")
	}
	if w := perform(f.app, "DELETE", "/api/household/members/outside", nil, f.cookie); w.Code != 404 {
		t.Fatal("cross household management")
	}
	if w := perform(f.app, "GET", "/api/documents/"+d.ID, nil, outsider); w.Code != 404 {
		t.Fatal("outsider saw document")
	}
	if w := perform(f.app, "POST", "/api/household/invitations", map[string]any{"email": "write-only@example.test", "role": "member", "permissions": []string{"documents.write"}}, f.cookie); w.Code != 403 {
		t.Fatal("write granted without read")
	}
	link := inviteMember(t, f, "mismatch@example.test", "member", []string{"documents.read"}, f.cookie)
	if w := perform(f.app, "POST", "/api/invitations/accept", map[string]string{"token": invitationToken(link), "name": "Test", "password": invitedPassword}, f.cookie); w.Code != 403 {
		t.Fatal("accepted invitation as wrong logged-in user")
	}
}

func TestMembershipAndSharingMutationsRequireOrigin(t *testing.T) {
	f := newFixture(t)
	for _, path := range []string{"/api/household/invitations", "/api/household/members/anything", "/api/documents/mine/shares/anything", "/api/invitations/accept"} {
		method := "POST"
		if strings.Contains(path, "/shares/") {
			method = "PUT"
		}
		if strings.Contains(path, "/members/") {
			method = "DELETE"
		}
		req := httptest.NewRequest(method, origin+path, strings.NewReader(`{}`))
		req.AddCookie(f.cookie)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", "https://untrusted.example")
		req.Header.Set("Sec-Fetch-Site", "cross-site")
		w := httptest.NewRecorder()
		f.app.Handler.ServeHTTP(w, req)
		if w.Code != 403 {
			t.Fatalf("cross-site mutation %s: %d", path, w.Code)
		}
	}
}

func TestInvitationPersistenceAndOneTimeOwnerUpgrade(t *testing.T) {
	f := newFixture(t)
	otherMember(t, f, "legacy-reader", f.state.Session.Household.ID)
	for _, statement := range []string{`DELETE FROM identity_permissions WHERE capability IN ('household.manage','documents.share')`, `DELETE FROM schema_migrations WHERE module='app.household-sharing-permissions'`} {
		if _, err := f.db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	restarted, err := app.New(context.Background(), f.db, f.assets, origin, f.dataDir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	f.app = restarted
	link := inviteMember(t, f, "persist@example.test", "member", []string{"documents.read"}, f.cookie)
	var count int
	if err := f.db.QueryRow(`SELECT count(*) FROM identity_permissions WHERE user_id='legacy-reader' AND capability IN ('household.manage','documents.share')`).Scan(&count); err != nil || count != 0 {
		t.Fatal("legacy reader got elevated permissions")
	}
	if _, err := f.db.Exec(`DELETE FROM identity_permissions WHERE user_id=? AND capability='documents.share'`, f.state.Session.User.ID); err != nil {
		t.Fatal(err)
	}
	again, err := app.New(context.Background(), f.db, f.assets, origin, f.dataDir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	f.app = again
	if err := f.db.QueryRow(`SELECT count(*) FROM identity_permissions WHERE user_id=? AND capability='documents.share'`, f.state.Session.User.ID).Scan(&count); err != nil || count != 0 {
		t.Fatal("restart regranted revoked sharing")
	}
	if w := perform(f.app, "POST", "/api/invitations/inspect", map[string]string{"token": invitationToken(link)}, nil); w.Code != 200 {
		t.Fatalf("lost invitation on restart: %d %s", w.Code, w.Body.String())
	}
	acceptMember(t, f, link)
}

func TestAdministratorHasNoAutomaticDocumentAccess(t *testing.T) {
	f := newFixture(t)
	_, admin := acceptMember(t, f, inviteMember(t, f, "full-admin@example.test", "admin", []string{"documents.read", "documents.write", "documents.share"}, f.cookie))
	d := decodeDocument(t, uploadRequest(t, f, "owner-private.txt", []byte("Owner private original"), validMetadata, f.cookie))
	base := "/api/documents/" + d.ID
	for _, p := range []struct {
		method, path string
		body         any
	}{
		{"GET", base, nil}, {"GET", base + "/download", nil}, {"GET", base + "/shares", nil},
		{"PATCH", base, map[string]string{"title": "Admin change", "category": "otros"}},
		{"PUT", base + "/shares/" + f.state.Session.User.ID, nil},
	} {
		w := perform(f.app, p.method, p.path, p.body, admin)
		if w.Code != 404 {
			t.Fatalf("administrative role bypassed ownership: %s %d %s", p.path, w.Code, w.Body.String())
		}
	}
}
