package api

import (
	"net/http"

	"github.com/serendip-infra/serendip-infra/core/audit"
	"github.com/serendip-infra/serendip-infra/core/graph"
	"github.com/serendip-infra/serendip-infra/core/resource"
)

// GraphHandler exposes read-only graph traversal (core/graph.Graph) over
// HTTP -- the first real consumer of graph.SQLiteGraph, which spec 001
// built and tested directly but never had an actual caller for outside
// core/store/sqlite's own transactions (core/store/sqlite calls the
// package-level graph functions against its own *sql.Tx instead).
//
// These endpoints don't verify the referenced resource actually exists --
// core/graph only ever queries the edges table, which has no way to know
// whether a (kind, id) pair with zero edges is "doesn't exist" or "exists
// but has no relationships." An empty result array covers both cases
// honestly rather than fabricating an existence check core/graph was
// never designed to make.
type GraphHandler struct {
	graph graph.Graph
}

func NewGraphHandler(g graph.Graph) *GraphHandler {
	return &GraphHandler{graph: g}
}

// Register wires GET /v1/graph/{kind}/{id}/children, .../dependents,
// .../blast-radius.
func (h *GraphHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/graph/{kind}/{id}/children", h.children)
	mux.HandleFunc("GET /v1/graph/{kind}/{id}/dependents", h.dependents)
	mux.HandleFunc("GET /v1/graph/{kind}/{id}/blast-radius", h.blastRadius)
}

func (h *GraphHandler) children(w http.ResponseWriter, r *http.Request) {
	result, err := h.graph.Children(r.Context(), refFromPath(r))
	h.respond(w, r, result, err)
}

func (h *GraphHandler) dependents(w http.ResponseWriter, r *http.Request) {
	result, err := h.graph.Dependents(r.Context(), refFromPath(r))
	h.respond(w, r, result, err)
}

func (h *GraphHandler) blastRadius(w http.ResponseWriter, r *http.Request) {
	result, err := h.graph.BlastRadius(r.Context(), refFromPath(r))
	h.respond(w, r, result, err)
}

func refFromPath(r *http.Request) graph.ResourceRef {
	return graph.ResourceRef{Kind: resource.Kind(r.PathValue("kind")), ID: r.PathValue("id")}
}

func (h *GraphHandler) respond(w http.ResponseWriter, r *http.Request, result []graph.ResourceRef, err error) {
	if err != nil {
		status, title := MapStoreError(err)
		WriteProblem(w, status, title, err.Error(), audit.RequestIDFrom(r.Context()))
		return
	}
	writeJSON(w, http.StatusOK, result)
}
