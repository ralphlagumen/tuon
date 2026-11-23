#!/bin/sh
# Review a dependency change before committing it (see DEPENDENCIES.md):
# every module that changed in go.mod since the base commit, with its age
# (refused under 14 days), whether tuon links it and where to read the
# diff; then checksums and known vulnerabilities.
#
#   ./scripts/deps-review.sh [--base REV] [--allow mod@ver]…
#
# depsreview and devsandbox are standard library only: running them runs
# no dependency code.
set -eu
cd "$(dirname "$0")/.."
status=0
go run ./scripts/depsreview "$@" || status=$?
echo "== module checksums"; go mod verify
echo "== vulnerabilities (sandboxed)"
if [ "$(uname)" = Darwin ]; then
  go run ./scripts/devsandbox --net -- go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 ./... | tail -3
else
  go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 ./... | tail -3
fi
exit $status
