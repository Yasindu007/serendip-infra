package localpath

import (
	"context"
	"errors"
	"fmt"

	"github.com/serendip-infra/serendip-infra/providers/kubernetes"
)

const (
	// Namespace is where every Serendip-managed PersistentVolumeClaim
	// lives -- a dedicated namespace, not "default", keeps
	// control-plane-managed objects visibly separate from anything else
	// a user might put in the cluster.
	Namespace = "serendip-system"

	storageClassName = "serendip-local-path"
	// provisionerName is the real upstream local-path-provisioner name
	// -- referenced, not reimplemented (constitution rule 10).
	provisionerName = "rancher.io/local-path"
)

// Provider implements core/reconcile.StorageProvider against
// local-path-provisioner.
type Provider struct {
	client *kubernetes.Client
}

// NewProvider ensures the Namespace and an Immediate-binding-mode
// StorageClass both exist (idempotent get-then-create) before
// returning -- deliberately NOT K3s's bundled default "local-path"
// StorageClass, which uses volumeBindingMode: WaitForFirstConsumer and
// would leave every Volume Pending forever, since no Application
// Platform provider exists yet to ever schedule a consuming Pod
// (specs/007-storage-local-path-provider/spec.md). Setup failures
// surface at cmd/serendipd startup, not on the first Volume creation.
func NewProvider(ctx context.Context, client *kubernetes.Client) (*Provider, error) {
	p := &Provider{client: client}
	if err := p.ensureNamespace(ctx); err != nil {
		return nil, fmt.Errorf("localpath: ensure namespace %s: %w", Namespace, err)
	}
	if err := p.ensureStorageClass(ctx); err != nil {
		return nil, fmt.Errorf("localpath: ensure storage class %s: %w", storageClassName, err)
	}
	return p, nil
}

func (p *Provider) ensureNamespace(ctx context.Context) error {
	_, err := p.client.Get(ctx, "/api/v1/namespaces/"+Namespace)
	if err == nil {
		return nil // already exists
	}
	if !errors.Is(err, kubernetes.ErrNotFound) {
		return err
	}
	_, err = p.client.Create(ctx, "/api/v1/namespaces", kubernetes.Object{
		APIVersion: "v1",
		Kind:       "Namespace",
		Metadata:   kubernetes.ObjectMeta{Name: Namespace},
	})
	return err
}

// storageClassWire mirrors a StorageClass's real wire shape --
// provisioner/volumeBindingMode/reclaimPolicy are top-level fields, not
// nested under "spec" like every other kind this project has touched
// so far (confirmed directly: kubernetes.Object's spec/status envelope
// genuinely doesn't fit this kind, see GetRaw/CreateRaw's doc comments).
type storageClassWire struct {
	APIVersion        string                `json:"apiVersion"`
	Kind              string                `json:"kind"`
	Metadata          kubernetes.ObjectMeta `json:"metadata"`
	Provisioner       string                `json:"provisioner"`
	VolumeBindingMode string                `json:"volumeBindingMode"`
	ReclaimPolicy     string                `json:"reclaimPolicy,omitempty"`
}

func (p *Provider) ensureStorageClass(ctx context.Context) error {
	path := "/apis/storage.k8s.io/v1/storageclasses/" + storageClassName
	_, err := p.client.GetRaw(ctx, path)
	if err == nil {
		return nil // already exists
	}
	if !errors.Is(err, kubernetes.ErrNotFound) {
		return err
	}
	_, err = p.client.CreateRaw(ctx, "/apis/storage.k8s.io/v1/storageclasses", storageClassWire{
		APIVersion: "storage.k8s.io/v1",
		Kind:       "StorageClass",
		Metadata:   kubernetes.ObjectMeta{Name: storageClassName},
		// Deliberately no "default" annotation -- Serendip's own PVCs
		// always reference this StorageClass by name explicitly, so
		// default-ness buys nothing and risks an ambiguous-default
		// warning alongside K3s's own bundled "local-path" StorageClass,
		// which typically is marked default.
		Provisioner:       provisionerName,
		VolumeBindingMode: "Immediate",
		ReclaimPolicy:     "Delete",
	})
	return err
}
