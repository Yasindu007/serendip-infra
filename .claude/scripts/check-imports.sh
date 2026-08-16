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
