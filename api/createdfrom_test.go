package api_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/serendip-infra/serendip-infra/core/resource"
	"github.com/serendip-infra/serendip-infra/core/store/sqlite"
)

// origin is a template provenance as the server would set it (spec 027).
func origin() *resource.TemplateOrigin {
	return &resource.TemplateOrigin{
		Template: "0190d1c0-0027-7000-8000-000000000001", TemplateName: "web-server",
		Version: "1.0", VersionID: "0190d1c0-0027-7000-8000-000000000101",
		By: "person:owner", At: time.Date(2026, 11, 28, 12, 0, 0, 0, time.UTC),
		Parameters: map[string]any{"port": float64(80)},
	}
}

// createdFrom is the server's (spec 027 plan decision 9): refused on a
// client's create, kept when an update leaves it out or repeats it, and
// refused when an update sets or changes it, on Applications and
// Components alike.
func TestCreatedFromIsTheServers(t *testing.T) {
	d := newDeployWorld(t)
	ctx := t.Context()

	// Refused on create.
	got := d.do("tok-owner", http.MethodPost, "/v1/applications", map[string]any{
		"metadata": map[string]any{"name": "forged", "ownerRefs": []any{map[string]any{"kind": "Project", "id": d.proj}}},
		"spec":     map[string]any{"createdFrom": origin()}})
	if got.Code != http.StatusBadRequest || !strings.Contains(got.Body.String(), "createdFrom") {
		t.Errorf("an Application created with createdFrom: %d %s", got.Code, got.Body)
	}
	if r := d.component(t, "tok-devops", d.app, "forged", map[string]any{"image": "x:1", "createdFrom": origin()}); r.StatusCode != http.StatusBadRequest {
		t.Errorf("a Component created with createdFrom: %d", r.StatusCode)
	}

	apps := sqlite.NewStore[resource.ApplicationSpec, resource.ApplicationStatus](d.db, resource.KindApplication)
	comps := sqlite.NewStore[resource.ComponentSpec, resource.ComponentStatus](d.db, resource.KindComponent)
	app, err := apps.Create(ctx, resource.Application{Metadata: resource.Metadata{Name: "made",
		OwnerRefs: []resource.OwnerRef{{Kind: resource.KindProject, ID: d.proj}}}, Spec: resource.ApplicationSpec{CreatedFrom: origin()}})
	if err != nil {
		t.Fatal(err)
	}
	comp, err := comps.Create(ctx, resource.Component{Metadata: resource.Metadata{Name: "made",
		OwnerRefs: []resource.OwnerRef{{Kind: resource.KindApplication, ID: app.Metadata.ID}}},
		Spec: resource.ComponentSpec{Image: "web:1", CreatedFrom: origin()}})
	if err != nil {
		t.Fatal(err)
	}

	type kind struct {
		name, path, token string
		get               func() (any, func(*resource.TemplateOrigin))
	}
	kinds := []kind{
		{"Application", "/v1/applications/" + app.Metadata.ID, "tok-owner", func() (any, func(*resource.TemplateOrigin)) {
			a, err := apps.Get(ctx, app.Metadata.ID)
			if err != nil {
				t.Fatal(err)
			}
			return &a, func(o *resource.TemplateOrigin) { a.Spec.CreatedFrom = o }
		}},
		{"Component", "/v1/components/" + comp.Metadata.ID, "tok-devops", func() (any, func(*resource.TemplateOrigin)) {
			c, err := comps.Get(ctx, comp.Metadata.ID)
			if err != nil {
				t.Fatal(err)
			}
			return &c, func(o *resource.TemplateOrigin) { c.Spec.CreatedFrom = o }
		}},
	}
	for _, k := range kinds {
		t.Run(k.name, func(t *testing.T) {
			// Left out: kept.
			body, set := k.get()
			set(nil)
			got := d.do(k.token, http.MethodPut, k.path, body)
			if got.Code != http.StatusOK {
				t.Fatalf("an update leaving createdFrom out: %d %s", got.Code, got.Body)
			}
			var after struct {
				Spec struct {
					CreatedFrom *resource.TemplateOrigin `json:"createdFrom"`
				} `json:"spec"`
			}
			if err := json.Unmarshal(got.Body.Bytes(), &after); err != nil || after.Spec.CreatedFrom == nil || after.Spec.CreatedFrom.TemplateName != "web-server" {
				t.Errorf("createdFrom after leaving it out = %+v, %v", after.Spec.CreatedFrom, err)
			}

			// Repeated as stored: fine.
			body, _ = k.get()
			if got := d.do(k.token, http.MethodPut, k.path, body); got.Code != http.StatusOK {
				t.Errorf("an update repeating createdFrom: %d %s", got.Code, got.Body)
			}

			// Changed: refused, and the stored one stays.
			body, set = k.get()
			changed := origin()
			changed.Version = "2.0"
			set(changed)
			if got := d.do(k.token, http.MethodPut, k.path, body); got.Code != http.StatusBadRequest || !strings.Contains(got.Body.String(), "createdFrom") {
				t.Errorf("an update changing createdFrom: %d %s", got.Code, got.Body)
			}
			body, set = k.get()
			changed = origin()
			changed.Parameters["port"] = float64(81)
			set(changed)
			if got := d.do(k.token, http.MethodPut, k.path, body); got.Code != http.StatusBadRequest {
				t.Errorf("an update changing createdFrom's parameters: %d %s", got.Code, got.Body)
			}
		})
	}

	// Set on a resource made by hand: refused.
	plain, err := apps.Get(ctx, d.app)
	if err != nil {
		t.Fatal(err)
	}
	plain.Spec.CreatedFrom = origin()
	if got := d.do("tok-owner", http.MethodPut, "/v1/applications/"+d.app, plain); got.Code != http.StatusBadRequest {
		t.Errorf("createdFrom added on update: %d %s", got.Code, got.Body)
	}
	web, err := comps.Get(ctx, d.web)
	if err != nil {
		t.Fatal(err)
	}
	web.Spec.CreatedFrom = origin()
	if got := d.do("tok-devops", http.MethodPut, "/v1/components/"+d.web, web); got.Code != http.StatusBadRequest {
		t.Errorf("createdFrom added to a Component on update: %d %s", got.Code, got.Body)
	}
}

// A Component's image is checked at write (RP-109), on create and update.
func TestComponentImageCheckedAtWrite(t *testing.T) {
	d := newDeployWorld(t)
	for _, image := range []string{"nginx latest", `nginx"`, "-flag", strings.Repeat("a", 513)} {
		if r := d.component(t, "tok-devops", d.app, "bad", map[string]any{"image": image}); r.StatusCode != http.StatusBadRequest {
			t.Errorf("image %.20q: %d, want 400", image, r.StatusCode)
		}
	}
	web, err := sqlite.NewStore[resource.ComponentSpec, resource.ComponentStatus](d.db, resource.KindComponent).Get(t.Context(), d.web)
	if err != nil {
		t.Fatal(err)
	}
	web.Spec.Image = "nginx:1.27 --privileged"
	if got := d.do("tok-devops", http.MethodPut, "/v1/components/"+d.web, web); got.Code != http.StatusBadRequest || !strings.Contains(got.Body.String(), "image") {
		t.Errorf("a bad image on update: %d %s", got.Code, got.Body)
	}
	web.Spec.Image = "registry.local:5000/team/web@sha256:" + strings.Repeat("a", 64)
	if got := d.do("tok-devops", http.MethodPut, "/v1/components/"+d.web, web); got.Code != http.StatusOK {
		t.Errorf("a digest image on update: %d %s", got.Code, got.Body)
	}
}
