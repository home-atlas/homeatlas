package domain

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

const MaxFileSize int64 = 20 << 20
const MetadataLimit = 16 << 10

var StorageKey = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

type Item struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	Category    string   `json:"category"`
	CreatedAt   string   `json:"createdAt"`
	Description string   `json:"description"`
	Tags        []string `json:"tags"`
	Filename    string   `json:"filename"`
	MediaType   string   `json:"mediaType"`
	Size        int64    `json:"size"`
	SHA256      string   `json:"sha256"`
	OwnerID     string   `json:"ownerId"`
	OwnerName   string   `json:"ownerName"`
	FileKey     string   `json:"-"`
	HasFile     bool     `json:"hasFile"`
}
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
type Metadata struct {
	Title       string   `json:"title"`
	Category    string   `json:"category"`
	Description string   `json:"description"`
	Tags        []string `json:"tags"`
}
type Recipient struct {
	UserID   string `json:"userId"`
	Name     string `json:"name"`
	Access   string `json:"access"`
	MemberID string `json:"-"`
}

func Text(value string, max int, multiline bool) bool {
	if !utf8.ValidString(value) || utf8.RuneCountInString(value) > max {
		return false
	}
	for _, char := range value {
		if unicode.IsControl(char) && !(multiline && (char == '\n' || char == '\r' || char == '\t')) {
			return false
		}
	}
	return true
}
func SafeFilename(value string) bool {
	return value != "" && len(value) <= 255 && Text(value, 180, false) && !strings.ContainsAny(value, "/\\:") && value == strings.TrimSpace(value)
}
