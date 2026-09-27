package bootstrap

import (
	"context"
	"fmt"
	"os"
	"os/exec"
)

// uninstallScriptPath is where k3s's install script writes its
// auto-generated uninstaller -- official, not something this package
// constructs itself.
const uninstallScriptPath = "/usr/local/bin/k3s-uninstall.sh"

// Uninstall runs the official uninstall script k3s's installer
// generates. Returns a clear error if the script doesn't exist rather
// than a confusing exec failure -- the most likely cause is k3s was
// never installed (or was installed as an agent joining another
// cluster, not a server, which generates a differently-named script).
func Uninstall(ctx context.Context) error {
	if _, err := os.Stat(uninstallScriptPath); err != nil {
		return fmt.Errorf("bootstrap: uninstall script not found at %s (was k3s ever installed as a server here?): %w", uninstallScriptPath, err)
	}
	cmd := exec.CommandContext(ctx, uninstallScriptPath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("bootstrap: run uninstall script: %w", err)
	}
	return nil
}
