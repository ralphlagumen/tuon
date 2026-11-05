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
go build -trimpath -ldflags "-s -w" -o bin/tuon ./cmd/tuon

if [ "$(uname)" = "Darwin" ] && [ ! -d /Applications/Ghostty.app ] && [ ! -d "$HOME/Applications/Ghostty.app" ]; then
  if command -v brew >/dev/null 2>&1; then
    say "Installing Ghostty (shows real photos)…"
    brew install --cask ghostty
  else
    say "Skipping Ghostty: Homebrew not found. Photos will use pixel blocks."
  fi
fi

mkdir -p "$PREFIX"
install -m 0755 bin/tuon "$PREFIX/tuon"
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
