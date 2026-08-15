#!/usr/bin/env bash
# Constitution rule 2: core/ must not import providers/ or vendor SDKs.
set -e
violations=$(grep -rn --include="*.go" -E "\"(github\.com/[^\"]*serendip[^\"]*/providers|github\.com/longhorn|github\.com/rancher|github\.com/crossplane|github\.com/openbao|k8s\.io/client-go)" core/ 2>/dev/null || true)
if [ -n "$violations" ]; then
  echo "IMPORT VIOLATIONS in core/:"
  echo "$violations"
  exit 1
fi
echo "core/ import boundaries clean"
