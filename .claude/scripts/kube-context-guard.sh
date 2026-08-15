#!/usr/bin/env bash
# Blocks kubectl/helm mutations unless current context is a kind-* or k3d-* dev cluster.
# Reads the tool input JSON from stdin (Claude Code hook contract).
input=$(cat)
cmd=$(echo "$input" | grep -o "\"command\"[^,}]*" | head -1)
if echo "$cmd" | grep -qE "kubectl (apply|delete|patch|edit|drain|cordon|scale)|helm (install|upgrade|uninstall|rollback)"; then
  ctx=$(kubectl config current-context 2>/dev/null || echo "none")
  case "$ctx" in
    kind-*|k3d-*) exit 0 ;;
    *)
      echo "BLOCKED: kubectl/helm mutation on context '$ctx' — only kind-*/k3d-* dev clusters are allowed from Claude Code. Switch context or run manually." >&2
      exit 2
      ;;
  esac
fi
exit 0
