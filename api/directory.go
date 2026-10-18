package api

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"slices"
	"strings"

	"github.com/serendip-infra/serendip-infra/core/audit"
	"github.com/serendip-infra/serendip-infra/core/identity"
	"github.com/serendip-infra/serendip-infra/core/resource"
	"github.com/serendip-infra/serendip-infra/core/store/sqlite"
)

// The identity provider's directory, read live (specs/018-agent-manager-poc
// plan.md decision 5): who can be granted a role, and a person's groups as
// they are now. When the directory can't be read, both routes answer from
// the principals seen at sign-in instead, and say so in "source".

const (
	// sourceLive marks an answer read from the identity provider just now.
	sourceLive = "live"
	// sourceSignedIn marks an answer from what principals carried when they
	// last signed in, used when the directory can't be read.
	sourceSignedIn = "signedIn"

	// maxDirectoryQuery bounds the search text.
	maxDirectoryQuery = 100
	// maxDirectoryItems bounds a fallback answer, matching the directory's
	// own bound.
	maxDirectoryItems = 50
)

// directoryItem is one person or group that can be granted a role; the
// field names match KnownPrincipal's, so clients treat both alike.
type directoryItem struct {
	Type        resource.PrincipalType `json:"type"`
	ID          string                 `json:"id"`
	DisplayName string                 `json:"displayName,omitempty"`
}

type directoryView struct {
	Source    string          `json:"source"`
	Truncated bool            `json:"truncated"`
	Items     []directoryItem `json:"items"`
}

type groupsView struct {
	Source string   `json:"source"`
	Groups []string `json:"groups"`
}

// searchDirectory answers GET /v1/authorization/directory?type=&q=.
func (h *AuthorizationHandler) searchDirectory(w http.ResponseWriter, r *http.Request) {
	if !h.mayListPrincipals(w, r) {
		return
	}
	ctx := r.Context()
	t := resource.PrincipalType(r.URL.Query().Get("type"))
	if t != resource.PrincipalPerson && t != resource.PrincipalGroup {
		writeStoreErr(w, r, fmt.Errorf("%w: type: must be person or group", resource.ErrValidation))
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if len(q) > maxDirectoryQuery {
		writeStoreErr(w, r, fmt.Errorf("%w: q: at most %d bytes", resource.ErrValidation, maxDirectoryQuery))
		return
	}
	if h.directory != nil {
		search := h.directory.People
		if t == resource.PrincipalGroup {
			search = h.directory.Groups
		}
		res, err := search(ctx, q)
		if err == nil {
			view := directoryView{Source: sourceLive, Truncated: res.Truncated, Items: make([]directoryItem, 0, min(len(res.Items), maxDirectoryItems))}
			for _, e := range res.Items {
				if len(view.Items) == maxDirectoryItems {
					view.Truncated = true
					break
				}
				view.Items = append(view.Items, directoryItem{Type: t, ID: e.ID, DisplayName: e.Name})
			}
			writeJSON(w, http.StatusOK, view)
			return
		}
		if ctx.Err() != nil {
			return // the client has gone; nothing to answer
		}
		// The error may name provider internals: log it, answer from what
		// sign-ins recorded.
		log.Printf("api: directory search failed, answering from signed-in principals (request %s): %v", audit.RequestIDFrom(ctx), err)
	}
	known, err := h.principals.List(ctx, t)
	if err != nil {
		writeStoreErr(w, r, err)
		return
	}
	view := directoryView{Source: sourceSignedIn, Items: []directoryItem{}}
	lq := strings.ToLower(q)
	for _, p := range known {
		if lq != "" && !strings.Contains(strings.ToLower(p.ID), lq) && !strings.Contains(strings.ToLower(p.DisplayName), lq) {
			continue
		}
		if len(view.Items) == maxDirectoryItems {
			view.Truncated = true
			break
		}
		view.Items = append(view.Items, directoryItem{Type: p.Type, ID: p.ID, DisplayName: p.DisplayName})
	}
	writeJSON(w, http.StatusOK, view)
}

// personGroups answers GET /v1/authorization/principals/person/{id}/groups:
// the groups a person belongs to now, for checking their access.
func (h *AuthorizationHandler) personGroups(w http.ResponseWriter, r *http.Request) {
	if !h.mayListPrincipals(w, r) {
		return
	}
	ctx := r.Context()
	id := r.PathValue("id")
	if h.directory != nil {
		groups, err := h.directory.GroupsOf(ctx, id)
		switch {
		case err == nil:
			if groups == nil {
				groups = []string{}
			}
			slices.Sort(groups)
			writeJSON(w, http.StatusOK, groupsView{Source: sourceLive, Groups: groups})
			return
		case errors.Is(err, identity.ErrUnknownSubject):
			WriteProblem(w, http.StatusNotFound, "not found", "the identity provider has no such person", audit.RequestIDFrom(ctx))
			return
		case ctx.Err() != nil:
			return // the client has gone; nothing to answer
		}
		log.Printf("api: directory groups lookup failed, answering from the last sign-in (request %s): %v", audit.RequestIDFrom(ctx), err)
	}
	known, err := h.principals.List(ctx, resource.PrincipalPerson)
	if err != nil {
		writeStoreErr(w, r, err)
		return
	}
	i := slices.IndexFunc(known, func(p sqlite.KnownPrincipal) bool { return p.ID == id })
	if i < 0 {
		WriteProblem(w, http.StatusNotFound, "not found", "no such person has signed in, and the identity provider can't be asked", audit.RequestIDFrom(ctx))
		return
	}
	groups := slices.Clone(known[i].Groups)
	if groups == nil {
		groups = []string{}
	}
	slices.Sort(groups)
	writeJSON(w, http.StatusOK, groupsView{Source: sourceSignedIn, Groups: groups})
}
