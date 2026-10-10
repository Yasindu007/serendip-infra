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

// Namespace is where every Serendip-managed PersistentVolumeClaim lives
// -- a dedicated namespace, not "default", keeps control-plane-managed
// objects visibly separate from anything else a user might put in the
// cluster.
const Namespace = "serendip-system"

// hostPathBase mirrors local-path-provisioner's own default backing
// path convention, for familiarity, even though this provider bypasses
// the provisioner entirely (see Provider's doc comment for why).
const hostPathBase = "/var/lib/rancher/k3s/storage"

// Provider implements core/reconcile.StorageProvider via static
// PersistentVolume/PersistentVolumeClaim binding -- NOT by driving
// rancher.io/local-path-provisioner's dynamic provisioning (its own
// bundled StorageClass, or any custom one using
// volumeBindingMode: Immediate). Discovered via real-cluster testing,
// confirmed against the provisioner's own source
// (github.com/rancher/local-path-provisioner, sig-storage-lib-external-
// provisioner's shared controller): in the provisioner's default
// per-node hostPath mode, it hard-requires a target node, supplied only
// via the volume.kubernetes.io/selected-node annotation that the
// Kubernetes scheduler sets as part of WaitForFirstConsumer's delayed-
// binding flow -- Immediate binding mode never populates that
// annotation, so the provisioner fails every Create with "configuration
// error, no node was specified." WaitForFirstConsumer itself was also
// not viable: it needs an actual consuming Pod to trigger scheduling,
// and no Application Platform provider exists yet to ever create one.
//
// The fix: standard, long-predates-dynamic-provisioning Kubernetes
// static binding -- create a PersistentVolume directly (hostPath-backed,
// on this project's single K3s node per ADR 0002's current scope) and a
// PersistentVolumeClaim that references it by name via spec.volumeName.
// A PVC bound this way binds at the API level immediately, with no
// provisioner and no consuming Pod involved -- the exact property
// acceptance criterion 2 needs. This is still "integrate, don't
// rebuild" (constitution rule 10): Serendip declares standard
// Kubernetes API objects; kubelet (not Serendip) creates the backing
// directory on first real mount, exactly as it would for a
// provisioner-created hostPath PV.
type Provider struct {
	client *kubernetes.Client
}

// NewProvider ensures Namespace exists (idempotent get-then-create)
// before returning. Setup failures surface at cmd/serendipd startup,
// not on the first Volume creation.
func NewProvider(ctx context.Context, client *kubernetes.Client) (*Provider, error) {
	p := &Provider{client: client}
	if err := p.ensureNamespace(ctx); err != nil {
		return nil, fmt.Errorf("localpath: ensure namespace %s: %w", Namespace, err)
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

// pvName/pvcName derive deterministic object names from vol's own
// Metadata.ID -- no separate K8s object name needs tracking in
// VolumeStatus, Ensure/Delete can always recompute them. Kubernetes
// object names must be DNS-1123 subdomains; Serendip's UUIDv7 IDs are
// already lowercase hex and hyphens, so no sanitization beyond the
// prefix is needed.
func pvcName(vol resource.Volume) string { return "vol-" + vol.Metadata.ID }
func pvName(vol resource.Volume) string  { return "pv-vol-" + vol.Metadata.ID }

func hostPath(vol resource.Volume) string {
	return hostPathBase + "/" + pvcName(vol)
}

func (p *Provider) pvPath(vol resource.Volume) string {
	return "/api/v1/persistentvolumes/" + pvName(vol)
}

func (p *Provider) pvcPath(vol resource.Volume) string {
	return fmt.Sprintf("/api/v1/namespaces/%s/persistentvolumeclaims/%s", Namespace, pvcName(vol))
}

type pvHostPath struct {
	Path string `json:"path"`
	Type string `json:"type,omitempty"`
}

type pvSpecWire struct {
	Capacity                      map[string]string `json:"capacity"`
	AccessModes                   []string          `json:"accessModes"`
	PersistentVolumeReclaimPolicy string            `json:"persistentVolumeReclaimPolicy"`
	StorageClassName              string            `json:"storageClassName"`
	HostPath                      pvHostPath        `json:"hostPath"`
}

type pvcResources struct {
	Requests map[string]string `json:"requests"`
}

type pvcSpecWire struct {
	AccessModes      []string     `json:"accessModes"`
	Resources        pvcResources `json:"resources"`
	VolumeName       string       `json:"volumeName"`
	StorageClassName string       `json:"storageClassName"`
}

type pvcStatusWire struct {
	Phase string `json:"phase,omitempty"`
}

// quantityBytes formats sizeBytes as a bare Kubernetes resource.Quantity
// string -- a bare integer is valid, interpreted as bytes with no
// suffix needed (confirmed in T005; same helper reused here for the PV
// side of capacity).
func quantityBytes(sizeBytes int64) string {
	return strconv.FormatInt(sizeBytes, 10)
}

// Ensure creates vol's backing PersistentVolume and
// PersistentVolumeClaim (statically bound to each other) if they don't
// exist yet, then reports the PVC's real status.phase -- "Pending",
// "Bound", or "Lost" are the real, standard Kubernetes PVC phase
// values, used directly as reason so an operator reading the Volume's
// Condition sees the real upstream state, not a Serendip-invented
// label. Static binding means Bound should appear essentially
// immediately once both objects exist -- no provisioner, no consuming
// Pod in the loop at all.
func (p *Provider) Ensure(ctx context.Context, vol resource.Volume) (bound bool, reason, message string, err error) {
	if err := p.ensurePV(ctx, vol); err != nil {
		return false, "", "", fmt.Errorf("localpath: ensure PV %s: %w", pvName(vol), err)
	}

	obj, err := p.client.Get(ctx, p.pvcPath(vol))
	if errors.Is(err, kubernetes.ErrNotFound) {
		obj, err = p.createPVC(ctx, vol)
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

func (p *Provider) ensurePV(ctx context.Context, vol resource.Volume) error {
	_, err := p.client.Get(ctx, p.pvPath(vol))
	if err == nil {
		return nil // already exists
	}
	if !errors.Is(err, kubernetes.ErrNotFound) {
		return err
	}

	specBytes, err := json.Marshal(pvSpecWire{
		Capacity:                      map[string]string{"storage": quantityBytes(vol.Spec.SizeBytes)},
		AccessModes:                   []string{"ReadWriteOnce"},
		PersistentVolumeReclaimPolicy: "Delete",
		StorageClassName:              "", // empty, matching the PVC's own empty storageClassName -- static binding, no dynamic-provisioner matching involved
		HostPath:                      pvHostPath{Path: hostPath(vol), Type: "DirectoryOrCreate"},
	})
	if err != nil {
		return fmt.Errorf("marshal PV spec: %w", err)
	}
	_, err = p.client.Create(ctx, "/api/v1/persistentvolumes", kubernetes.Object{
		APIVersion: "v1",
		Kind:       "PersistentVolume",
		Metadata:   kubernetes.ObjectMeta{Name: pvName(vol)},
		Spec:       specBytes,
	})
	return err
}

func (p *Provider) createPVC(ctx context.Context, vol resource.Volume) (kubernetes.Object, error) {
	specBytes, err := json.Marshal(pvcSpecWire{
		AccessModes: []string{"ReadWriteOnce"},
		Resources: pvcResources{Requests: map[string]string{
			"storage": quantityBytes(vol.Spec.SizeBytes),
		}},
		VolumeName:       pvName(vol),
		StorageClassName: "",
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

// Delete removes vol's backing PersistentVolumeClaim and
// PersistentVolume. Already being gone (either one, or both) is
// success, not an error -- matches StorageProvider.Delete's own
// documented contract. PVC is deleted first: a hostPath PV with no
// active claim has nothing blocking its own deletion, but deleting the
// PV while the PVC still references it would be the wrong order to
// reason about even though no finalizer actually enforces it here.
func (p *Provider) Delete(ctx context.Context, vol resource.Volume) error {
	if err := p.client.Delete(ctx, p.pvcPath(vol)); err != nil && !errors.Is(err, kubernetes.ErrNotFound) {
		return fmt.Errorf("localpath: delete PVC %s: %w", pvcName(vol), err)
	}
	if err := p.client.Delete(ctx, p.pvPath(vol)); err != nil && !errors.Is(err, kubernetes.ErrNotFound) {
		return fmt.Errorf("localpath: delete PV %s: %w", pvName(vol), err)
	}
	return nil
}

var _ reconcile.StorageProvider = (*Provider)(nil)
