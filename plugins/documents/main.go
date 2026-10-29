// Documents is an isolated WASI policy package. The host supplies streaming
// attachment storage, per-resource authorization, search and recovery primitives.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode"
	"unicode/utf8"
)

type metadata struct {
	Title       string   `json:"title"`
	Category    string   `json:"category"`
	Description string   `json:"description"`
	Tags        []string `json:"tags"`
}

func text(s string, max int, multiline bool) bool {
	if !utf8.ValidString(s) || utf8.RuneCountInString(s) > max {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) && !(multiline && (r == '\n' || r == '\r' || r == '\t')) {
			return false
		}
	}
	return true
}
func validate(raw string) (metadata, bool) {
	var m metadata
	d := json.NewDecoder(strings.NewReader(raw))
	d.DisallowUnknownFields()
	if len(raw) > 16384 || d.Decode(&m) != nil || d.Decode(new(any)) != io.EOF {
		return m, false
	}
	m.Title = strings.TrimSpace(m.Title)
	m.Description = strings.TrimSpace(m.Description)
	categories := map[string]bool{"manuales": true, "recibos": true, "contratos": true, "garantias": true, "otros": true}
	if m.Title == "" || !text(m.Title, 160, false) || !text(m.Description, 2000, true) || !categories[m.Category] || len(m.Tags) > 10 {
		return m, false
	}
	tags := []string{}
	seen := map[string]bool{}
	for _, tag := range m.Tags {
		tag = strings.TrimSpace(tag)
		if tag == "" || !text(tag, 40, false) {
			return m, false
		}
		key := strings.ToLower(tag)
		if !seen[key] {
			tags = append(tags, tag)
			seen[key] = true
		}
	}
	m.Tags = tags
	return m, true
}
func main() {
	raw, err := io.ReadAll(io.LimitReader(os.Stdin, 32769))
	if err != nil || len(raw) > 32768 {
		os.Exit(1)
	}
	var request struct {
		View  string            `json:"view"`
		Input map[string]string `json:"input"`
	}
	if json.NewDecoder(bytes.NewReader(raw)).Decode(&request) != nil || request.View != "validate" {
		os.Exit(1)
	}
	value, ok := validate(request.Input["metadata"])
	result := "invalid"
	if ok {
		encoded, _ := json.Marshal(value)
		result = string(encoded)
	}
	if err := json.NewEncoder(os.Stdout).Encode(struct {
		Text string `json:"text"`
	}{result}); err != nil {
		fmt.Fprintln(os.Stderr, "output failed")
		os.Exit(1)
	}
}
