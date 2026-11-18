package domain

import (
	"encoding/json"
	"testing"
)

func TestInvitationPersistenceFieldsStayOutOfPublicJSON(t *testing.T) {
	value := Invitation{HouseholdID: "private-home", IssuerID: "private-issuer", IssuerMemberID: "private-membership", HouseholdName: "private-name", ExpiresUnix: 1, AcceptedUnix: 2, RevokedUnix: 3}
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	if len(fields) != 6 {
		t.Fatal("invitation persistence fields became public", string(raw))
	}
	for _, name := range []string{"id", "email", "role", "permissions", "expiresAt", "status"} {
		if _, ok := fields[name]; !ok {
			t.Fatal("public contract changed", name)
		}
	}
}
