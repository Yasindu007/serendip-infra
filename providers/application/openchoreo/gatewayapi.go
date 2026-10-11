package openchoreo

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
	"time"

	"github.com/serendip-infra/serendip-infra/providers/kubernetes"
	"sigs.k8s.io/yaml"
)

// Gateway API CRDs (standard channel, CRDs only -- no gateway controller).
// OpenChoreo's RenderedRelease controller lists live data-plane resources
// across a fixed set of kinds that includes Gateway and HTTPRoute, and
// fails every reconcile with "not found" when those CRDs are absent --
// even with gateway.enabled=false on both charts. Without them no
// release ever reports status, so nothing becomes Ready. Found during
// spec 011's real-cluster verification; the charts require Gateway API
// >= v1.2.
const (
	gatewayAPIVersion     = "v1.3.0"
	gatewayAPIManifestURL = "https://github.com/kubernetes-sigs/gateway-api/releases/download/" + gatewayAPIVersion + "/standard-install.yaml"
	gatewayAPIManifestSHA = "78796d5c51450fc55d8dc8092ba8137f8c807982d7508d7875d5c537a24082b9"

	crdCollectionPath = "/apis/apiextensions.k8s.io/v1/customresourcedefinitions"
	maxManifestBytes  = 4 << 20
)

// gatewayAPICRDNames are the CRDs gatewayAPIManifestURL contains. If all
// already exist the download is skipped, so restarts don't need network
// access to GitHub.
var gatewayAPICRDNames = []string{
	"gatewayclasses.gateway.networking.k8s.io",
	"gateways.gateway.networking.k8s.io",
	"grpcroutes.gateway.networking.k8s.io",
	"httproutes.gateway.networking.k8s.io",
	"referencegrants.gateway.networking.k8s.io",
}

// EnsureGatewayAPICRDs downloads the pinned Gateway API manifest, verifies
// its checksum, and creates any of its CRDs that don't already exist.
// Existing CRDs are left untouched (never upgraded or downgraded), so a
// cluster that already runs a gateway controller keeps its own version.
func EnsureGatewayAPICRDs(ctx context.Context, client *kubernetes.Client) error {
	present, err := allCRDsPresent(ctx, client, gatewayAPICRDNames)
	if err != nil {
		return fmt.Errorf("openchoreo: gateway api crds: %w", err)
	}
	if present {
		return nil
	}
	manifest, err := fetchManifest(ctx, &http.Client{Timeout: 2 * time.Minute}, gatewayAPIManifestURL, gatewayAPIManifestSHA)
	if err != nil {
		return fmt.Errorf("openchoreo: gateway api crds: %w", err)
	}
	if err := ensureCRDsFromManifest(ctx, client, manifest); err != nil {
		return fmt.Errorf("openchoreo: gateway api crds: %w", err)
	}
	return nil
}

func allCRDsPresent(ctx context.Context, client *kubernetes.Client, names []string) (bool, error) {
	for _, name := range names {
		_, err := client.Get(ctx, crdCollectionPath+"/"+name)
		if errors.Is(err, kubernetes.ErrNotFound) {
			return false, nil
		}
		if err != nil {
			return false, fmt.Errorf("get CRD %s: %w", name, err)
		}
	}
	return true, nil
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

func ensureCRDsFromManifest(ctx context.Context, client *kubernetes.Client, manifest []byte) error {
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
		if obj.Kind != "CustomResourceDefinition" {
			return fmt.Errorf("unexpected %s %q in CRD manifest", obj.Kind, obj.Metadata.Name)
		}
		// Status in a manifest is ignored by the API server on create.
		obj.Status = nil

		_, err = client.Get(ctx, crdCollectionPath+"/"+obj.Metadata.Name)
		if err == nil {
			continue
		}
		if !errors.Is(err, kubernetes.ErrNotFound) {
			return fmt.Errorf("get CRD %s: %w", obj.Metadata.Name, err)
		}
		if _, err := client.Create(ctx, crdCollectionPath, obj); err != nil {
			return fmt.Errorf("create CRD %s: %w", obj.Metadata.Name, err)
		}
	}
	return nil
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
