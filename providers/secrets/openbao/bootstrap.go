package openbao

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// bootstrapClient is the subset of *Client Bootstrap needs -- a small
// interface (not *Client directly) so tests can exercise Bootstrap's
// orchestration logic against a fake, in-process implementation
// instead of a real httptest.Server; the wire-level behavior of each
// of these calls is already covered by client_test.go.
type bootstrapClient interface {
	Health(ctx context.Context) (HealthStatus, error)
	Init(ctx context.Context, shares, threshold int) (unsealKeys []string, rootToken string, err error)
	Unseal(ctx context.Context, key string) (sealed bool, err error)
	SetToken(token string)
}

var _ bootstrapClient = (*Client)(nil)

// credentials is the on-disk shape persisted at a Bootstrap call's
// credentialsPath -- see ADR 0009's documented custody simplification.
type credentials struct {
	RootToken string `json:"rootToken"`
	UnsealKey string `json:"unsealKey"`
}

// Bootstrap ensures the OpenBao instance behind client is initialized
// and unsealed, persisting (fresh instance) or reusing (restart
// against an already-initialized instance) root-token/unseal-key
// material at credentialsPath. Sets the resulting root token on
// client via SetToken before returning.
//
// Safe to call on every serendipd startup -- OpenBao always starts
// sealed after a process/pod restart unless auto-unseal is configured
// (which this spec explicitly doesn't use, ADR 0009), so re-sealed-
// after-restart is the normal, expected case here, not an error path.
//
// If the instance reports already-initialized but credentialsPath
// doesn't exist, this is a hard error, not a fallback -- there is no
// safe recovery path for "OpenBao says initialized, but this host has
// no record of the credentials that produced that state."
func Bootstrap(ctx context.Context, client bootstrapClient, credentialsPath string) error {
	health, err := client.Health(ctx)
	if err != nil {
		return fmt.Errorf("openbao: check health before bootstrap: %w", err)
	}

	var creds credentials
	if !health.Initialized {
		keys, rootToken, err := client.Init(ctx, 1, 1)
		if err != nil {
			return fmt.Errorf("openbao: init: %w", err)
		}
		if len(keys) != 1 {
			return fmt.Errorf("openbao: init returned %d unseal keys, want 1", len(keys))
		}
		creds = credentials{RootToken: rootToken, UnsealKey: keys[0]}
		if err := writeCredentials(credentialsPath, creds); err != nil {
			return fmt.Errorf("openbao: persist credentials after init: %w", err)
		}
		health.Sealed = true // a freshly initialized instance always starts sealed
	} else {
		creds, err = readCredentials(credentialsPath)
		if err != nil {
			return fmt.Errorf("openbao: instance already initialized but local credentials unreadable at %s: %w", credentialsPath, err)
		}
	}

	client.SetToken(creds.RootToken)

	if health.Sealed {
		if _, err := client.Unseal(ctx, creds.UnsealKey); err != nil {
			return fmt.Errorf("openbao: unseal: %w", err)
		}
	}
	return nil
}

func writeCredentials(path string, creds credentials) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create credentials directory: %w", err)
	}
	buf, err := json.Marshal(creds)
	if err != nil {
		return fmt.Errorf("marshal credentials: %w", err)
	}
	if err := os.WriteFile(path, buf, 0o600); err != nil {
		return fmt.Errorf("write credentials file: %w", err)
	}
	return nil
}

func readCredentials(path string) (credentials, error) {
	buf, err := os.ReadFile(path)
	if err != nil {
		return credentials{}, fmt.Errorf("read credentials file: %w", err)
	}
	var creds credentials
	if err := json.Unmarshal(buf, &creds); err != nil {
		return credentials{}, fmt.Errorf("parse credentials file: %w", err)
	}
	return creds, nil
}
