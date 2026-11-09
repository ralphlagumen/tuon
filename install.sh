#!/bin/sh
# One-command local install: builds tuon, installs Ghostty (the terminal that
# shows real photos) if missing, and puts `tuon` on your PATH.
#
#   ./install.sh            install to ~/.local/bin
#   PREFIX=/usr/local/bin ./install.sh
set -eu

cd "$(dirname "$0")"
PREFIX="${PREFIX:-$HOME/.local/bin}"

say() { printf '  %s\n' "$*"; }

command -v go >/dev/null 2>&1 || { say "Go is needed to build tuon: brew install go"; exit 1; }

say "Building tuon…"
mkdir -p bin
# the same flags as releases; the version is the signed tag checked out, if any
VERSION=$(git describe --tags --exact-match --match 'v*' 2>/dev/null || echo dev)
# tuon is the small launcher; tuon-app (next to it) is the chat app it runs in the sandbox
for cmd in tuon-app tuon; do
  CGO_ENABLED=0 GOWORK=off go build -trimpath -buildvcs=false -ldflags "-s -w -buildid= -X main.version=$VERSION" -o "bin/$cmd" "./cmd/$cmd"
done
rm -f "$HOME/.config/tuon/source" # older versions kept the clone's path here; updates no longer use it

if [ "$(uname)" = "Darwin" ] && [ ! -d /Applications/Ghostty.app ] && [ ! -d "$HOME/Applications/Ghostty.app" ]; then
  if command -v brew >/dev/null 2>&1; then
    say "Installing Ghostty (shows real photos)…"
    brew install --cask ghostty
  else
    say "Skipping Ghostty: Homebrew not found. Photos open in your viewer (View photo)."
  fi
fi

# the Touch ID lock: a small Swift helper that uses the Secure Enclave
HELPER=""
if [ "$(uname)" = "Darwin" ]; then
  if command -v swiftc >/dev/null 2>&1; then
    say "Building the Touch ID helper…"
    swiftc -O cmd/tuon-keyhelper/main.swift -o bin/tuon-keyhelper && HELPER=1
  else
    say "No Swift compiler (xcode-select --install): tuon will use a passphrase instead of Touch ID."
  fi
fi

mkdir -p "$PREFIX"
install -m 0755 bin/tuon-app "$PREFIX/tuon-app" # the app first: a new launcher never runs next to an old app
install -m 0755 bin/tuon "$PREFIX/tuon"
[ -n "$HELPER" ] && install -m 0755 bin/tuon-keyhelper "$PREFIX/tuon-keyhelper"
say "Installed $PREFIX/tuon"

case ":$PATH:" in
  *":$PREFIX:"*) ;;
  *) say "Add this to your shell profile so \`tuon\` works everywhere:"
     say "  export PATH=\"$PREFIX:\$PATH\"" ;;
esac

# log in and restore older encrypted chats, when there's someone to answer
if [ -t 0 ] && [ -t 1 ] && [ -z "${TUON_SKIP_SETUP:-}" ]; then
  "$PREFIX/tuon" setup || say "Setup didn't finish. Run \`tuon setup\` any time."
fi

say "Done. Run: tuon  (in Ghostty, kitty or WezTerm for real photos)"
