package domain

import (
	"encoding/json"
	"regexp"
)

const ContractVersion = "1"

type Navigation struct {
	Label string `json:"label"`
	Path  string `json:"path"`
}

type Manifest struct {
	Collection      json.RawMessage `json:"collection,omitempty"`
	ID              string          `json:"id"`
	Name            string          `json:"name"`
	Version         string          `json:"version"`
	ContractVersion string          `json:"contractVersion"`
	Runtime         string          `json:"runtime,omitempty"`
	Capabilities    []string        `json:"capabilities"`
	Dependencies    []string        `json:"dependencies"`
	Navigation      Navigation      `json:"navigation"`
}

type SearchResult struct {
	Module string `json:"module"`
	ID     string `json:"id"`
	Title  string `json:"title"`
	Path   string `json:"path"`
}

type Status struct {
	Manifest
	Enabled          bool   `json:"enabled"`
	Available        bool   `json:"available"`
	AccessPermission string `json:"accessPermission"`
}

var identifier = regexp.MustCompile(`^[a-z][a-z0-9-]{0,47}$`)
var reserved = map[string]bool{"api": true, "assets": true, "core": true, "identity": true, "schema": true, "module": true, "plugin": true, "backups": true, "plugins": true, "plugin-files": true, "modules": true, "health": true, "search": true, "auth": true, "setup": true, "household": true, "invitations": true, "settings": true, "family": true, "join": true, "login": true, "recover": true, "verify-email": true, "activate-access": true, "forgot-password": true, "home": true}

// ValidID shares the Core's namespace boundary with package installation.
func ValidID(id string) bool { return identifier.MatchString(id) && !reserved[id] }
