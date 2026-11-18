package application

import (
	"context"

	"github.com/home-atlas/homeatlas/internal/core/access"
)

type Client struct {
	SessionToken, DeviceToken, RemoteAddress string
	HasSession, HasDevice                    bool
}
type Result struct {
	Data                      any
	SessionToken, DeviceToken string
	EndSession                bool
}

// Authenticate supplies the same principal/session context to HTTP and trusted in-process callers.
func (s *Application) Authenticate(ctx context.Context, token string) (context.Context, error) {
	ctx = access.WithPrincipal(ctx, access.Principal{})
	ctx = context.WithValue(ctx, sessionKey{}, (*Session)(nil))
	current, err := s.LoadSession(ctx, token)
	if err != nil {
		return ctx, err
	}
	grants := make(map[string]bool, len(current.Permissions))
	for _, permission := range current.Permissions {
		grants[permission] = true
	}
	ctx = access.WithPrincipal(ctx, access.Principal{MembershipID: current.MembershipID, UserID: current.User.ID, HouseholdID: current.Household.ID, Permissions: grants})
	return context.WithValue(ctx, sessionKey{}, current), nil
}
