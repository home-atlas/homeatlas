package domain

import (
	"fmt"
	"regexp"
)

var identifier = regexp.MustCompile(`^[a-z][a-z0-9-]{0,47}$`)

func ValidateDefinition(id, name, version string, definition Definition) error {
	c := struct {
		ID, Name, Version string
		Definition
	}{id, name, version, definition}
	if !identifier.MatchString(c.ID) || c.Name == "" || c.Version == "" || c.MaxFileBytes < 1 || c.MaxFileBytes > MaxFileSize || len(c.Categories) < 1 || len(c.Categories) > 32 || c.TitleMaxLength < 1 || c.TitleMaxLength > 1024 || c.DescriptionMaxLength < 1 || c.DescriptionMaxLength > 16000 || c.MaxTags < 0 || c.MaxTags > 64 || c.TagMaxLength < 1 || c.TagMaxLength > 256 || c.Title == "" || len(c.Title) > 120 || len(c.Description) > 1000 || len(c.MediaTypes) < 1 || len(c.MediaTypes) > 4 {
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
		if !StorageKey.MatchString(option.Value) || option.Label == "" || len(option.Label) > 120 || seen[option.Value] {
			return fmt.Errorf("invalid collection category")
		}
		seen[option.Value] = true
	}
	return nil
}
