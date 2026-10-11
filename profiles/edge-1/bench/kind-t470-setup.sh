#!/usr/bin/env bash
# Creates a kind node constrained to approximate a ThinkPad T470's CPU class
# (spec 013, plan.md decision 1). Kind-specific setup only: the benchmark
# harness itself (cmd/bench-edge1) makes no kind assumptions.
#
#   kind-t470-setup.sh up   <cluster-name> <kubeconfig-out>
#   kind-t470-setup.sh down <cluster-name>
#
# A simulation never satisfies the reference-hardware gate (constitution
# rules 6 and 9).
set -euo pipefail

SIM_CPUS="${SIM_CPUS:-2}"
SIM_CPUSET="${SIM_CPUSET:-0-3}"
NODE_IMAGE="${NODE_IMAGE:-kindest/node:v1.34.0}"
here="$(cd "$(dirname "$0")" && pwd)"

case "${1:-}" in
up)
  name="${2:?cluster name required}"
  out="${3:?kubeconfig output path required}"
  kind create cluster --name "$name" --image "$NODE_IMAGE" --config "$here/kind-t470.yaml" --wait 120s
  node="$name-control-plane"
  docker update --cpus "$SIM_CPUS" --cpuset-cpus "$SIM_CPUSET" "$node" > /dev/null
  kind get kubeconfig --name "$name" > "$out"
  echo "applied limits (from docker inspect):"
  docker inspect "$node" --format '  NanoCpus={{.HostConfig.NanoCpus}} CpusetCpus={{.HostConfig.CpusetCpus}} Memory={{.HostConfig.Memory}}'
  echo "kubeconfig written to $out"
  echo "run the harness with: --sim-cpus $SIM_CPUS --sim-cpuset $SIM_CPUSET"
  ;;
down)
  kind delete cluster --name "${2:?cluster name required}"
  ;;
*)
  echo "usage: $0 up <cluster-name> <kubeconfig-out> | down <cluster-name>" >&2
  exit 2
  ;;
esac
