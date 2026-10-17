#!/usr/bin/env bash
# Blocks kubectl/helm mutations unless current context is a kind-* or k3d-* dev cluster.
# Reads the tool input JSON from stdin (Claude Code hook contract).
# Matches against the whole input, not just the "command" field up to its first
# comma, so separators or commas earlier in the command can't hide a mutation.
# Flags may sit between the binary and the verb (kubectl -n x apply); a pipe or
# command separator ends the match.
input=$(cat)
if echo "$input" | grep -qE "kubectl[^|;&]*[[:space:]](apply|delete|patch|edit|drain|cordon|scale)([[:space:]\"]|$)|helm[^|;&]*[[:space:]](install|upgrade|uninstall|rollback)([[:space:]\"]|$)"; then
  # Bounded so an unreachable kubeconfig/exec plugin can't stall every Bash call;
  # a timeout yields "none", which blocks (fail closed).
  ctx=$(timeout 5 kubectl config current-context 2>/dev/null || echo "none")
  case "$ctx" in
    kind-*|k3d-*) exit 0 ;;
    *)
      echo "BLOCKED: kubectl/helm mutation on context '$ctx' — only kind-*/k3d-* dev clusters are allowed from Claude Code. Switch context or run manually." >&2
      exit 2
      ;;
  esac
fi
exit 0
