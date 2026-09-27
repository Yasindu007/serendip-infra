package bootstrap

// DefaultVersion is the K3s stable-channel release researched for this
// spec (2026-09-26) -- explicit pinning rather than relying on the
// installer's own unset-env "resolve against stable channel" behavior,
// for reproducibility (specs/005-k3s-bootstrap/spec.md).
const DefaultVersion = "v1.36.4+k3s1"

// installScriptURL is the official K3s install script -- this package
// never reimplements install logic (constitution rule 10: integrate,
// don't rebuild).
const installScriptURL = "https://get.k3s.io"

// InstallOptions configures Install.
type InstallOptions struct {
	// Version is set as INSTALL_K3S_VERSION. Defaults to DefaultVersion
	// if empty.
	Version string
}

// InstallEnv returns the exact env vars Install would set -- the single
// source of truth both `--dry-run` and the real Install path read from,
// so the printed dry-run summary can never drift from what actually
// runs.
func InstallEnv(opts InstallOptions) []string {
	version := opts.Version
	if version == "" {
		version = DefaultVersion
	}
	return []string{"INSTALL_K3S_VERSION=" + version}
}
