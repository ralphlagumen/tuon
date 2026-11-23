#!/bin/sh
# Checks to run before every push or release (GitHub CI is off for now):
# module checksums, build, vet, tests, known vulnerabilities.
#
# Tests and govulncheck run dependency code, so on macOS they run inside
# scripts/devsandbox: no home folder, SSH agent, Unix sockets or repo
# writes (tests also no internet). Building and vetting run none.
#
#   ./scripts/check.sh
set -eu
cd "$(dirname "$0")/.."
sbx=""
if [ "$(uname)" = Darwin ]; then
  go build -o bin/devsandbox ./scripts/devsandbox
  sbx="bin/devsandbox"
fi
echo "== module checksums";  go mod verify
echo "== build";             go build ./...
echo "== vet";               go vet ./...
if [ -n "$sbx" ]; then
  echo "== tests (sandboxed)";           $sbx -- go test ./...
  echo "== vulnerabilities (sandboxed)"; $sbx --net -- go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 ./...
else
  echo "== tests (NOT sandboxed: dependency code runs with your rights)"; go test ./...
  echo "== vulnerabilities";   go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 ./...
fi
# the release signing key must not sit in the SSH agent: anything that can
# reach the agent could sign a tuon release
if [ -f "$HOME/.ssh/tuon_signing.pub" ] && command -v ssh-add >/dev/null 2>&1; then
  fp=$(ssh-keygen -lf "$HOME/.ssh/tuon_signing.pub" | awk '{print $2}')
  if ssh-add -l 2>/dev/null | grep -qF "$fp"; then
    echo "== the release signing key is loaded in your SSH agent: ssh-add -d ~/.ssh/tuon_signing"
    exit 1
  fi
fi
echo "== all checks passed"
