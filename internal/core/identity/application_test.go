package identity

import (
	"context"
	"errors"
	"testing"

	"github.com/home-atlas/homeatlas/internal/core/access"
	"github.com/home-atlas/homeatlas/internal/core/fault"
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
	tx               *applicationTransaction
	actor, target    access.Member
	deleted, revoked bool
	revokeError      error
}

func (s *memberRepository) Begin(context.Context) (unitofwork.Transaction, error) { return s.tx, nil }
func (s *memberRepository) check(tx unitofwork.Transaction) {
	if tx != s.tx {
		panic("authorization and writes escaped the transaction")
	}
}
func (s *memberRepository) readMember(_ context.Context, tx unitofwork.Transaction, home, user string) (access.Member, error) {
	s.check(tx)
	if home != "home" {
		return access.Member{}, unitofwork.ErrNotFound
	}
	if user == "owner" {
		return s.actor, nil
	}
	return s.target, nil
}
func (s *memberRepository) deleteMembership(_ context.Context, tx unitofwork.Transaction, home, user string) (unitofwork.Mutation, error) {
	s.check(tx)
	s.deleted = true
	return unitofwork.Mutation{Affected: 1}, nil
}
func (s *memberRepository) revokeMemberInvitations(_ context.Context, tx unitofwork.Transaction, now int64, home, user, email string) (unitofwork.Mutation, error) {
	s.check(tx)
	s.revoked = true
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
				if err != nil || !store.tx.committed || !store.deleted || !store.revoked {
					t.Fatalf("successful atomic removal: %v %+v", err, store)
				}
				return
			}
			if err == nil || store.tx.committed || !store.tx.rolledBack {
				t.Fatalf("failed operation committed: %v %+v", err, store)
			}
			if scenario == "stale-actor" || scenario == "protected-owner" {
				if store.deleted || store.revoked {
					t.Fatal("protected membership was changed")
				}
			}
			if scenario == "stale-actor" && fault.From(err).Kind != fault.Unauthenticated {
				t.Fatal(err)
			}
		})
	}
}
