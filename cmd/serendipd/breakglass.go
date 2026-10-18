package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/user"
	"strings"
	"time"
	"unicode"

	"github.com/serendip-infra/serendip-infra/core/audit"
	"github.com/serendip-infra/serendip-infra/core/policy"
	"github.com/serendip-infra/serendip-infra/core/resource"
	"github.com/serendip-infra/serendip-infra/core/store"
	"github.com/serendip-infra/serendip-infra/core/store/sqlite"
)

// breakGlassInterface is the audit interface of a break-glass grant, so it
// stands apart from every API, console and bootstrap change.
const breakGlassInterface = "break-glass"

// runBreakGlass is `serendipd break-glass grant-owner`: recovery when nobody
// who can sign in is Owner any more (specs/017-authorization plan.md
// decision 7, Azure's "elevate access"). It opens the store file directly,
// which takes local access to the control-plane host; there is no API for
// it. It grants one person Owner at "/", audited with its own interface,
// and works while serendipd runs: serendipd's periodic policy reload picks
// the grant up within 30 s.
func runBreakGlass(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 || args[0] != "grant-owner" {
		return errors.New("usage: serendipd break-glass grant-owner (--subject <sub> | --username <name>) [--db <path>]")
	}
	fs := flag.NewFlagSet("break-glass grant-owner", flag.ContinueOnError)
	fs.SetOutput(stderr)
	subject := fs.String("subject", "", "the person's subject (sub) at the identity provider")
	username := fs.String("username", "", "the person's username, looked up among people who have signed in")
	dbPath := fs.String("db", envOr("SERENDIP_DB_PATH", "serendip.db"), "serendipd's store file")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if (*subject == "") == (*username == "") {
		return errors.New("give exactly one of --subject or --username")
	}
	// Never create a store: a mistyped path must not "succeed" on an empty
	// database nobody reads.
	if _, err := os.Stat(*dbPath); err != nil {
		return fmt.Errorf("store %s: %w", *dbPath, err)
	}
	db, err := sqlite.Open(ctx, *dbPath)
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer db.Close()
	if err := sqlite.Migrate(ctx, db); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}

	sub := *subject
	if sub == "" {
		if sub, err = subjectOf(ctx, sqlite.NewPrincipalRegistry(db), *username); err != nil {
			return err
		}
	}
	assignments := sqlite.NewStore[resource.RoleAssignmentSpec, resource.RoleAssignmentStatus](db, resource.KindRoleAssignment)
	all, err := assignments.List(ctx, store.ListOptions{})
	if err != nil {
		return err
	}
	for _, a := range all {
		if a.Spec.Principal.Type == resource.PrincipalPerson && a.Spec.Principal.ID == sub &&
			a.Spec.RoleDefinitionID == policy.OwnerRoleID && a.Spec.Scope.IsRoot() {
			fmt.Fprintf(stdout, "%s is already Owner at \"/\" (assignment %s); nothing changed\n", sub, a.Metadata.ID)
			return nil
		}
	}

	ctx = audit.WithInterface(audit.WithActor(ctx, breakGlassActor()), breakGlassInterface)
	created, err := assignments.Create(ctx, resource.RoleAssignment{
		Metadata: resource.Metadata{Name: "break-glass-owner"},
		Spec: resource.RoleAssignmentSpec{
			Principal:        resource.PrincipalRef{Type: resource.PrincipalPerson, ID: sub},
			RoleDefinitionID: policy.OwnerRoleID,
			Scope:            resource.RootScope,
			Description:      "break-glass grant at " + time.Now().UTC().Format(time.RFC3339),
		},
	})
	if err != nil {
		return fmt.Errorf("grant Owner: %w", err)
	}
	fmt.Fprintf(stderr, "WARNING: break-glass: %s is now Owner at \"/\" (assignment %s). This is recorded in the audit log.\n", sub, created.Metadata.ID)
	fmt.Fprintln(stderr, "A running serendipd applies it within 30 seconds. Review and narrow it once access is restored.")
	return nil
}

// breakGlassActor names who ran the command, for the audit row: the user
// who invoked sudo, else the OS account. Host access is the trust root
// here, so this is a record, not a credential; the fixed "break-glass"
// prefix and interface are what identify the entry.
func breakGlassActor() string {
	name := os.Getenv("SUDO_USER")
	if name == "" {
		if u, err := user.Current(); err == nil {
			name = u.Username
		}
	}
	name = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, name)
	if len(name) > 64 {
		name = name[:64]
	}
	if name == "" {
		return "break-glass"
	}
	return "break-glass:" + name
}

// subjectOf resolves a username among the people who have signed in. It
// refuses a name that's unknown or shared, rather than guess whom to make
// Owner.
func subjectOf(ctx context.Context, reg *sqlite.PrincipalRegistry, username string) (string, error) {
	people, err := reg.List(ctx, resource.PrincipalPerson)
	if err != nil {
		return "", err
	}
	var found []string
	for _, p := range people {
		if p.DisplayName == username {
			found = append(found, p.ID)
		}
	}
	switch len(found) {
	case 0:
		return "", fmt.Errorf("nobody named %q has signed in to serendipd; use --subject", username)
	case 1:
		return found[0], nil
	}
	return "", fmt.Errorf("%d people are named %q (%s); use --subject", len(found), username, strings.Join(found, ", "))
}
