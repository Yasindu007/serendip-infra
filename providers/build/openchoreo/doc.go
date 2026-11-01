// Package openchoreo implements the build capability on OpenChoreo's
// workflow plane (specs/022-builds, ADR 0020). Its Installer is the
// openchoreo-workflow-plane package: it installs the workflow-plane chart
// by shelling out to the real `helm` CLI (ADR 0010, via
// providers/internal/helm), registers ClusterWorkflowPlane/default, and
// hand-builds a registry:2 and Serendip's own serendip-dockerfile
// ClusterWorkflow through the generic providers/kubernetes.Client.
//
// It shares nothing with providers/application/openchoreo, so either can
// be deleted without breaking the other: the few lines both need (the CA
// copy, the apply helper) are repeated here on purpose.
package openchoreo
