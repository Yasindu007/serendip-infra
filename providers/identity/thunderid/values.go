package thunderid

import (
	"encoding/json"
	"fmt"
)

// Chart coordinates and the names everything else is derived from. The
// chart's appVersion and the image tag are pinned together (T001: chart
// 1.0.1 ships appVersion 1.0.1).
const (
	Chart        = "oci://ghcr.io/thunder-id/helm-charts/thunderid"
	ChartVersion = "1.0.1"
	ImageTag     = "1.0.1"

	Namespace = "serendip-identity"
	Release   = "thunderid"

	// ServiceName/ServicePort are fixed by the chart (<release>-service,
	// port 8090).
	ServiceName = "thunderid-service"
	ServicePort = 8090

	// ClientID is the public OIDC client Serendip's human-facing clients
	// (the future UI and CLI) log in through. Fixed, so nothing has to be
	// stored to recognise it (decision 4).
	ClientID = "serendip"
	// LoopbackRedirectURI is the client's only redirect URI: a fixed
	// loopback, used by verification now and a CLI later. ThunderID matches
	// redirect URIs exactly, and bootstrap files are only imported on first
	// install.
	LoopbackRedirectURI = "http://127.0.0.1:8250/callback"

	// AdminUsername is the bootstrap administrator the chart's setup job
	// creates; its password lives in AdminSecretName/AdminSecretKey.
	AdminUsername   = "admin"
	AdminSecretName = "serendip-identity-admin"
	AdminSecretKey  = "password"

	// AdminSubject is the bootstrap administrator's subject ("sub") in
	// access tokens: the fixed user ID in the shipped bootstrap resources,
	// the same on every install (specs/017-authorization T001 finding 1).
	// serendipd makes it Owner at "/" on first start.
	AdminSubject = "01900000-0000-7000-8000-000000000030"

	// The default organization unit and authentication flow the shipped
	// bootstrap resources create (01-default-resources.yaml, 1.0.1).
	defaultOUID       = "01900000-0000-7000-8000-000000000001"
	defaultAuthFlowID = "01900000-0000-7000-8000-000000000061"
	// defaultSignOutFlowID is the shipped "Default Sign Out Flow". Without
	// it, RP-initiated logout fails with "Sign-out flow is not configured
	// for the application" (spec 016 T010).
	defaultSignOutFlowID = "01900000-0000-7000-8000-00000000006a"
	// clientAppID is our application's own fixed ID, in the bootstrap
	// file's ID space.
	clientAppID = "01900000-0000-7000-8000-0000000005e1"

	bootstrapFile = "70-serendip-client.yaml"
)

// tokenAttributes are the user attributes copied into access tokens:
// "username" becomes the audit actor (decision 8); "groups" lists the
// person's group names for authorization (spec 017 plan.md decision 9).
// UpdateClient adds any of them missing on an existing install.
var tokenAttributes = []string{"username", "email", "name", "groups"}

// ConsoleRedirects are the web console's login callback and post-logout
// page (specs/016-ui-mvp plan.md decision 7). Empty fields are left out.
type ConsoleRedirects struct {
	Callback, PostLogout string
}

// clientDocument is the bootstrap resource declaring the serendip client,
// in the shape of the shipped Console application (T001 spike: imported
// and logged in against as written). JSON is valid YAML, so it is
// rendered with encoding/json.
func clientDocument(console ConsoleRedirects) map[string]any {
	redirects := []string{LoopbackRedirectURI}
	postLogout := []string{LoopbackRedirectURI}
	if console.Callback != "" {
		redirects = append(redirects, console.Callback)
	}
	if console.PostLogout != "" {
		postLogout = append(postLogout, console.PostLogout)
	}
	return map[string]any{
		"resource_type":             "application",
		"id":                        clientAppID,
		"name":                      "Serendip",
		"description":               "Serendip API, UI and CLI human login",
		"type":                      "browser",
		"ouId":                      defaultOUID,
		"authFlowId":                defaultAuthFlowID,
		"signOutFlowId":             defaultSignOutFlowID,
		"isRegistrationFlowEnabled": false,
		"allowedUserTypes":          []string{"Person"},
		"inboundAuthConfig": []any{map[string]any{
			"type": "oauth2",
			"config": map[string]any{
				"clientId":                ClientID,
				"redirectUris":            redirects,
				"postLogoutRedirectUris":  postLogout,
				"grantTypes":              []string{"authorization_code", "refresh_token"},
				"responseTypes":           []string{"code"},
				"pkceRequired":            true,
				"tokenEndpointAuthMethod": "none",
				"publicClient":            true,
				"token": map[string]any{
					"accessToken": map[string]any{
						"userConfig": map[string]any{"validityPeriod": 3600, "attributes": tokenAttributes},
					},
					"idToken": map[string]any{"validityPeriod": 3600, "userAttributes": tokenAttributes},
				},
			},
		}},
	}
}

// values renders the chart values for issuer (decision 1), as JSON (valid
// YAML for helm -f).
func values(issuer string, console ConsoleRedirects) ([]byte, error) {
	client, err := json.Marshal(clientDocument(console))
	if err != nil {
		return nil, fmt.Errorf("render client document: %w", err)
	}
	sqlite := map[string]string{"type": "sqlite"}
	v := map[string]any{
		"deployment": map[string]any{
			"replicaCount": 1,
			"image":        map[string]any{"tag": ImageTag, "pullPolicy": "IfNotPresent"},
			"resources": map[string]any{
				"requests": map[string]string{"cpu": "100m", "memory": "64Mi"},
				"limits":   map[string]string{"cpu": "1", "memory": "256Mi"},
			},
		},
		"hpa":     map[string]any{"enabled": false},
		"ingress": map[string]any{"enabled": false},
		"configuration": map[string]any{
			"server": map[string]any{"httpOnly": true, "publicUrl": issuer},
			"database": map[string]any{
				"config": sqlite, "entity": sqlite, "runtime_persistent": sqlite, "runtime_transient": sqlite,
			},
		},
		"setup": map[string]any{
			"admin": map[string]any{
				"username":    AdminUsername,
				"passwordRef": map[string]string{"name": AdminSecretName, "key": AdminSecretKey},
			},
		},
		"bootstrap": map[string]any{"scripts": map[string]string{bootstrapFile: string(client)}},
	}
	return json.MarshalIndent(v, "", "  ")
}
