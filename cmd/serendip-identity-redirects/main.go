// Command serendip-identity-redirects brings an existing install's
// serendip client in the identity provider (ThunderID) up to what a fresh
// install sets: the web console's login callback and post-logout page, and
// the token attributes, including the "groups" claim authorization reads
// (specs/017-authorization plan.md decision 9).
//
// It's the documented one-time operator step for an install that predates
// the console or group-based access, or after SERENDIP_PUBLIC_URL changes
// (docs/architecture/identity.md "Console sessions"). Fresh installs never
// need it. Without --public-url it only adds missing token attributes. It talks to ThunderID through a port-forward the operator opens,
// because the Kubernetes Service proxy strips the Authorization header the
// management API needs:
//
//	kubectl -n serendip-identity port-forward svc/thunderid-service 18090:8090
//	SERENDIP_IDENTITY_ADMIN_PASSWORD=... serendip-identity-redirects \
//	    --public-url http://localhost:8080
//
// The password is read from the environment, never a flag, so it stays out
// of shell history and process listings. Nothing is printed but the
// outcome.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/serendip-infra/serendip-infra/api"
	"github.com/serendip-infra/serendip-infra/providers/identity/thunderid"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "serendip-identity-redirects:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	fs := flag.NewFlagSet("serendip-identity-redirects", flag.ContinueOnError)
	base := fs.String("thunderid", "http://127.0.0.1:18090", "ThunderID reached through a port-forward (not the Kubernetes Service proxy)")
	issuer := fs.String("issuer", "http://identity.localhost:30080", "the identity provider's public issuer URL, as serendipd logs it")
	public := fs.String("public-url", "", "the console's public URL, as SERENDIP_PUBLIC_URL; omit to only add missing token attributes")
	insecure := fs.Bool("allow-insecure-origin", false, "accept a plain-http non-localhost public URL, as SERENDIP_ALLOW_INSECURE_ORIGIN=1")
	if err := fs.Parse(args); err != nil {
		return err
	}
	var callback, postLogout string
	if *public != "" {
		origin, err := api.ParseOrigin(*public, *insecure)
		if err != nil {
			return err
		}
		callback, postLogout = origin.String()+"/auth/callback", origin.String()+"/"
	}
	password := os.Getenv("SERENDIP_IDENTITY_ADMIN_PASSWORD")
	if password == "" {
		return errors.New("set SERENDIP_IDENTITY_ADMIN_PASSWORD (from the Secret serendip-identity/serendip-identity-admin, key password)")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if err := thunderid.UpdateClient(ctx, *base, &http.Client{Timeout: 15 * time.Second}, *issuer, password, callback, postLogout); err != nil {
		return err
	}
	if callback != "" {
		fmt.Printf("the serendip client accepts %s and %s\n", callback, postLogout)
	}
	fmt.Println("the serendip client's access tokens carry username, email, name and groups")
	return nil
}
