package application

import (
	"context"
	"errors"
	"testing"

	"github.com/home-atlas/homeatlas/internal/core/access"
	"github.com/home-atlas/homeatlas/internal/core/fault"
	"github.com/home-atlas/homeatlas/internal/core/identity/domain"
	"github.com/home-atlas/homeatlas/internal/core/unitofwork"
)

type applicationTransaction struct {
	committed, rolledBack bool
	commitError           error
}

func (t *applicationTransaction) Commit() error {
	if t.commitError != nil {
		return t.commitError
	}
	t.committed = true
	return nil
}
func (t *applicationTransaction) Rollback() error {
	if !t.committed {
		t.rolledBack = true
	}
	return nil
}

type memberRepository struct {
	Repository
	tx                   *applicationTransaction
	actor, target        access.Member
	deleted, RevokedUnix bool
	revokeError          error
}

func (s *memberRepository) Begin(context.Context) (unitofwork.Transaction, error) { return s.tx, nil }
func (s *memberRepository) check(tx unitofwork.Transaction) {
	if tx != s.tx {
		panic("authorization and writes escaped the transaction")
	}
}
func (s *memberRepository) ReadMember(_ context.Context, tx unitofwork.Transaction, home, user string) (access.Member, error) {
	s.check(tx)
	if home != "home" {
		return access.Member{}, unitofwork.ErrNotFound
	}
	if user == "owner" {
		return s.actor, nil
	}
	return s.target, nil
}
func (s *memberRepository) DeleteMembership(_ context.Context, tx unitofwork.Transaction, home, user string) (unitofwork.Mutation, error) {
	s.check(tx)
	s.deleted = true
	return unitofwork.Mutation{Affected: 1}, nil
}
func (s *memberRepository) RevokeMemberInvitations(_ context.Context, tx unitofwork.Transaction, now int64, home, user, email string) (unitofwork.Mutation, error) {
	s.check(tx)
	s.RevokedUnix = true
	return unitofwork.Mutation{Affected: 1}, s.revokeError
}

// A non-SQL port proves that application authorization and atomicity do not rely on HTTP or a driver.
func TestIdentityApplicationPreservesMemberTransaction(t *testing.T) {
	for _, scenario := range []string{"success", "stale-actor", "protected-owner", "revocation-fails", "commit-fails"} {
		t.Run(scenario, func(t *testing.T) {
			store := &memberRepository{tx: &applicationTransaction{}, actor: access.Member{ID: "owner", MembershipID: "current", Role: "owner", Permissions: []string{Manage}}, target: access.Member{ID: "reader", Role: "member"}}
			switch scenario {
			case "stale-actor":
				store.actor.MembershipID = "rejoined"
			case "protected-owner":
				store.target.Role = "owner"
			case "revocation-fails":
				store.revokeError = errors.New("write failed")
			case "commit-fails":
				store.tx.commitError = errors.New("commit failed")
			}
			app := &Application{store: store}
			ctx := access.WithPrincipal(context.Background(), access.Principal{HouseholdID: "home", UserID: "owner", MembershipID: "current"})
			_, err := app.ChangeMember(ctx, Client{}, "reader", GrantInput{}, true)
			if scenario == "success" {
				if err != nil || !store.tx.committed || !store.deleted || !store.RevokedUnix {
					t.Fatalf("successful atomic removal: %v %+v", err, store)
				}
				return
			}
			if err == nil || store.tx.committed || !store.tx.rolledBack {
				t.Fatalf("failed operation committed: %v %+v", err, store)
			}
			if scenario == "stale-actor" || scenario == "protected-owner" {
				if store.deleted || store.RevokedUnix {
					t.Fatal("protected membership was changed")
				}
			}
			if scenario == "stale-actor" && fault.From(err).Kind != fault.Unauthenticated {
				t.Fatal(err)
			}
		})
	}
}

type sessionRepository struct {
	Repository
	session *Session
	failure error
}

func (s *sessionRepository) ReadSessionRecord(_ context.Context, _ []byte, now int64) (*Session, int64, error) {
	return s.session, now, s.failure
}
func TestIdentityAuthenticationWithoutHTTP(t *testing.T) {
	record := &Session{User: User{ID: "reader"}, Household: Household{ID: "home"}, MembershipID: "reader-current", Permissions: []string{"documents.read"}}
	store := &sessionRepository{session: record}
	app := &Application{store: store}
	// Previous trusted context must never add permissions to a newly authenticated session.
	prior := access.WithPrincipal(context.Background(), access.Principal{UserID: "owner", HouseholdID: "other", MembershipID: "owner-current", Permissions: map[string]bool{Manage: true}})
	ctx, err := app.Authenticate(prior, domain.RandomCode())
	if err != nil {
		t.Fatal(err)
	}
	p, ok := access.Current(ctx)
	if !ok || p.UserID != "reader" || p.HouseholdID != "home" || p.MembershipID != "reader-current" || access.Allows(ctx, Manage) || !access.Allows(ctx, "documents.read") {
		t.Fatal("authentication inherited another identity", p)
	}
	if current, _ := ctx.Value(sessionKey{}).(*Session); current != record {
		t.Fatal("application session context missing")
	}
	store.failure = unitofwork.ErrNotFound
	failed, err := app.Authenticate(ctx, domain.RandomCode())
	if !errors.Is(err, unitofwork.ErrNotFound) {
		t.Fatal(err)
	}
	if _, ok := access.Current(failed); ok {
		t.Fatal("failed authentication retained principal")
	}
	if current, _ := failed.Value(sessionKey{}).(*Session); current != nil {
		t.Fatal("failed authentication retained session")
	}
}
