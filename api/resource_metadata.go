package api

import (
	"net/http"
)

// ResourceMetadata is what clients learn about signing in to this API,
// served at /.well-known/oauth-protected-resource (RFC 9728, spec 021).
// The CLI reads it to find the identity provider, and MCP clients follow
// it from a 401's WWW-Authenticate.
type ResourceMetadata struct {
	// Issuer returns the identity provider's issuer, or "" until identity
	// is ready.
	Issuer func() string
	// CLIClient is the public client people sign in to the CLI through.
	CLIClient string
	// AssistantClient is the client a person's token is exchanged for when
	// they act through an AI assistant (spec 021 plan decision 1).
	AssistantClient string
}

// WithResourceMetadata serves m at /.well-known/oauth-protected-resource.
// Without it the path answers 404.
func WithResourceMetadata(m ResourceMetadata) ServerOption {
	return func(o *serverOptions) { o.metadata = &m }
}

// resourceMetadataPath is RFC 9728's well-known location.
const resourceMetadataPath = "/.well-known/oauth-protected-resource"

// resourceMetadataHandler answers the RFC 9728 document. It needs no
// credential: it names public endpoints only. The resource is the
// console's origin when known, or the address the client used.
func resourceMetadataHandler(m ResourceMetadata, origin string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		issuer := ""
		if m.Issuer != nil {
			issuer = m.Issuer()
		}
		if issuer == "" {
			w.Header().Set("Retry-After", "10")
			WriteProblem(w, http.StatusServiceUnavailable, "not ready", "the identity provider isn't ready yet; retry later", "")
			return
		}
		resource := origin
		w.Header().Set("Cache-Control", "max-age=60")
		if resource == "" {
			// From the request's own Host: fine for the caller, never to be
			// cached and served to anyone else.
			w.Header().Set("Cache-Control", "no-store")
			scheme := "http"
			if r.TLS != nil {
				scheme = "https"
			}
			resource = scheme + "://" + r.Host
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"resource":                 resource,
			"resource_name":            "Serendip",
			"authorization_servers":    []string{issuer},
			"bearer_methods_supported": []string{"header"},
			// Extensions (RFC 9728 allows more members): which clients to
			// use, so a CLI needs nothing configured but the server address.
			"serendip_cli_client_id":       m.CLIClient,
			"serendip_assistant_client_id": m.AssistantClient,
		})
	}
}
