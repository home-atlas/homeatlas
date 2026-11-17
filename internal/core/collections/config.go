// Package collections implements the host-owned file/metadata/ACL primitives
// available to isolated plugins. Package rules run through Validate, never SQL.
package collections

import (
	"context"
	"fmt"
	"regexp"

	"github.com/home-atlas/homeatlas/internal/core/objectstore"
)

type Option struct {
	Value string `json:"value"`
	Label string `json:"label"`
}
type Definition struct {
	TitleMaxLength       int      `json:"titleMaxLength"`
	DescriptionMaxLength int      `json:"descriptionMaxLength"`
	MaxTags              int      `json:"maxTags"`
	TagMaxLength         int      `json:"tagMaxLength"`
	Title                string   `json:"title"`
	Description          string   `json:"description"`
	Categories           []Option `json:"categories"`
	MediaTypes           []string `json:"mediaTypes"`
	MaxFileBytes         int64    `json:"maxFileBytes"`
	LegacySnapshot       bool     `json:"legacySnapshot,omitempty"`
}
type Config struct {
	Storage           *objectstore.Store
	ID, Name, Version string
	Definition
	Validate func(context.Context, []byte) ([]byte, error)
}

var identifier = regexp.MustCompile(`^[a-z][a-z0-9-]{0,47}$`)

func (c Config) Valid() error {
	if !identifier.MatchString(c.ID) || c.Name == "" || c.Version == "" || c.MaxFileBytes < 1 || c.MaxFileBytes > MaxFileSize || len(c.Categories) < 1 || len(c.Categories) > 32 || c.TitleMaxLength < 1 || c.TitleMaxLength > 1024 || c.DescriptionMaxLength < 1 || c.DescriptionMaxLength > 16000 || c.MaxTags < 0 || c.MaxTags > 64 || c.TagMaxLength < 1 || c.TagMaxLength > 256 || c.Validate == nil || c.Title == "" || len(c.Title) > 120 || len(c.Description) > 1000 || len(c.MediaTypes) < 1 || len(c.MediaTypes) > 4 {
		return fmt.Errorf("invalid collection configuration")
	}
	types := map[string]bool{}
	for _, typ := range c.MediaTypes {
		if types[typ] || (typ != "application/pdf" && typ != "image/png" && typ != "image/jpeg" && typ != "text/plain") {
			return fmt.Errorf("invalid collection media type")
		}
		types[typ] = true
	}
	seen := map[string]bool{}
	for _, option := range c.Categories {
		if !storageKey.MatchString(option.Value) || option.Label == "" || len(option.Label) > 120 || seen[option.Value] {
			return fmt.Errorf("invalid collection category")
		}
		seen[option.Value] = true
	}
	return nil
}
func (c Config) category(value string) bool {
	for _, option := range c.Categories {
		if option.Value == value {
			return true
		}
	}
	return false
}
