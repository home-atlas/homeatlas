package access

import (
	"context"
	"database/sql"
)

// Member identifies one continuous period of access to a household.
// MembershipID changes after removal and rejoining, so old grants cannot revive.
type Member struct {
	ID           string   `json:"id"`
	MembershipID string   `json:"-"`
	Name         string   `json:"name"`
	Email        string   `json:"email,omitempty"`
	Username     string   `json:"username,omitempty"`
	Assisted     bool     `json:"assisted,omitempty"`
	Role         string   `json:"role"`
	Permissions  []string `json:"permissions"`
}

// Directory is the Core boundary used by modules; its tables remain private.
type Directory interface {
	Member(context.Context, *sql.Tx, string, string) (Member, error)
}
