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

// Names for Environments, pipelines, deployments and releases (spec 020),
// derived from Serendip IDs like the others. OpenChoreo shortens the
// data-plane namespace it derives from project and environment names
// itself (T001 spike), so these only need to be valid object names.

// environmentObjectName is the OpenChoreo Environment for a Serendip
// Environment.
func environmentObjectName(envID string) string { return "env-" + shortHash(envID) }

// pipelineName is the DeploymentPipeline for a Serendip Project.
func pipelineName(projectID string) string { return "pl-" + shortHash(projectID) }

// projectEnvBindingName is the ProjectReleaseBinding giving an
// Application's project its data-plane namespace in one Environment.
func projectEnvBindingName(project, envID string) string {
	return project + "-" + environmentObjectName(envID)
}

// deploymentBindingName is the ReleaseBinding running a Component in one
// Environment: a Serendip Deployment.
func deploymentBindingName(compID, envID string) string {
	return componentName(compID) + "-" + environmentObjectName(envID)
}

// releaseObjectName names a release after its content, so creating the
// same release twice is a no-op.
func releaseObjectName(compID string, content []byte) string {
	return componentName(compID) + "-" + shortHash(string(content))
}
