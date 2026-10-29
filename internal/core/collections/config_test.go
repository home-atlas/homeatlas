package collections

import (
	"context"
	"strings"
	"testing"
)

func testConfig(id string) Config {
	return Config{ID: id, Name: "Files", Version: "1", Definition: Definition{Title: "Files", Categories: []Option{{Value: "shares", Label: "Shared"}}, MediaTypes: []string{"text/plain"}, MaxFileBytes: 1024, TitleMaxLength: 160, DescriptionMaxLength: 2000, MaxTags: 10, TagMaxLength: 40}, Validate: func(_ context.Context, b []byte) ([]byte, error) { return b, nil }}
}
func TestCollectionNamespaceAndDeclaredDefault(t *testing.T) {
	a, b := testConfig("ext-pets"), testConfig("ext-pet")
	if a.Valid() != nil || b.Valid() != nil {
		t.Fatal("valid configuration rejected")
	}
	query := "CREATE TABLE collection_items(category TEXT DEFAULT '__collection_default__'); SELECT item_id FROM collection_grants; SELECT item_id FROM collection_snapshot_grants"
	x, y := a.sql(query), b.sql(query)
	if x == y || !strings.Contains(x, "plugin_collection_ext_pets_shares") || !strings.Contains(y, "plugin_collection_ext_pet_shares") || !strings.Contains(x, "DEFAULT 'shares'") {
		t.Fatal(x, y)
	}
	for _, id := range []string{"../auth", "a_b", "';DROP TABLE identity_users;--", "/root"} {
		c := testConfig(id)
		if c.Valid() == nil {
			t.Fatal("unsafe namespace", id)
		}
	}
}
func TestCollectionBoundsAreInstallationData(t *testing.T) {
	c := testConfig("ext-files")
	c.MaxFileBytes = MaxFileSize + 1
	if c.Valid() == nil {
		t.Fatal("unbounded file")
	}
	c = testConfig("ext-files")
	c.MediaTypes = []string{"text/html"}
	if c.Valid() == nil {
		t.Fatal("unsupported content sniffing type")
	}
	c = testConfig("ext-files")
	c.Categories = append(c.Categories, c.Categories[0])
	if c.Valid() == nil {
		t.Fatal("ambiguous category")
	}
}
