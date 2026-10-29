package access

import "context"

// Principal is supplied by trusted authentication middleware, never request headers.
type Principal struct {
	MembershipID string
	UserID       string
	HouseholdID  string
	Permissions  map[string]bool
}

type principalKey struct{}

func WithPrincipal(ctx context.Context, principal Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, principal)
}

func Current(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(principalKey{}).(Principal)
	return p, ok && p.UserID != "" && p.HouseholdID != ""
}

func Allows(ctx context.Context, permission string) bool {
	p, ok := Current(ctx)
	return ok && permission != "" && p.Permissions[permission]
}
