package openbao

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/serendip-infra/serendip-infra/providers/kubernetes"
)

// Namespace is shared with providers/storage/localpath -- whichever
// provider's Ensure/NewProvider runs first creates it (idempotent
// GET-then-Create, safe either order). A dedicated namespace keeps
// every Serendip-managed object visibly separate from anything else a
// user might put in the cluster.
const Namespace = "serendip-system"

const (
	StatefulSetName = "openbao"
	ServiceName     = "openbao"
	PVCName         = "openbao-data"
	ConfigMapName   = "openbao-config"
	Image           = "quay.io/openbao/openbao:2.7.1"
	ListenPort      = 8200
)

// serverConfigHCL is OpenBao's server config -- pebbledb storage
// backend (standalone mode, single-node, non-HA, ADR 0009), a plain
// tcp listener with TLS disabled (fine since this stays cluster-
// internal, reached only via the Kubernetes API server's Service-proxy
// subresource -- see doc.go). NOT "file": OpenBao 2.7.0 removed the
// file storage backend entirely (confirmed via oss-researcher against
// OpenBao's own issue #4077/PR #4088 -- the project's own packaged
// default config hit this exact "unknown storage type file" crash
// post-2.7.0 and was fixed by switching to pebbledb, the documented
// non-HA single-node durable replacement). Deliberately carries NO
// mlock-related directive at all: OpenBao 2.7+ removed mlock entirely,
// and any disable_mlock key (even "false") is now a fatal startup
// error, not a no-op (confirmed via oss-researcher against OpenBao's
// own current docs).
const serverConfigHCL = `storage "pebbledb" {
  path = "/openbao/data"
}

listener "tcp" {
  address     = "0.0.0.0:8200"
  tls_disable = "true"
}
`

// Deployer constructs the OpenBao Kubernetes deployment directly via
// providers/kubernetes.Client -- no Helm (ADR 0009), same precedent
// providers/storage/localpath set for bypassing Helm/dynamic-
// provisioning machinery in favor of direct API object creation.
type Deployer struct {
	client *kubernetes.Client

	// waitReadyPollInterval/waitReadyTimeout default to the package
	// constants below; overridable (unexported, same-package tests
	// only) so deploy_test.go can exercise the ready-polling and
	// timeout paths in milliseconds instead of real minutes.
	waitReadyPollInterval time.Duration
	waitReadyTimeout      time.Duration
}

// NewDeployer builds a Deployer. Call Ensure to actually create the
// deployment -- construction alone performs no API calls.
func NewDeployer(client *kubernetes.Client) *Deployer {
	return &Deployer{
		client:                client,
		waitReadyPollInterval: defaultWaitReadyPollInterval,
		waitReadyTimeout:      defaultWaitReadyTimeout,
	}
}

// Ensure idempotently creates (GET-then-Create) the namespace,
// ConfigMap, PVC, Service, and StatefulSet, in that dependency order --
// each later object references an earlier one by name (the
// StatefulSet mounts the ConfigMap and the PVC; the Service selects
// the StatefulSet's Pods), so creating them any other order would let
// a reference dangle, even though Kubernetes itself doesn't enforce
// creation ordering by reference. Finally waits for the StatefulSet's
// Pod to actually be ready (not just created) -- callers (wireOpenBao)
// talk to OpenBao through the Service proxy immediately after Ensure
// returns, with no reconciler/resync loop yet (spec 009's job) to paper
// over a Pod that's still pulling its image or replaying PebbleDB's
// WAL, so Ensure itself owns that wait.
func (d *Deployer) Ensure(ctx context.Context) error {
	if err := d.ensureNamespace(ctx); err != nil {
		return fmt.Errorf("openbao: ensure namespace %s: %w", Namespace, err)
	}
	if err := d.ensureConfigMap(ctx); err != nil {
		return fmt.Errorf("openbao: ensure ConfigMap %s: %w", ConfigMapName, err)
	}
	if err := d.ensurePVC(ctx); err != nil {
		return fmt.Errorf("openbao: ensure PVC %s: %w", PVCName, err)
	}
	if err := d.ensureService(ctx); err != nil {
		return fmt.Errorf("openbao: ensure Service %s: %w", ServiceName, err)
	}
	if err := d.ensureStatefulSet(ctx); err != nil {
		return fmt.Errorf("openbao: ensure StatefulSet %s: %w", StatefulSetName, err)
	}
	if err := d.waitStatefulSetReady(ctx); err != nil {
		return fmt.Errorf("openbao: wait for StatefulSet %s ready: %w", StatefulSetName, err)
	}
	return nil
}

// statefulSetStatus is the subset of a StatefulSet's status this
// package needs to tell "Pod exists but isn't ready yet" (the ordinary,
// expected state right after ensureStatefulSet creates it) apart from
// "actually ready."
type statefulSetStatus struct {
	ReadyReplicas int `json:"readyReplicas"`
}

// defaultWaitReadyPollInterval/defaultWaitReadyTimeout bound how long
// Ensure will wait for the StatefulSet's Pod to report ready. The
// timeout is deliberately generous -- comfortably above the
// startupProbe's own 2s*30=60s budget for PebbleDB WAL replay on a
// slow/cold node (ADR 0002's reference hardware, not just this dev
// machine).
const (
	defaultWaitReadyPollInterval = 2 * time.Second
	defaultWaitReadyTimeout      = 90 * time.Second
)

// waitStatefulSetReady polls the StatefulSet's status until
// readyReplicas >= 1, respecting ctx cancellation and an overall
// timeout -- a fresh image pull or PebbleDB's WAL replay on first boot
// both take real wall-clock time that a single GET-then-proceed check
// would race.
func (d *Deployer) waitStatefulSetReady(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, d.waitReadyTimeout)
	defer cancel()

	path := fmt.Sprintf("/apis/apps/v1/namespaces/%s/statefulsets/%s", Namespace, StatefulSetName)
	ticker := time.NewTicker(d.waitReadyPollInterval)
	defer ticker.Stop()

	for {
		obj, err := d.client.Get(ctx, path)
		if err != nil {
			return err
		}
		var status statefulSetStatus
		if len(obj.Status) > 0 {
			if err := json.Unmarshal(obj.Status, &status); err != nil {
				return fmt.Errorf("decode StatefulSet status: %w", err)
			}
		}
		if status.ReadyReplicas >= 1 {
			return nil
		}

		select {
		case <-ctx.Done():
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return fmt.Errorf("timed out waiting for ready Pod: %w", ctx.Err())
			}
			return fmt.Errorf("stopped waiting for ready Pod: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

func (d *Deployer) ensureNamespace(ctx context.Context) error {
	_, err := d.client.Get(ctx, "/api/v1/namespaces/"+Namespace)
	if err == nil {
		return nil
	}
	if !errors.Is(err, kubernetes.ErrNotFound) {
		return err
	}
	_, err = d.client.Create(ctx, "/api/v1/namespaces", kubernetes.Object{
		APIVersion: "v1",
		Kind:       "Namespace",
		Metadata:   kubernetes.ObjectMeta{Name: Namespace},
	})
	return err
}

func (d *Deployer) ensureConfigMap(ctx context.Context) error {
	path := fmt.Sprintf("/api/v1/namespaces/%s/configmaps/%s", Namespace, ConfigMapName)
	_, err := d.client.Get(ctx, path)
	if err == nil {
		return nil
	}
	if !errors.Is(err, kubernetes.ErrNotFound) {
		return err
	}
	dataBytes, err := json.Marshal(map[string]string{"server.hcl": serverConfigHCL})
	if err != nil {
		return fmt.Errorf("marshal ConfigMap data: %w", err)
	}
	_, err = d.client.Create(ctx, fmt.Sprintf("/api/v1/namespaces/%s/configmaps", Namespace), kubernetes.Object{
		APIVersion: "v1",
		Kind:       "ConfigMap",
		Metadata:   kubernetes.ObjectMeta{Name: ConfigMapName, Namespace: Namespace},
		Data:       dataBytes,
	})
	return err
}

// ensurePVC deliberately omits storageClassName so Kubernetes picks
// the cluster's default StorageClass (K3s's bundled local-path,
// dynamic provisioning, standard WaitForFirstConsumer binding --
// untouched, not bypassed). This differs from providers/storage/
// localpath's own static PV/PVC approach: that provider had no
// consuming Pod to trigger WaitForFirstConsumer's scheduling-driven
// binding, but the StatefulSet's Pod here IS a real consuming Pod, so
// the provisioner's normal, as-designed dynamic-provisioning path
// works with zero workarounds -- see plan.md's open-question 6.
func (d *Deployer) ensurePVC(ctx context.Context) error {
	path := fmt.Sprintf("/api/v1/namespaces/%s/persistentvolumeclaims/%s", Namespace, PVCName)
	_, err := d.client.Get(ctx, path)
	if err == nil {
		return nil
	}
	if !errors.Is(err, kubernetes.ErrNotFound) {
		return err
	}
	specBytes, err := json.Marshal(map[string]any{
		"accessModes": []string{"ReadWriteOnce"},
		"resources": map[string]any{
			"requests": map[string]string{"storage": "1Gi"},
		},
	})
	if err != nil {
		return fmt.Errorf("marshal PVC spec: %w", err)
	}
	_, err = d.client.Create(ctx, fmt.Sprintf("/api/v1/namespaces/%s/persistentvolumeclaims", Namespace), kubernetes.Object{
		APIVersion: "v1",
		Kind:       "PersistentVolumeClaim",
		Metadata:   kubernetes.ObjectMeta{Name: PVCName, Namespace: Namespace},
		Spec:       specBytes,
	})
	return err
}

func (d *Deployer) ensureService(ctx context.Context) error {
	path := fmt.Sprintf("/api/v1/namespaces/%s/services/%s", Namespace, ServiceName)
	_, err := d.client.Get(ctx, path)
	if err == nil {
		return nil
	}
	if !errors.Is(err, kubernetes.ErrNotFound) {
		return err
	}
	specBytes, err := json.Marshal(map[string]any{
		"selector": map[string]string{"app": "openbao"},
		"ports": []map[string]any{
			{"port": ListenPort, "targetPort": ListenPort},
		},
	})
	if err != nil {
		return fmt.Errorf("marshal Service spec: %w", err)
	}
	_, err = d.client.Create(ctx, fmt.Sprintf("/api/v1/namespaces/%s/services", Namespace), kubernetes.Object{
		APIVersion: "v1",
		Kind:       "Service",
		Metadata:   kubernetes.ObjectMeta{Name: ServiceName, Namespace: Namespace},
		Spec:       specBytes,
	})
	return err
}

// ensureStatefulSet's container args pass ONLY "server" -- a real,
// reproducible bug (found via T007 real-cluster verification, on both
// a WSL2 K3s cluster and an independent kind/Docker cluster, ruling
// out any host-specific flakiness) in an earlier version of this
// comment's own reasoning: the official entrypoint's default command
// is already "bao server -config=/openbao/config \"$@\"", so an
// earlier attempt at explicitly also passing "-config=/openbao/config"
// here (reasoning it "matches the image's own convention exactly")
// instead made that flag appear TWICE in the final command line --
// once from the entrypoint's own hardcoded default, once from this
// arg being appended as its "$@". Since "-config" in directory mode
// tells OpenBao to scan/merge every config file in that directory,
// processing the flag twice loads (and binds) the listener stanza
// twice within the same process, crashing immediately and
// deterministically on the very first boot with "listen tcp4
// 0.0.0.0:8200: bind: address already in use" -- not a restart race,
// reproduced identically on a pod that had never started before.
// Passing just "server" relies entirely on the entrypoint's own
// default, with no flag ever duplicated.
func (d *Deployer) ensureStatefulSet(ctx context.Context) error {
	path := fmt.Sprintf("/apis/apps/v1/namespaces/%s/statefulsets/%s", Namespace, StatefulSetName)
	_, err := d.client.Get(ctx, path)
	if err == nil {
		return nil
	}
	if !errors.Is(err, kubernetes.ErrNotFound) {
		return err
	}
	labels := map[string]string{"app": "openbao"}
	specBytes, err := json.Marshal(map[string]any{
		"serviceName": ServiceName,
		"replicas":    1,
		"selector":    map[string]any{"matchLabels": labels},
		"template": map[string]any{
			"metadata": map[string]any{"labels": labels},
			"spec": map[string]any{
				"terminationGracePeriodSeconds": 30,
				"containers": []map[string]any{
					{
						"name":  "openbao",
						"image": Image,
						"args":  []string{"server"},
						"ports": []map[string]any{
							{"containerPort": ListenPort},
						},
						"volumeMounts": []map[string]any{
							{"name": "config", "mountPath": "/openbao/config"},
							{"name": "data", "mountPath": "/openbao/data"},
						},
						"resources": map[string]any{
							"requests": map[string]string{"cpu": "100m", "memory": "128Mi"},
							"limits":   map[string]string{"cpu": "250m", "memory": "512Mi"},
						},
						// startupProbe: PebbleDB WAL replay can take a
						// moment on a non-empty data dir -- a generous
						// failureThreshold avoids kubelet judging the
						// container unhealthy mid-init. tcpSocket only
						// (not an HTTP health check): just confirms the
						// listener actually bound, nothing OpenBao-
						// specific about readiness/seal state here.
						"startupProbe": map[string]any{
							"tcpSocket":        map[string]any{"port": ListenPort},
							"periodSeconds":    2,
							"failureThreshold": 30,
						},
						// preStop delays SIGTERM delivery by 5s -- a
						// container restart within the same Pod reuses its
						// network namespace, so the previous bao process
						// must fully release port 8200 before the next
						// attempt binds it (openbao-helm's own documented
						// preStop convention for exactly this class of
						// restart race).
						"lifecycle": map[string]any{
							"preStop": map[string]any{
								"exec": map[string]any{"command": []string{"sh", "-c", "sleep 5"}},
							},
						},
					},
				},
				"volumes": []map[string]any{
					{"name": "config", "configMap": map[string]any{"name": ConfigMapName}},
					{"name": "data", "persistentVolumeClaim": map[string]any{"claimName": PVCName}},
				},
			},
		},
	})
	if err != nil {
		return fmt.Errorf("marshal StatefulSet spec: %w", err)
	}
	_, err = d.client.Create(ctx, fmt.Sprintf("/apis/apps/v1/namespaces/%s/statefulsets", Namespace), kubernetes.Object{
		APIVersion: "apps/v1",
		Kind:       "StatefulSet",
		Metadata:   kubernetes.ObjectMeta{Name: StatefulSetName, Namespace: Namespace, Labels: labels},
		Spec:       specBytes,
	})
	return err
}
