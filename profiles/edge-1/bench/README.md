# edge-1 benchmarks

Constitution rule 9: benchmarks live here and run on reference hardware (a
ThinkPad T470) before profile defaults change. Rule 4 sets the budget they
check: the control plane under 256 MB RSS and 5% CPU at idle.

| File | What |
|---|---|
| `001-resource-model.md` | Spec 001: 10k-resource CRUD smoke test (dev machine) |
| `002-edge1-footprint.md` | Spec 013: whole edge-1 stack idle footprint (simulated T470) |
| `results/` | Raw `result.json` / `summary.md` per run |

## The footprint harness: `cmd/bench-edge1`

One command benchmarks the whole edge-1 stack as `serendipd` deploys it:

1. Starts `serendipd` against a cluster and times a **cold start** (every
   install, including image pulls).
2. Waits until every pod is Ready, settles, then samples **idle memory and
   CPU per component group** and `serendipd`'s own process.
3. Deploys a **light workload** (3 exposed nginx Components by default),
   records each one's create → `Serving` time, settles, and samples again.
4. Restarts `serendipd` and times a **warm restart**.
5. Writes `result.json` (every sample, machine profile, verdicts) and
   `summary.md`.

**How it measures:** cluster numbers come from the kubelet Summary API
(`/api/v1/nodes/<node>/proxy/stats/summary`, memory = `workingSetBytes`),
read through the API server. Nothing extra is installed in the cluster,
and the same code works on kind and on K3s. `serendipd` is measured as a
host process (`/proc` on Linux, PowerShell on Windows).

**Groups** come from the namespaces Serendip's providers deploy into.
Anything unrecognised is listed by name, never hidden.

**Verdicts in every result:**
- the rule-4 pass/fail line for `serendipd`;
- the 16 GB headroom verdict: idle platform + 10 light Components against
  16 GB − 2 GB host OS. Up to 50% is comfortable, up to 75% tight,
  otherwise it points to 32 GB.

### Run it on a real T470 (the reference-hardware gate)

On the T470, with K3s installed by `serendip-agent bootstrap`:

```sh
go build -o serendipd ./cmd/serendipd
go build -o bench-edge1 ./cmd/bench-edge1
sudo ./bench-edge1 --serendipd ./serendipd --kubeconfig /etc/rancher/k3s/k3s.yaml --out results/<date>-t470/run1
```

Don't pass `--sim-*` flags. A run without them is a real reference-hardware
result. Run it twice and compare:

```sh
./bench-edge1 compare results/<date>-t470/run1/result.json results/<date>-t470/run2/result.json
```

### Simulate a T470 on a dev machine (kind)

```sh
profiles/edge-1/bench/kind-t470-setup.sh up bench-t470 /tmp/bench-t470.kubeconfig
go run ./cmd/bench-edge1 --serendipd ./serendipd --kubeconfig /tmp/bench-t470.kubeconfig \
  --sim-cpus 2 --sim-cpuset 0-3 \
  --sim-note "kind node limited to 4 threads and 2.0 CPUs of quota to approximate an i5-6300U (2c/4t)" \
  --not-simulated "16-32 GB RAM (only measured, not limited); SATA/NVMe disk speed" \
  --out profiles/edge-1/bench/results/<date>-simulated-t470/run1
profiles/edge-1/bench/kind-t470-setup.sh down bench-t470
```

Any `--sim-*` flag marks the run as a simulation. Its summary opens with
"SIMULATION — does not satisfy the reference-hardware gate", and no
footprint may be reported as verified on its strength (constitution rule 6).

## Reading results honestly

- On kind, the "node" is the kind container, not the Docker VM, so the
  totals exclude the VM's own overhead. On K3s, node totals are the host.
- Cold start is dominated by image pulls, so it's network-bound. Warm
  restart is the CPU/API-bound number.
- Create → `Serving` includes the workload image pull, and is quantised by
  the reconcile engine's early re-checks (about 2, 6, 14 and 30 s after
  create, spec 014): a workload ready at 15 s is reported at 30 s.
