#!/bin/sh
# Linux checks from a Mac (or any machine with Docker): builds tuon and
# every package's tests for Linux, then in a throwaway Alpine container
# runs the tests as a normal user and `tuon sandbox-test` (Landlock and
# seccomp, the launcher's actions with xclip on a virtual display).
# Uses the Docker host's kernel (OrbStack: recent, Landlock 10).
set -eu
cd "$(dirname "$0")/.."
arch=$(docker info --format '{{.Architecture}}' 2>/dev/null) || { echo "linux-test: Docker isn't running"; exit 1; }
case "$arch" in aarch64|arm64) goarch=arm64 ;; *) goarch=amd64 ;; esac
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
mkdir -p "$work/t" "$work/b"
for p in $(go list ./... | grep -v -e /scripts/ -e /cmd/spike); do
  n=$(echo "$p" | sed 's#github.com/ralph/tuon/##; s#/#_#g')
  GOOS=linux GOARCH=$goarch CGO_ENABLED=0 go test -c -o "$work/t/$n.test" "$p" 2>&1 | grep -v "no test files" || true
done
for c in tuon tuon-app; do GOOS=linux GOARCH=$goarch CGO_ENABLED=0 go build -trimpath -o "$work/b/$c" "./cmd/$c"; done
cat > "$work/run.sh" <<'IN'
#!/bin/sh
apk add -q xvfb xclip util-linux-misc >/dev/null 2>&1 || echo "(apk failed: the clipboard and terminal checks will fail)"
adduser -D -u 1000 u >/dev/null 2>&1
fail=0
echo "== tests"
for t in /w/t/*.test; do
  n=$(basename "$t" .test); d=/src/$(echo "$n" | tr _ /)
  if out=$(su u -c "cd $d && HOME=/home/u $t -test.count=1 2>&1"); then echo "ok   $n"; else
    echo "FAIL $n"; echo "$out" | grep -E "^(--- FAIL|    .*_test.go)" | head -8; fail=1; fi
done
echo "== sandbox-test"
Xvfb :99 -nolisten tcp >/dev/null 2>&1 &
sleep 1
mkdir -p /home/u/.local/bin /home/u/Downloads /home/u/Desktop /home/u/.ssh /home/u/.config/tuon
echo secret > /home/u/.ssh/id_probe
cp /w/b/tuon /w/b/tuon-app /home/u/.local/bin/; chown -R u /home/u
su u -c "cd /home/u && DISPLAY=:99 SSH_AUTH_SOCK=/tmp/agent.sock GITHUB_TOKEN=x HOME=/home/u script -qec '/home/u/.local/bin/tuon sandbox-test' /dev/null" || fail=1
exit $fail
IN
chmod +x "$work/run.sh"
docker run --rm -v "$PWD:/src:ro" -v "$work:/w" alpine:latest /w/run.sh
