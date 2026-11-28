package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/serendip-infra/serendip-infra/core/audit"
	"github.com/serendip-infra/serendip-infra/core/resource"
	"github.com/serendip-infra/serendip-infra/core/store"
	"github.com/serendip-infra/serendip-infra/core/template"
)

// TemplateHandler serves Templates and their versions
// (specs/027-templates-and-scaffolder plan decisions 1 and 10), mirroring
// Azure's templateSpecs and templateSpecs/versions:
//
//	POST   /v1/templates                            create (generic)
//	GET    /v1/templates                            list, the built-ins first
//	GET    /v1/templates/{id}                       get, a built-in too
//	PUT    /v1/templates/{id}                       update (generic; 400 on a built-in)
//	DELETE /v1/templates/{id}                       delete (?cascade=true takes its versions)
//	GET    /v1/templates/{id}/versions              list, oldest first, the latest marked
//	GET    /v1/templates/{id}/versions/{version}    get by label
//	POST   /v1/templates/{id}/versions              publish
//	DELETE /v1/templates/{id}/versions/{version}    delete
//
// There's no version update: a published version never changes, and a
// PUT answers 404, as for any method a path isn't served with.
type TemplateHandler struct {
	templates store.Store[resource.TemplateSpec, resource.TemplateStatus]
	versions  store.Store[resource.TemplateVersionSpec, resource.TemplateVersionStatus]
	generic   *ResourceHandler[resource.TemplateSpec, resource.TemplateStatus]
	now       func() time.Time
}

// NewTemplateHandler serves templates from templates and versions. The
// versions store must key versions by resource.TemplateVersionKey, so a
// label is unique within its Template.
func NewTemplateHandler(templates store.Store[resource.TemplateSpec, resource.TemplateStatus],
	versions store.Store[resource.TemplateVersionSpec, resource.TemplateVersionStatus]) *TemplateHandler {
	h := &TemplateHandler{templates: templates, versions: versions, now: time.Now}
	h.generic = NewResourceHandler(templates, resourcePaths[resource.KindTemplate]).
		WithServerStatus().
		// The server chooses a new Template's ID, as for a version.
		WithServerFields(func(t, current *resource.Template) error {
			if current == nil {
				t.Metadata.ID = ""
			}
			return nil
		}).
		WithMetadataCheck(refuseBuiltInID).
		WithPrepare(func(t *resource.Template) error {
			if err := resource.ValidateTemplate(t.Metadata, t.Spec); err != nil {
				return err
			}
			// Nothing reconciles a Template: it's ready once stored. On
			// update the stored status is kept (WithServerStatus).
			t.Status.Status = readyStatus("Saved", "the template is stored", h.now())
			return nil
		})
	return h
}

func (h *TemplateHandler) Register(mux Mux) {
	p := "/v1/" + resourcePaths[resource.KindTemplate]
	mux.HandleFunc("POST "+p, h.generic.Create)
	mux.HandleFunc("GET "+p, h.List)
	mux.HandleFunc("GET "+p+"/{id}", h.Get)
	mux.HandleFunc("PUT "+p+"/{id}", h.generic.Update)
	mux.HandleFunc("DELETE "+p+"/{id}", h.Delete)
	mux.HandleFunc("GET "+p+"/{id}/versions", h.ListVersions)
	mux.HandleFunc("GET "+p+"/{id}/versions/{version}", h.GetVersion)
	mux.HandleFunc("POST "+p+"/{id}/versions", h.Publish)
	mux.HandleFunc("DELETE "+p+"/{id}/versions/{version}", h.DeleteVersion)
}

// errBuiltIn refuses a change to a built-in template.
var errBuiltIn = fmt.Errorf("%w: a built-in template can't be changed; create your own from it instead", resource.ErrValidation)

// refuseBuiltInID keeps the built-ins' fixed IDs to the built-ins: a
// stored Template can't take one, and a PUT can't target one.
func refuseBuiltInID(m resource.Metadata) error {
	if template.IsBuiltIn(m.ID) {
		return errBuiltIn
	}
	return nil
}

// readyStatus is the Ready condition of a resource nothing reconciles.
func readyStatus(reason, message string, now time.Time) resource.Status {
	return resource.Status{Conditions: []resource.Condition{{
		Type: "Ready", Status: resource.ConditionTrue, Reason: reason, Message: message, LastTransitionTime: now.UTC(),
	}}}
}

// templateView is a Template as the API shows it.
type templateView struct {
	resource.Template
	BuiltIn bool `json:"builtIn,omitempty"`
}

// versionView is a version as the API shows it. Latest marks the one
// created most recently (plan decision 1, RP-104).
type versionView struct {
	resource.TemplateVersion
	BuiltIn bool `json:"builtIn,omitempty"`
	Latest  bool `json:"latest,omitempty"`
}

func builtInTemplateView(b template.BuiltIn) templateView {
	b.Template.Status.Status = readyStatus("BuiltIn", "a starter template that ships with Serendip", time.Time{})
	return templateView{Template: b.Template, BuiltIn: true}
}

// List answers every built-in (anyone signed in may read them, plan
// decision 5) and the stored Templates the caller may read. A filter by
// owner or label leaves the built-ins out: they have neither.
func (h *TemplateHandler) List(w http.ResponseWriter, r *http.Request) {
	opts := listOptions(r)
	list, err := h.templates.List(r.Context(), opts)
	if err != nil {
		writeStoreErr(w, r, err)
		return
	}
	if list, err = filterReadable(r.Context(), list); err != nil {
		WriteProblem(w, http.StatusInternalServerError, "authorization check failed", "the list couldn't be filtered; retry later", audit.RequestIDFrom(r.Context()))
		return
	}
	out := make([]templateView, 0, len(list)+3)
	if opts.OwnerID == "" && len(opts.Labels) == 0 {
		for _, b := range template.BuiltIns() {
			out = append(out, builtInTemplateView(b))
		}
	}
	for _, t := range list {
		out = append(out, templateView{Template: t})
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *TemplateHandler) Get(w http.ResponseWriter, r *http.Request) {
	if b, ok := template.LookupBuiltIn(r.PathValue("id")); ok {
		writeJSON(w, http.StatusOK, builtInTemplateView(b))
		return
	}
	t, err := h.templates.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		writeStoreErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, templateView{Template: t})
}

func (h *TemplateHandler) Delete(w http.ResponseWriter, r *http.Request) {
	if template.IsBuiltIn(r.PathValue("id")) {
		writeStoreErr(w, r, errBuiltIn)
		return
	}
	h.generic.Delete(w, r)
}

// templateVersions returns a template's versions, oldest first: a
// built-in's, or a stored Template's (404 when it doesn't exist).
func (h *TemplateHandler) templateVersions(ctx context.Context, id string) ([]resource.TemplateVersion, bool, error) {
	if b, ok := template.LookupBuiltIn(id); ok {
		return b.Versions, true, nil
	}
	if _, err := h.templates.Get(ctx, id); err != nil {
		return nil, false, err
	}
	list, err := h.versions.List(ctx, store.ListOptions{OwnerID: id})
	return list, false, err
}

// latestIndex is the index of the version created most recently, or -1.
// Labels aren't compared: "latest" is the newest, not the highest
// (RP-104). Equal times go to the later one listed (the store lists in
// creation order).
func latestIndex(vs []resource.TemplateVersion) int {
	best := -1
	for i, v := range vs {
		if best < 0 || !v.Metadata.CreatedAt.Before(vs[best].Metadata.CreatedAt) {
			best = i
		}
	}
	return best
}

// LatestVersion is a template's latest version (plan decision 1).
func LatestVersion(vs []resource.TemplateVersion) (resource.TemplateVersion, bool) {
	i := latestIndex(vs)
	if i < 0 {
		return resource.TemplateVersion{}, false
	}
	return vs[i], true
}

func versionViews(vs []resource.TemplateVersion, builtIn bool) []versionView {
	latest := latestIndex(vs)
	out := make([]versionView, len(vs))
	for i, v := range vs {
		if builtIn {
			v.Status.Status = readyStatus("BuiltIn", "a starter template that ships with Serendip", time.Time{})
		}
		out[i] = versionView{TemplateVersion: v, BuiltIn: builtIn, Latest: i == latest}
	}
	return out
}

func (h *TemplateHandler) ListVersions(w http.ResponseWriter, r *http.Request) {
	vs, builtIn, err := h.templateVersions(r.Context(), r.PathValue("id"))
	if err != nil {
		writeStoreErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, versionViews(vs, builtIn))
}

func (h *TemplateHandler) GetVersion(w http.ResponseWriter, r *http.Request) {
	vs, builtIn, err := h.templateVersions(r.Context(), r.PathValue("id"))
	if err != nil {
		writeStoreErr(w, r, err)
		return
	}
	label := r.PathValue("version")
	for _, v := range versionViews(vs, builtIn) {
		if v.Metadata.Name == label {
			writeJSON(w, http.StatusOK, v)
			return
		}
	}
	writeStoreErr(w, r, fmt.Errorf("%w: template %s has no version %q", store.ErrNotFound, r.PathValue("id"), label))
}

// Publish stores a new version, checked by template.ValidatePublish. The
// path names the Template: the body's owner, template and version fields
// are the server's, and so is its ID.
func (h *TemplateHandler) Publish(w http.ResponseWriter, r *http.Request) {
	tid := r.PathValue("id")
	if template.IsBuiltIn(tid) {
		writeStoreErr(w, r, errBuiltIn)
		return
	}
	if _, err := h.templates.Get(r.Context(), tid); err != nil {
		writeStoreErr(w, r, err)
		return
	}
	var v resource.TemplateVersion
	if !decodeResourceBody(w, r, &v) {
		return
	}
	v.Metadata.ID = ""
	v.Metadata.OwnerRefs = []resource.OwnerRef{{Kind: resource.KindTemplate, ID: tid}}
	v.Spec.Template, v.Spec.Version = tid, v.Metadata.Name
	if err := template.ValidatePublish(v.Metadata, v.Spec); err != nil {
		writeStoreErr(w, r, err)
		return
	}
	v.Status = resource.TemplateVersionStatus{Status: readyStatus("Published", "the version is published and can't change", h.now())}
	created, err := h.versions.Create(r.Context(), v)
	if errors.Is(err, store.ErrDuplicate) {
		WriteProblem(w, http.StatusConflict, "version exists",
			fmt.Sprintf("template %s already has a version %q; a published version can't change, so publish a new label", tid, v.Metadata.Name),
			audit.RequestIDFrom(r.Context()))
		return
	}
	if err != nil {
		writeStoreErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, versionView{TemplateVersion: created})
}

// DeleteVersion deletes one version. The authorizer resolved the label to
// the version its Approval names (targetTemplateVersion); the store
// refuses a different one.
func (h *TemplateHandler) DeleteVersion(w http.ResponseWriter, r *http.Request) {
	tid, label := r.PathValue("id"), r.PathValue("version")
	if template.IsBuiltIn(tid) {
		writeStoreErr(w, r, errBuiltIn)
		return
	}
	id, ok, err := versionIDIn(h.versions)(r.Context(), tid, label)
	if err != nil {
		writeStoreErr(w, r, err)
		return
	}
	if !ok {
		writeStoreErr(w, r, fmt.Errorf("%w: template %s has no version %q", store.ErrNotFound, tid, label))
		return
	}
	if err := h.versions.Delete(r.Context(), id, false); err != nil {
		writeStoreErr(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// versionIDIn finds a stored version's ID by its Template and label.
func versionIDIn(versions store.Store[resource.TemplateVersionSpec, resource.TemplateVersionStatus]) func(ctx context.Context, templateID, label string) (string, bool, error) {
	return func(ctx context.Context, templateID, label string) (string, bool, error) {
		list, err := versions.List(ctx, store.ListOptions{OwnerID: templateID})
		if err != nil {
			return "", false, err
		}
		for _, v := range list {
			if v.Metadata.Name == label {
				return v.Metadata.ID, true, nil
			}
		}
		return "", false, nil
	}
}
