#!/usr/bin/env bash
# Fast feedback after every edit: format + vet + build the module if Go files changed.
cd "$(git rev-parse --show-toplevel 2>/dev/null || pwd)"
changed=$(git diff --name-only 2>/dev/null | grep "\.go$" || true)
[ -z "$changed" ] && exit 0
unformatted=$(gofmt -l $changed 2>/dev/null || true)
if [ -n "$unformatted" ]; then
  echo "gofmt needed: $unformatted" >&2
  gofmt -w $unformatted
fi
go build ./... 2>&1 | head -30
go vet ./... 2>&1 | head -30
exit 0
