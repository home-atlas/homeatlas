package main

import (
	"strings"
	"testing"
)

func TestDocumentPolicy(t *testing.T) {
	m, ok := validate(`{"title":"  Factura  ","category":"recibos","description":"Compra\nCocina","tags":["Cocina","cocina","  2026 "]}`)
	if !ok || m.Title != "Factura" || len(m.Tags) != 2 || m.Tags[1] != "2026" {
		t.Fatal(m, ok)
	}
	for _, raw := range []string{`{"title":"","category":"otros"}`, `{"title":"x","category":"unknown"}`, `{"title":"x","category":"otros","extra":true}`, `{"title":"x","category":"otros"} {}`, `{"title":"` + strings.Repeat("x", 161) + `","category":"otros"}`} {
		if _, ok := validate(raw); ok {
			t.Fatal("invalid metadata accepted", raw)
		}
	}
}
