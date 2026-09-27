// Package bootstrap drives a real, single-node K3s server install/
// uninstall on a Linux host -- the one real host-mutating capability in
// agent/, gated behind an explicit confirmation flag at the CLI layer
// (cmd/serendip-agent). See specs/005-k3s-bootstrap/plan.md for the
// design.
package bootstrap
