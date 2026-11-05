#!/bin/sh
# Review a dependency change before committing it (see DEPENDENCIES.md):
# what changed in go.mod since the last commit, which of it is watched,
# and whether checksums and vulnerability checks pass.
#
#   ./scripts/deps-review.sh [base-commit]   (default: HEAD)
set -eu
cd "$(dirname "$0")/.."
base="${1:-HEAD}"
watched='go.mau.fi/|github.com/beeper/|github.com/imroc/req|golang.org/x/crypto|github.com/ncruces/|lukechampine.com/|github.com/zalando/go-keyring'

changes=$(git diff "$base" -- go.mod | grep -E '^[+-][[:space:]]' | grep -vE '^(\+\+\+|---)' || true)
if [ -z "$changes" ]; then
  echo "== go.mod unchanged since $base"
else
  echo "== go.mod changes since $base"
  echo "$changes" | sed 's/^/  /'
  hits=$(echo "$changes" | grep -E "$watched" || true)
  if [ -n "$hits" ]; then
    echo
    echo "== WATCHED modules changed: read their diffs before committing (DEPENDENCIES.md)"
    echo "$hits" | sed 's/^/  /'
  fi
  if git diff "$base" -- go.mod | grep -qE '^\+replace'; then
    echo
    echo "== a new 'replace' directive was added: review where it points"
  fi
fi
echo "== module checksums"; go mod verify
echo "== vulnerabilities";  go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 ./... | tail -3
