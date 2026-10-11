package openchoreo

import (
	"crypto/sha256"
	"encoding/hex"
)

// Object names are derived from the Serendip resource ID alone, because
// a reconciler's delete branch only has the ID once the Serendip record
// is gone. A short hash rather than the raw UUID: OpenChoreo derives the
// data-plane cell namespace name from org namespace + project +
// environment, and a 36-char UUID would push that past Kubernetes'
// 63-char namespace-name limit.
const nameHashLen = 10

// environmentName is the single Environment every Project/Component is
// bound to (specs/011-application-platform-capability/plan.md decision 5).
const environmentName = "development"

func shortHash(id string) string {
	sum := sha256.Sum256([]byte(id))
	return hex.EncodeToString(sum[:])[:nameHashLen]
}

func projectName(appID string) string { return "app-" + shortHash(appID) }

func componentName(compID string) string { return "cmp-" + shortHash(compID) }

func projectReleaseBindingName(appID string) string {
	return projectName(appID) + "-" + environmentName
}

// releaseBindingName is the name OpenChoreo's Component controller gives
// the ReleaseBinding it creates under autoDeploy: {component}-{env}.
func releaseBindingName(compID string) string {
	return componentName(compID) + "-" + environmentName
}

// Labels identifying which Serendip resource an object realises. Purely
// for operators reading kubectl output; nothing reads them back.
const (
	labelSerendipID   = "serendip.dev/id"
	labelSerendipName = "serendip.dev/name"
)

func serendipLabels(id, name string) map[string]string {
	labels := map[string]string{labelSerendipID: id}
	if name != "" {
		labels[labelSerendipName] = name
	}
	return labels
}
