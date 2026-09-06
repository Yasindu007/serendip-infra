#!/usr/bin/env bash
# Constitution rule 2: core/ must not import providers/ or vendor SDKs.
# api/ is included too -- rule 2's removability bullet ("deleting a
# provider package must not break go build ./core/... ./api/...") only
# holds if api/ never imports providers/ directly either (specs/002-http-api
# T017).
set -e
pattern="\"(github\.com/[^\"]*serendip[^\"]*/providers|github\.com/longhorn|github\.com/rancher|github\.com/crossplane|github\.com/openbao|k8s\.io/client-go)"
for dir in core/ api/; do
  violations=$(grep -rn --include="*.go" -E "$pattern" "$dir" 2>/dev/null || true)
  if [ -n "$violations" ]; then
    echo "IMPORT VIOLATIONS in $dir:"
    echo "$violations"
    exit 1
  fi
  echo "$dir import boundaries clean"
done

# specs/004-bootstrap-agent T007: agent/ imports only core/id from
# core/ (a single-function, dependency-free ID generator -- plan.md's
# resolved wire-format decision is that agent/client defines its own
# structs rather than importing core/resource) and nothing from api/ at
# all -- agent/ is just another API client, like any future UI/CLI/MCP
# consumer (constitution rule 7), not a privileged caller into core/.
agent_pattern="\"github\.com/serendip-infra/serendip-infra/(api|core/(store|registry|reconcile|audit|event|graph|resource))(/[^\"]*)?\""
agent_violations=$(grep -rn --include="*.go" -E "$agent_pattern" agent/ 2>/dev/null || true)
if [ -n "$agent_violations" ]; then
  echo "IMPORT VIOLATIONS in agent/ (only core/id allowed from core/, no api/ imports):"
  echo "$agent_violations"
  exit 1
fi
echo "agent/ import boundaries clean (core/id only, no api/)"
