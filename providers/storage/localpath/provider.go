package localpath

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	"github.com/serendip-infra/serendip-infra/core/reconcile"
	"github.com/serendip-infra/serendip-infra/core/resource"
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

// pvcName derives a deterministic PersistentVolumeClaim name from vol's
// own Metadata.ID -- no separate K8s object name needs tracking in
// VolumeStatus, Ensure/Delete can always recompute it. Kubernetes
// object names must be DNS-1123 subdomains; Serendip's UUIDv7 IDs are
// already lowercase hex and hyphens, so no sanitization beyond the
// prefix is needed.
func pvcName(vol resource.Volume) string {
	return "vol-" + vol.Metadata.ID
}

func (p *Provider) pvcPath(vol resource.Volume) string {
	return fmt.Sprintf("/api/v1/namespaces/%s/persistentvolumeclaims/%s", Namespace, pvcName(vol))
}

type pvcResources struct {
	Requests map[string]string `json:"requests"`
}

type pvcSpecWire struct {
	AccessModes      []string     `json:"accessModes"`
	StorageClassName string       `json:"storageClassName"`
	Resources        pvcResources `json:"resources"`
}

type pvcStatusWire struct {
	Phase string `json:"phase,omitempty"`
}

// Ensure creates vol's backing PersistentVolumeClaim if it doesn't
// exist yet, then reports its real status.phase -- "Pending", "Bound",
// or "Lost" are the real, standard Kubernetes PVC phase values, used
// directly as reason so an operator reading the Volume's Condition sees
// the real upstream state, not a Serendip-invented label. A
// freshly-created PVC's response may not yet report a phase at all;
// that's treated as "Pending", not an error -- it hasn't had a chance
// to bind yet.
func (p *Provider) Ensure(ctx context.Context, vol resource.Volume) (bound bool, reason, message string, err error) {
	path := p.pvcPath(vol)
	obj, err := p.client.Get(ctx, path)
	if errors.Is(err, kubernetes.ErrNotFound) {
		obj, err = p.create(ctx, vol)
		if err != nil {
			return false, "", "", fmt.Errorf("localpath: create PVC %s: %w", pvcName(vol), err)
		}
	} else if err != nil {
		return false, "", "", fmt.Errorf("localpath: get PVC %s: %w", pvcName(vol), err)
	}

	var status pvcStatusWire
	if len(obj.Status) > 0 {
		if err := json.Unmarshal(obj.Status, &status); err != nil {
			return false, "", "", fmt.Errorf("localpath: decode PVC %s status: %w", pvcName(vol), err)
		}
	}
	phase := status.Phase
	if phase == "" {
		phase = "Pending"
	}
	return phase == "Bound", phase, "", nil
}

func (p *Provider) create(ctx context.Context, vol resource.Volume) (kubernetes.Object, error) {
	specBytes, err := json.Marshal(pvcSpecWire{
		AccessModes:      []string{"ReadWriteOnce"},
		StorageClassName: storageClassName,
		Resources: pvcResources{Requests: map[string]string{
			// A bare integer is a valid Kubernetes resource.Quantity,
			// interpreted as bytes with no suffix -- no need to format
			// as "Gi"/"Mi".
			"storage": strconv.FormatInt(vol.Spec.SizeBytes, 10),
		}},
	})
	if err != nil {
		return kubernetes.Object{}, fmt.Errorf("marshal PVC spec: %w", err)
	}
	createPath := fmt.Sprintf("/api/v1/namespaces/%s/persistentvolumeclaims", Namespace)
	return p.client.Create(ctx, createPath, kubernetes.Object{
		APIVersion: "v1",
		Kind:       "PersistentVolumeClaim",
		Metadata:   kubernetes.ObjectMeta{Name: pvcName(vol), Namespace: Namespace},
		Spec:       specBytes,
	})
}

// Delete removes vol's backing PersistentVolumeClaim. Already being
// gone is success, not an error -- matches StorageProvider.Delete's own
// documented contract.
func (p *Provider) Delete(ctx context.Context, vol resource.Volume) error {
	err := p.client.Delete(ctx, p.pvcPath(vol))
	if errors.Is(err, kubernetes.ErrNotFound) {
		return nil
	}
	return err
}

var _ reconcile.StorageProvider = (*Provider)(nil)
