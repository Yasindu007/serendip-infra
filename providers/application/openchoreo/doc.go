// Package openchoreo implements the Application Platform capability on
// OpenChoreo (ADR 0005). It installs OpenChoreo by shelling out to the real
// `helm` CLI (ADR 0010, via providers/internal/helm), and realises Serendip
// Applications/Components as OpenChoreo custom resources through the
// generic providers/kubernetes.Client.
package openchoreo
