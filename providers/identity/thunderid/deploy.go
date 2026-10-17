package thunderid

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"

	"github.com/serendip-infra/serendip-infra/providers/internal/helm"
	"github.com/serendip-infra/serendip-infra/providers/kubernetes"
)

// Deployer installs ThunderID with its Helm chart.
type Deployer struct {
	run    helm.Runner
	client *kubernetes.Client
}

// NewDeployer checks helm is available and targets kubeconfigPath's
// cluster.
func NewDeployer(client *kubernetes.Client, kubeconfigPath string) (*Deployer, error) {
	if err := helm.CheckAvailable(); err != nil {
		return nil, fmt.Errorf("thunderid: %w", err)
	}
	return &Deployer{run: helm.WithKubeconfig(helm.Run, kubeconfigPath), client: client}, nil
}

// Ensure installs or upgrades the release for issuer and waits for it to be
// Ready. Safe to call on every start: the chart's setup job (admin user,
// client, signing keys) and its PVCs are pre-install hooks, so an upgrade
// re-renders configuration without re-seeding (decision 2; T001 finding
// 8). The namespace and admin Secret are ensured first because the setup
// job reads the password from that Secret.
func (d *Deployer) Ensure(ctx context.Context, issuer string) error {
	if err := ensureNamespace(ctx, d.client); err != nil {
		return fmt.Errorf("thunderid: namespace: %w", err)
	}
	if err := EnsureAdminSecret(ctx, d.client); err != nil {
		return fmt.Errorf("thunderid: %w", err)
	}
	vals, err := values(issuer)
	if err != nil {
		return fmt.Errorf("thunderid: %w", err)
	}
	f, err := os.CreateTemp("", "serendip-thunderid-values-*.json")
	if err != nil {
		return fmt.Errorf("thunderid: values file: %w", err)
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(vals); err != nil {
		f.Close()
		return fmt.Errorf("thunderid: values file: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("thunderid: values file: %w", err)
	}
	if _, err := d.run(ctx,
		"upgrade", "--install", Release, Chart,
		"--version", ChartVersion,
		"--namespace", Namespace,
		"-f", f.Name(),
		"--wait", "--timeout", "5m",
	); err != nil {
		return fmt.Errorf("thunderid: install: %w", err)
	}
	return nil
}

func ensureNamespace(ctx context.Context, client *kubernetes.Client) error {
	_, err := client.Apply(ctx, "/api/v1/namespaces/"+Namespace, kubernetes.Object{
		APIVersion: "v1",
		Kind:       "Namespace",
		Metadata:   kubernetes.ObjectMeta{Name: Namespace},
	}, kubernetes.FieldManager)
	return err
}

const (
	adminSecretPath = "/api/v1/namespaces/" + Namespace + "/secrets/" + AdminSecretName
	// certsPVCPath is the chart's certs volume: a pre-install hook that
	// outlives the release, so its presence means this cluster already
	// holds a seeded install (T001 finding 1).
	certsPVCPath = "/api/v1/namespaces/" + Namespace + "/persistentvolumeclaims/" + Release + "-certs-pvc"
)

// EnsureAdminSecret creates the administrator password Secret for a fresh
// install, and otherwise leaves things exactly as they are (decision 3, as
// amended in T005):
//   - present: never updated -- the chart's one-time setup job has already
//     stored whatever it held, so a new value would only make it lie;
//   - missing on a cluster that already holds an install (the chart's certs
//     PVC exists): not recreated, for the same reason -- a fresh random
//     password would not be the admin's. Logged, and login keeps working
//     for anyone who knows the password;
//   - missing on a fresh cluster: created with a random password.
//
// The namespace must exist.
func EnsureAdminSecret(ctx context.Context, client *kubernetes.Client) error {
	_, err := client.Get(ctx, adminSecretPath)
	if err == nil {
		return nil
	}
	if !errors.Is(err, kubernetes.ErrNotFound) {
		return fmt.Errorf("admin secret: %w", err)
	}
	switch _, err := client.Get(ctx, certsPVCPath); {
	case err == nil:
		log.Printf("thunderid: admin secret %s/%s is missing on an existing install; not recreating it, since a new password would not be the stored one", Namespace, AdminSecretName)
		return nil
	case !errors.Is(err, kubernetes.ErrNotFound):
		return fmt.Errorf("admin secret: check for an existing install: %w", err)
	}
	password, err := newPassword()
	if err != nil {
		return fmt.Errorf("admin secret: %w", err)
	}
	data, err := json.Marshal(map[string][]byte{AdminSecretKey: []byte(password)})
	if err != nil {
		return fmt.Errorf("admin secret: %w", err)
	}
	_, err = client.Create(ctx, "/api/v1/namespaces/"+Namespace+"/secrets", kubernetes.Object{
		APIVersion: "v1",
		Kind:       "Secret",
		Metadata: kubernetes.ObjectMeta{
			Name:      AdminSecretName,
			Namespace: Namespace,
			Labels:    map[string]string{"app.kubernetes.io/managed-by": "serendip"},
		},
		Data: data,
	})
	if err != nil {
		// Lost a race with another creator: the Secret exists, which is all
		// this function promises.
		if _, getErr := client.Get(ctx, adminSecretPath); getErr == nil {
			return nil
		}
		return fmt.Errorf("admin secret: create: %w", err)
	}
	return nil
}

// newPassword returns 32 random bytes as unpadded base64url.
func newPassword() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
