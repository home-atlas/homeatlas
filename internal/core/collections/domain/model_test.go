package domain

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestStorageAndMembershipKeysStayOutOfPublicJSON(t *testing.T) {
	for _, value := range []any{Item{ID: "document", FileKey: "private-storage-key"}, Recipient{UserID: "reader", MemberID: "private-membership-key"}} {
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), "private-") {
			t.Fatal("internal storage/membership key became public", string(raw))
		}
	}
}
