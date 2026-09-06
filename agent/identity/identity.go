package identity

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/serendip-infra/serendip-infra/core/id"
)

// fileName is the persisted identity file's name within stateDir --
// specs/004-bootstrap-agent/plan.md's resolved default full path is
// stateDir=/var/lib/serendip/agent, giving
// /var/lib/serendip/agent/node-id.
const fileName = "node-id"

// Load returns this host's persisted node identity, generating and
// persisting one via core/id.New() (UUIDv7, ADR 0007 -- reusing spec
// 001's existing ID scheme rather than inventing a new one) if none
// exists yet under stateDir. Reused, not regenerated, on every
// subsequent call against the same stateDir.
func Load(stateDir string) (string, error) {
	path := filepath.Join(stateDir, fileName)

	data, err := os.ReadFile(path)
	if err == nil {
		nodeID := strings.TrimSpace(string(data))
		if nodeID == "" {
			return "", fmt.Errorf("identity: %s exists but is empty", path)
		}
		return nodeID, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("identity: read %s: %w", path, err)
	}

	nodeID := id.New()
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return "", fmt.Errorf("identity: create state dir %s: %w", stateDir, err)
	}
	if err := os.WriteFile(path, []byte(nodeID), 0o600); err != nil {
		return "", fmt.Errorf("identity: write %s: %w", path, err)
	}
	return nodeID, nil
}
