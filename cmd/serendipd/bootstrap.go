package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"

	"github.com/serendip-infra/serendip-infra/core/audit"
	"github.com/serendip-infra/serendip-infra/core/event"
	"github.com/serendip-infra/serendip-infra/core/policy"
	"github.com/serendip-infra/serendip-infra/core/resource"
	"github.com/serendip-infra/serendip-infra/core/store/sqlite"
)

// bootstrapAdminRoles are what the identity provider's bootstrap
// administrator gets at "/" (specs/017-authorization plan.md decision 7):
// full control, and Secret values, which Owner alone doesn't reach.
var bootstrapAdminRoles = []string{policy.OwnerRoleID, policy.SecretsOfficerRoleID}

// bootstrapAdmin makes the identity provider's bootstrap administrator
// (subject) Owner and Secrets Officer at "/": each role it has never held.
// "Never held" rather than "is there any assignment at all": an install
// upgraded from before authorization already has its API keys'
// assignments, and its administrator must still be able to sign in and
// act; an operator who later removes either role isn't overruled by a
// restart; and a start interrupted between the two grants finishes the
// second next time.
func bootstrapAdmin(ctx context.Context, db *sql.DB, bus event.Bus, subject string) error {
	ctx = audit.WithInterface(audit.WithActor(ctx, "serendipd"), "bootstrap")
	s := sqlite.NewStore[resource.RoleAssignmentSpec, resource.RoleAssignmentStatus](db, resource.KindRoleAssignment).SetBus(bus)
	granted := 0
	for _, role := range bootstrapAdminRoles {
		ever, err := sqlite.EverAssignedRole(ctx, db, resource.PrincipalPerson, subject, role)
		if err != nil {
			return err
		}
		if ever {
			continue
		}
		_, err = s.Create(ctx, resource.RoleAssignment{
			Metadata: resource.Metadata{Name: "bootstrap-admin"},
			Spec: resource.RoleAssignmentSpec{
				Principal:        resource.PrincipalRef{Type: resource.PrincipalPerson, ID: subject},
				RoleDefinitionID: role,
				Scope:            resource.RootScope,
				Description:      "bootstrap administrator (spec 017)",
			},
		})
		if err != nil {
			return fmt.Errorf("bootstrap administrator: %w", err)
		}
		granted++
	}
	if granted == 0 {
		return nil
	}
	log.Printf("serendipd: the identity provider's bootstrap administrator (%s) is Owner and Secrets Officer at \"/\"", subject)
	return nil
}
