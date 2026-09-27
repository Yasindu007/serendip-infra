package bootstrap

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"strings"
)

// CheckLinux rejects any non-Linux host up front -- same reasoning as
// agent/discover.Discover: agent/CLAUDE.md's target is "a bare
// Debian/Ubuntu host," and letting individual steps fail haphazardly on
// other OSes is worse than one clear rejection.
func CheckLinux() error {
	if runtime.GOOS != "linux" {
		return fmt.Errorf("bootstrap: unsupported OS %q, only linux is supported", runtime.GOOS)
	}
	return nil
}

// CheckRoot rejects a non-root invocation -- k3s's install script
// requires root, and failing fast with a clear message beats letting
// the install script itself fail confusingly partway through.
func CheckRoot() error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("bootstrap: must run as root (or via sudo) to install k3s")
	}
	return nil
}

// parseSystemdStatus interprets `systemctl is-system-running`'s output
// and the error (if any) from running it. "running" and "degraded" are
// both acceptable -- degraded means some unrelated unit failed, not that
// systemd itself is unusable. A *exec.Error (the systemctl binary
// doesn't exist at all) means this host doesn't use systemd, which
// k3s's install requires -- a hard failure, not a warning.
func parseSystemdStatus(output []byte, cmdErr error) (ok bool, warning string, err error) {
	var execErr *exec.Error
	if errors.As(cmdErr, &execErr) {
		return false, "", fmt.Errorf("bootstrap: systemctl not found -- this host does not appear to use systemd, which k3s's install requires: %w", cmdErr)
	}

	status := strings.TrimSpace(string(output))
	switch status {
	case "running":
		return true, "", nil
	case "degraded":
		return true, "systemd reports \"degraded\" (some unrelated unit failed) -- k3s may still install and run fine, but worth investigating separately", nil
	default:
		if cmdErr != nil {
			return false, "", fmt.Errorf("bootstrap: systemctl is-system-running failed unexpectedly (output %q): %w", status, cmdErr)
		}
		return false, "", fmt.Errorf("bootstrap: systemd reports status %q, not ready for a k3s install", status)
	}
}

// CheckSystemd is the thin real-exec wrapper around parseSystemdStatus.
func CheckSystemd() (warning string, err error) {
	out, cmdErr := exec.Command("systemctl", "is-system-running").Output()
	ok, warning, err := parseSystemdStatus(out, cmdErr)
	if !ok {
		return "", err
	}
	return warning, nil
}

var iptablesVersionRE = regexp.MustCompile(`iptables\s+v([\d.]+)\s+\((\w+)\)`)

// parseIPTablesVersion extracts the version and mode ("nf_tables" or
// "legacy") from `iptables --version`'s output, e.g.
// "iptables v1.8.9 (nf_tables)".
func parseIPTablesVersion(output []byte) (version, mode string, err error) {
	m := iptablesVersionRE.FindSubmatch(output)
	if m == nil {
		return "", "", fmt.Errorf("bootstrap: unrecognized iptables --version output: %q", string(output))
	}
	return string(m[1]), string(m[2]), nil
}

// isRiskyIPTablesVersion reports whether version falls in the
// documented 1.8.0-1.8.4 range where nf_tables mode causes accumulating/
// duplicate rule bugs with k3s.
func isRiskyIPTablesVersion(version string) bool {
	parts := strings.SplitN(version, ".", 3)
	if len(parts) < 3 {
		return false
	}
	major, err1 := strconv.Atoi(parts[0])
	minor, err2 := strconv.Atoi(parts[1])
	patch, err3 := strconv.Atoi(parts[2])
	if err1 != nil || err2 != nil || err3 != nil {
		return false
	}
	return major == 1 && minor == 8 && patch <= 4
}

// CheckIPTablesMode is best-effort: an unreadable or unparseable
// iptables --version is not itself an error (nothing to detect, so
// nothing to warn about) -- only a confirmed risky nf_tables version
// produces a warning. Never a hard failure (spec.md: "detect and warn,
// full remediation out of scope").
func CheckIPTablesMode() (warning string, err error) {
	out, cmdErr := exec.Command("iptables", "--version").Output()
	if cmdErr != nil {
		return "", nil
	}
	version, mode, parseErr := parseIPTablesVersion(out)
	if parseErr != nil {
		return "", nil
	}
	if mode == "nf_tables" && isRiskyIPTablesVersion(version) {
		return fmt.Sprintf("iptables %s in nf_tables mode is a known k3s issue (accumulating/duplicate rules) -- "+
			"consider switching to legacy mode via update-alternatives, or k3s's --prefer-bundled-bin flag", version), nil
	}
	return "", nil
}
