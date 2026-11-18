package app_test

import (
	"strings"
	"testing"
)

func TestSearchModuleFilter(t *testing.T) {
	f := newFixture(t)
	for _, test := range []struct {
		module   string
		status   int
		expected bool
	}{{"documents", 200, true}, {"unknown", 200, false}, {"../documents", 400, false}} {
		w := perform(f.app, "GET", "/api/search?q=Manual&module="+test.module, nil, f.cookie)
		if w.Code != test.status {
			t.Fatal(test.module, w.Code, w.Body.String())
		}
		if test.status == 200 && (strings.Contains(w.Body.String(), `"id":"mine"`) != test.expected) {
			t.Fatal("filtered results", test.module, w.Body.String())
		}
		if strings.Contains(w.Body.String(), "privado") || strings.Contains(w.Body.String(), "otro hogar") {
			t.Fatal("filter exposed private data")
		}
	}
	if w := perform(f.app, "GET", "/api/search?q=Manual&module=documents", nil, nil); w.Code != 401 {
		t.Fatal("anonymous search", w.Code)
	}
}
