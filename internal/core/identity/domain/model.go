package domain

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"net/mail"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/home-atlas/homeatlas/internal/core/access"
)

const (
	SessionLifetime    = 12 * time.Hour
	SessionIdle        = 30 * time.Minute
	AttemptWindow      = 15 * time.Minute
	RecoveryLifetime   = 15 * time.Minute
	PINDeviceLifetime  = 30 * 24 * time.Hour
	InvitationLifetime = 72 * time.Hour
	Manage             = "household.manage"
	passwordIterations = 600_000
)

var usernamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{2,39}$`)

func ValidText(value string, max int) bool {
	if !utf8.ValidString(value) || utf8.RuneCountInString(value) < 1 || utf8.RuneCountInString(value) > max {
		return false
	}
	for _, char := range value {
		if unicode.IsControl(char) {
			return false
		}
	}
	return true
}
func ValidTimezone(value string) bool {
	if value == "" || value == "Local" || len(value) > 100 {
		return false
	}
	_, err := time.LoadLocation(value)
	return err == nil
}
func NormalizeUsername(value string) (string, bool) {
	value = strings.ToLower(strings.TrimSpace(value))
	return value, usernamePattern.MatchString(value)
}
func NormalizeEmail(email string) (string, bool) {
	email = strings.ToLower(strings.TrimSpace(email))
	address, err := mail.ParseAddress(email)
	return email, err == nil && address.Address == email && len(email) <= 254
}
func OptionalEmail(value string) (string, bool) {
	if strings.TrimSpace(value) == "" {
		return "", true
	}
	return NormalizeEmail(value)
}
func NormalizeLogin(value string) (string, bool) {
	if strings.Contains(value, "@") {
		return NormalizeEmail(value)
	}
	return NormalizeUsername(value)
}
func PinRole(role string) bool { return role == "member" || role == "restricted" || role == "guest" }
func Manager(m access.Member) bool {
	return (m.Role == "owner" || m.Role == "admin") && slices.Contains(m.Permissions, Manage)
}
func Editable(actor, target access.Member) bool {
	return target.ID != actor.ID && target.Role != "owner" && (actor.Role == "owner" || target.Role != "admin")
}
func NormalizedPermissions(permissions []string) []string {
	result := []string{}
	for _, cap := range permissions {
		if cap != Manage {
			result = append(result, strings.TrimSpace(cap))
		}
	}
	return result
}
func TokenDigest(token string) ([]byte, bool) {
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if len(token) != 43 || err != nil || len(raw) != 32 {
		return nil, false
	}
	digest := sha256.Sum256([]byte(token))
	return digest[:], true
}
func RandomCode() string {
	var raw [32]byte
	// crypto/rand.Read terminates the process if the platform CSPRNG fails.
	rand.Read(raw[:])
	return base64.RawURLEncoding.EncodeToString(raw[:])
}
func RestoredUsername(m BackupMember) string {
	if m.Username != "" {
		return m.Username
	}
	return "u-" + strings.ToLower(m.ID)
}
func ValidNewPassword(password string) bool {
	length := utf8.RuneCountInString(password)
	return utf8.ValidString(password) && length >= 8 && length <= 128 && len(password) <= 512
}
func HashPassword(password string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key, err := pbkdf2.Key(sha256.New, password, salt, passwordIterations, 32)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("pbkdf2-sha256$%d$%s$%s", passwordIterations, base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key)), nil
}
func ParsePasswordHash(encoded string) (int, []byte, []byte, bool) {
	if len(encoded) > 200 {
		return 0, nil, nil, false
	}
	parts := strings.Split(encoded, "$")
	if len(parts) != 4 || parts[0] != "pbkdf2-sha256" {
		return 0, nil, nil, false
	}
	iterations, err := strconv.Atoi(parts[1])
	if err != nil || iterations < passwordIterations || iterations > 2_000_000 {
		return 0, nil, nil, false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[2])
	if err != nil || len(salt) != 16 {
		return 0, nil, nil, false
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[3])
	if err != nil || len(want) != 32 {
		return 0, nil, nil, false
	}
	return iterations, salt, want, true
}
func ValidPasswordHash(encoded string) bool { _, _, _, ok := ParsePasswordHash(encoded); return ok }
func VerifyPassword(password, encoded string) bool {
	iterations, salt, want, ok := ParsePasswordHash(encoded)
	if !ok {
		return false
	}
	key, err := pbkdf2.Key(sha256.New, password, salt, iterations, 32)
	return err == nil && subtle.ConstantTimeCompare(key, want) == 1
}

type User struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Email         string `json:"email"`
	EmailVerified bool   `json:"emailVerified"`
	Username      string `json:"username"`
}
type Household struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Timezone string `json:"timezone"`
}
type Session struct {
	ID           string    `json:"id"`
	User         User      `json:"user"`
	Household    Household `json:"household"`
	Role         string    `json:"role"`
	Permissions  []string  `json:"permissions"`
	MembershipID string    `json:"-"`
	ExpiresAt    time.Time `json:"expiresAt"`
}
type State struct {
	SetupRequired bool     `json:"setupRequired"`
	Session       *Session `json:"session"`
	SMTPEnabled   bool     `json:"smtpEnabled"`
}
type BackupMember struct {
	ID, Name, Email, Role, MembershipID string
	Username                            string
	Assisted                            bool
	PasswordHash                        string `json:"-"`
	Permissions                         []string
}
type Invitation struct {
	ID             string    `json:"id"`
	Email          string    `json:"email"`
	Role           string    `json:"role"`
	Permissions    []string  `json:"permissions"`
	ExpiresAt      time.Time `json:"expiresAt"`
	Status         string    `json:"status"`
	HouseholdID    string    `json:"-"`
	IssuerID       string    `json:"-"`
	IssuerMemberID string    `json:"-"`
	HouseholdName  string    `json:"-"`
	ExpiresUnix    int64     `json:"-"`
	AcceptedUnix   int64     `json:"-"`
	RevokedUnix    int64     `json:"-"`
}
