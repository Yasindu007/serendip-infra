// Package gatewayapi installs the pinned Gateway API standard-channel
// definitions (CRDs plus upstream's safe-upgrades admission policy). Both
// the gateway provider (ADR 0011) and the application platform provider
// (OpenChoreo's release controller lists Gateway API kinds even with its
// own gateway disabled) need them, so neither owns them. internal/ keeps
// it importable only from providers.
package gatewayapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/serendip-infra/serendip-infra/providers/kubernetes"
	"sigs.k8s.io/yaml"
)

// Version must stay within the range the pinned kgateway minor supports
// (ADR 0011: kgateway 2.4.x supports Gateway API 1.4-1.6). Bump both
// together.
const (
	Version     = "v1.6.1"
	manifestURL = "https://github.com/kubernetes-sigs/gateway-api/releases/download/" + Version + "/standard-install.yaml"
	manifestSHA = "24d931f22abd8e40c973264319ead7cfa09d0fb7716b7ab1ee2ff174cb063a73"

	bundleVersionAnnotation = "gateway.networking.k8s.io/bundle-version"
	maxManifestBytes        = 4 << 20
)

// collectionPaths maps every object kind the manifest may contain to its
// cluster-scoped REST collection. Anything else in the manifest is an
// error rather than silently skipped.
var collectionPaths = map[string]string{
	"CustomResourceDefinition":         "/apis/apiextensions.k8s.io/v1/customresourcedefinitions",
	"ValidatingAdmissionPolicy":        "/apis/admissionregistration.k8s.io/v1/validatingadmissionpolicies",
	"ValidatingAdmissionPolicyBinding": "/apis/admissionregistration.k8s.io/v1/validatingadmissionpolicybindings",
}

type objectRef struct{ kind, name string }

// pinnedObjects is everything the Version manifest contains. If all of
// them already exist at >= Version, the download is skipped, so restarts
// don't need network access to GitHub.
var pinnedObjects = []objectRef{
	{"CustomResourceDefinition", "backendtlspolicies.gateway.networking.k8s.io"},
	{"CustomResourceDefinition", "gatewayclasses.gateway.networking.k8s.io"},
	{"CustomResourceDefinition", "gateways.gateway.networking.k8s.io"},
	{"CustomResourceDefinition", "grpcroutes.gateway.networking.k8s.io"},
	{"CustomResourceDefinition", "httproutes.gateway.networking.k8s.io"},
	{"CustomResourceDefinition", "listenersets.gateway.networking.k8s.io"},
	{"CustomResourceDefinition", "referencegrants.gateway.networking.k8s.io"},
	{"CustomResourceDefinition", "tcproutes.gateway.networking.k8s.io"},
	{"CustomResourceDefinition", "tlsroutes.gateway.networking.k8s.io"},
	{"CustomResourceDefinition", "udproutes.gateway.networking.k8s.io"},
	{"ValidatingAdmissionPolicy", "safe-upgrades.gateway.networking.k8s.io"},
	{"ValidatingAdmissionPolicyBinding", "safe-upgrades.gateway.networking.k8s.io"},
}

// EnsureCRDs makes the cluster's Gateway API definitions at least Version:
// missing objects are created, objects whose bundle version is older are
// upgraded in place, and newer ones are left alone (never downgraded), so
// a cluster running its own newer gateway setup keeps it.
func EnsureCRDs(ctx context.Context, client *kubernetes.Client) error {
	hc := &http.Client{Timeout: 2 * time.Minute}
	return ensure(ctx, client, func(ctx context.Context) ([]byte, error) {
		return fetchManifest(ctx, hc, manifestURL, manifestSHA)
	})
}

func ensure(ctx context.Context, client *kubernetes.Client, fetch func(context.Context) ([]byte, error)) error {
	current, err := allAtLeastPinned(ctx, client)
	if err != nil {
		return fmt.Errorf("gatewayapi: %w", err)
	}
	if current {
		return nil
	}
	manifest, err := fetch(ctx)
	if err != nil {
		return fmt.Errorf("gatewayapi: %w", err)
	}
	if err := applyManifest(ctx, client, manifest); err != nil {
		return fmt.Errorf("gatewayapi: %w", err)
	}
	return nil
}

func allAtLeastPinned(ctx context.Context, client *kubernetes.Client) (bool, error) {
	for _, ref := range pinnedObjects {
		obj, err := client.Get(ctx, collectionPaths[ref.kind]+"/"+ref.name)
		if errors.Is(err, kubernetes.ErrNotFound) {
			return false, nil
		}
		if err != nil {
			return false, fmt.Errorf("get %s %s: %w", ref.kind, ref.name, err)
		}
		if compareVersions(obj.Metadata.Annotations[bundleVersionAnnotation], Version) < 0 {
			return false, nil
		}
	}
	return true, nil
}

func applyManifest(ctx context.Context, client *kubernetes.Client, manifest []byte) error {
	for _, doc := range splitYAMLDocuments(manifest) {
		raw, err := yaml.YAMLToJSON(doc)
		if err != nil {
			return fmt.Errorf("parse manifest document: %w", err)
		}
		var obj kubernetes.Object
		if err := json.Unmarshal(raw, &obj); err != nil {
			return fmt.Errorf("decode manifest document: %w", err)
		}
		if obj.Kind == "" {
			continue
		}
		collection, ok := collectionPaths[obj.Kind]
		if !ok {
			return fmt.Errorf("unexpected %s %q in Gateway API manifest", obj.Kind, obj.Metadata.Name)
		}
		// Status in a manifest is ignored by the API server.
		obj.Status = nil
		if err := applyObject(ctx, client, collection, obj); err != nil {
			return err
		}
	}
	return nil
}

func applyObject(ctx context.Context, client *kubernetes.Client, collection string, desired kubernetes.Object) error {
	path := collection + "/" + desired.Metadata.Name
	existing, err := client.Get(ctx, path)
	if errors.Is(err, kubernetes.ErrNotFound) {
		if _, err := client.Create(ctx, collection, desired); err != nil {
			return fmt.Errorf("create %s %s: %w", desired.Kind, desired.Metadata.Name, err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("get %s %s: %w", desired.Kind, desired.Metadata.Name, err)
	}
	have := existing.Metadata.Annotations[bundleVersionAnnotation]
	want := desired.Metadata.Annotations[bundleVersionAnnotation]
	if compareVersions(have, want) >= 0 {
		return nil
	}
	// Merge patch replaces arrays wholesale, and a CRD's schemas live in
	// the spec.versions array, so the upgraded definition fully replaces
	// the old one rather than merging stale schema keys into it.
	// Only keys the manifest actually sets: a null labels/annotations map in
	// a merge patch would delete whatever others put there.
	meta := map[string]any{}
	if len(desired.Metadata.Labels) > 0 {
		meta["labels"] = desired.Metadata.Labels
	}
	if len(desired.Metadata.Annotations) > 0 {
		meta["annotations"] = desired.Metadata.Annotations
	}
	patch := map[string]any{"metadata": meta, "spec": desired.Spec}
	if _, err := client.Patch(ctx, path, patch); err != nil {
		return fmt.Errorf("upgrade %s %s from %s to %s: %w", desired.Kind, desired.Metadata.Name, have, want, err)
	}
	return nil
}

// compareVersions compares "vMAJOR.MINOR.PATCH" bundle versions, ignoring
// any pre-release suffix (so a cluster on v1.6.1-rc1 counts as v1.6.1 and
// isn't upgraded). A missing or unparseable version sorts lowest, so an
// unlabeled object is treated as needing an upgrade.
func compareVersions(a, b string) int {
	pa, pb := parseVersion(a), parseVersion(b)
	for i := range pa {
		if pa[i] != pb[i] {
			if pa[i] < pb[i] {
				return -1
			}
			return 1
		}
	}
	return 0
}

func parseVersion(v string) [3]int {
	out := [3]int{-1, -1, -1}
	v = strings.TrimPrefix(v, "v")
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return out
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return [3]int{-1, -1, -1}
		}
		out[i] = n
	}
	return out
}

func fetchManifest(ctx context.Context, hc *http.Client, url, wantSHA string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download %s: unexpected status %d", url, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxManifestBytes+1))
	if err != nil {
		return nil, fmt.Errorf("download %s: %w", url, err)
	}
	if len(body) > maxManifestBytes {
		return nil, fmt.Errorf("download %s: larger than %d bytes", url, maxManifestBytes)
	}
	sum := sha256.Sum256(body)
	if got := hex.EncodeToString(sum[:]); got != wantSHA {
		return nil, fmt.Errorf("download %s: sha256 %s does not match pinned %s", url, got, wantSHA)
	}
	return body, nil
}

// splitYAMLDocuments splits a multi-document YAML stream on "---" separator
// lines (optionally followed by whitespace or a comment).
func splitYAMLDocuments(data []byte) [][]byte {
	var docs [][]byte
	var cur bytes.Buffer
	for _, line := range bytes.SplitAfter(data, []byte("\n")) {
		if isDocumentSeparator(line) {
			docs = append(docs, append([]byte(nil), cur.Bytes()...))
			cur.Reset()
			continue
		}
		cur.Write(line)
	}
	return append(docs, cur.Bytes())
}

func isDocumentSeparator(line []byte) bool {
	rest, ok := bytes.CutPrefix(bytes.TrimRight(line, " \t\r\n"), []byte("---"))
	if !ok {
		return false
	}
	return len(rest) == 0 || rest[0] == ' ' || rest[0] == '\t'
}
