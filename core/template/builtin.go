package template

import (
	"bytes"
	"encoding/json"

	"github.com/serendip-infra/serendip-infra/core/resource"
)

// Built-in template IDs (plan decision 10). They never change, so scripts
// can name them on every install, as built-in role IDs do.
const (
	WebServerID         = "0190d1c0-0027-7000-8000-000000000001"
	ServiceFromSourceID = "0190d1c0-0027-7000-8000-000000000002"
	WebAndAPIID         = "0190d1c0-0027-7000-8000-000000000003"

	webServerV1ID         = "0190d1c0-0027-7000-8000-000000000101"
	serviceFromSourceV1ID = "0190d1c0-0027-7000-8000-000000000102"
	webAndAPIV1ID         = "0190d1c0-0027-7000-8000-000000000103"
)

// starterImage is what the image starters default to: edge-1's benchmark
// image, already cached on a node that ran it (plan decision 11).
const starterImage = "nginx:1.27-alpine"

// BuiltIn is a starter template and its versions, oldest first.
type BuiltIn struct {
	Template resource.Template
	Versions []resource.TemplateVersion
}

type builtInDef struct {
	id, name, displayName, description string
	versions                           []builtInVersion
}

type builtInVersion struct {
	id, label, parameters, body string
}

// builtIns are the starters (plan decision 11). Each one's versions are
// checked by ValidatePublish in the tests, as a published one would be.
var builtIns = []builtInDef{
	{
		id: WebServerID, name: "web-server", displayName: "Web server",
		description: "One Component running an image, with one port exposed through the platform's gateway.",
		versions: []builtInVersion{{
			id: webServerV1ID, label: "1.0",
			parameters: `{
				"type": "object",
				"properties": {
					"image": {"type": "string", "title": "Image", "description": "The image to run.", "default": "` + starterImage + `", "maxLength": 512},
					"port": {"type": "integer", "title": "Port", "description": "The port the image listens on.", "default": 80, "minimum": 1, "maximum": 65535}
				}
			}`,
			body: `{"components": [
				{"name": "web", "image": {"$param": "image"},
				 "ports": [{"name": "http", "port": {"$param": "port"}, "expose": true}]}
			]}`,
		}},
	},
	{
		id: ServiceFromSourceID, name: "service-from-source", displayName: "Service from source",
		description: "One Component built from a Git repository's Dockerfile, with one port exposed. Needs the builds package.",
		versions: []builtInVersion{{
			id: serviceFromSourceV1ID, label: "1.0",
			parameters: `{
				"type": "object",
				"required": ["repository"],
				"properties": {
					"repository": {"type": "string", "title": "Repository", "description": "The Git repository's https:// or SSH URL.", "maxLength": 2048, "examples": ["https://github.com/example/app.git"]},
					"branch": {"type": "string", "title": "Branch", "default": "main", "maxLength": 255},
					"path": {"type": "string", "title": "Path", "description": "The build context within the repository.", "default": ".", "maxLength": 1024},
					"port": {"type": "integer", "title": "Port", "description": "The port the built image listens on.", "default": 8080, "minimum": 1, "maximum": 65535},
					"credentialSecret": {"type": "string", "title": "Credential Secret", "description": "A Secret holding the repository's username and password, for a private repository.", "x-serendip-ref": "Secret"}
				}
			}`,
			body: `{"requires": ["builds"], "components": [
				{"name": "service",
				 "source": {"repository": {"$param": "repository"}, "branch": {"$param": "branch"}, "path": {"$param": "path"}, "credentialSecret": {"$param": "credentialSecret"}},
				 "ports": [{"name": "http", "port": {"$param": "port"}, "expose": true}]}
			]}`,
		}},
	},
	{
		id: WebAndAPIID, name: "web-and-api", displayName: "Web front end and API",
		description: "Two Components: an API, and a web front end exposed through the gateway that calls it, with the API's address in API_URL.",
		versions: []builtInVersion{{
			id: webAndAPIV1ID, label: "1.0",
			parameters: `{
				"type": "object",
				"properties": {
					"webImage": {"type": "string", "title": "Web image", "default": "` + starterImage + `", "maxLength": 512},
					"apiImage": {"type": "string", "title": "API image", "default": "` + starterImage + `", "maxLength": 512},
					"apiPort": {"type": "integer", "title": "API port", "description": "The port the API image listens on.", "default": 80, "minimum": 1, "maximum": 65535}
				}
			}`,
			body: `{"components": [
				{"name": "api", "image": {"$param": "apiImage"},
				 "ports": [{"name": "http", "port": {"$param": "apiPort"}}]},
				{"name": "web", "image": {"$param": "webImage"},
				 "ports": [{"name": "http", "port": 80, "expose": true}],
				 "dependencies": [{"component": "api", "port": "http", "env": "API_URL"}]}
			]}`,
		}},
	},
}

// BuiltIns returns the starter templates, in gallery order. Each call
// returns fresh values, so a caller may change them.
func BuiltIns() []BuiltIn {
	out := make([]BuiltIn, 0, len(builtIns))
	for _, d := range builtIns {
		out = append(out, d.build())
	}
	return out
}

// LookupBuiltIn returns the starter template with id.
func LookupBuiltIn(id string) (BuiltIn, bool) {
	for _, d := range builtIns {
		if d.id == id {
			return d.build(), true
		}
	}
	return BuiltIn{}, false
}

// IsBuiltIn reports whether id is a starter template's ID.
func IsBuiltIn(id string) bool {
	for _, d := range builtIns {
		if d.id == id {
			return true
		}
	}
	return false
}

func (d builtInDef) build() BuiltIn {
	b := BuiltIn{Template: resource.Template{
		Metadata: resource.Metadata{ID: d.id, Name: d.name, Kind: resource.KindTemplate},
		Spec:     resource.TemplateSpec{DisplayName: d.displayName, Description: d.description},
	}}
	for _, v := range d.versions {
		var body resource.TemplateBody
		if err := json.Unmarshal([]byte(v.body), &body); err != nil {
			panic("template: built-in " + d.name + " " + v.label + ": " + err.Error())
		}
		params, err := compact(v.parameters)
		if err != nil {
			panic("template: built-in " + d.name + " " + v.label + ": " + err.Error())
		}
		b.Versions = append(b.Versions, resource.TemplateVersion{
			Metadata: resource.Metadata{
				ID: v.id, Name: v.label, Kind: resource.KindTemplateVersion,
				OwnerRefs: []resource.OwnerRef{{Kind: resource.KindTemplate, ID: d.id}},
			},
			Spec: resource.TemplateVersionSpec{Template: d.id, Version: v.label, Parameters: params, Body: body},
		})
	}
	return b
}

// compact keeps the text as written (so the duplicate-key check still sees
// it), without the indentation.
func compact(s string) (json.RawMessage, error) {
	var b bytes.Buffer
	if err := json.Compact(&b, []byte(s)); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}
