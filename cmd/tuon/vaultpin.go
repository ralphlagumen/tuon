package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/ralph/tuon/internal/paths"
	"github.com/ralph/tuon/internal/safefile"
)

// The vault pin: a fingerprint of vault.json that only the launcher keeps,
// in ~/Library/Application Support/tuon, which the sandboxed app can
// neither read nor write. The app can write tuon's folder and holds the
// key while it runs, so a bad library could rewrite vault.json (say, as a
// passphrase vault with a passphrase it chose): your data would then open
// without Touch ID, and stay that way after the library is gone. The
// launcher pins what it created (or, the first time, what it found) and
// refuses a vault.json that changed behind its back.

// pinPath: "" where there's no sandbox (the app could write anywhere).
func pinPath() string {
	var base string
	switch runtime.GOOS {
	case "darwin":
		base, _ = os.UserConfigDir() // ~/Library/Application Support
	case "linux": // ~/.local/share: outside what the sandboxed app may touch
		base = os.Getenv("XDG_DATA_HOME")
		if !filepath.IsAbs(base) {
			home, _ := os.UserHomeDir()
			base = filepath.Join(home, ".local", "share")
		}
	}
	if base == "" || !filepath.IsAbs(base) {
		return ""
	}
	return filepath.Join(base, "tuon", "vault-pin")
}

func vaultFingerprint(dir string) ([]byte, error) {
	b, err := os.ReadFile(filepath.Join(dir, "vault.json"))
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(b)
	return []byte(hex.EncodeToString(sum[:])), nil
}

// pinVault records vault.json as it is now (after the launcher made it).
func pinVault(dir string) error {
	p := pinPath()
	if p == "" {
		return nil
	}
	fp, err := vaultFingerprint(dir)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	return safefile.WriteFile(p, append(fp, '\n'), 0o600)
}

var errVaultChanged = errors.New("tuon's lock file (vault.json) changed, and tuon didn't change it.")

// vaultChanged says what happened and what to do, with this system's paths.
func vaultChanged() error {
	how := "a passphrase you don't know"
	if runtime.GOOS == "darwin" {
		how = "a passphrase instead of Touch ID"
	}
	return fmt.Errorf(`%w
  The chat app can write tuon's folder, so a misbehaving library could have rewritten it,
  for example to open your data with %s. tuon won't use it.
  If you didn't reset tuon yourself: check %s, then start over with
  'tuon logout --wipe' (your chats come back with your Messenger PIN).
  If you did (restored a backup, copied tuon's folder): delete %s`,
		errVaultChanged, how, filepath.Join(paths.WorkBase(), "net.log"), pinPath())
}

// checkVaultPin refuses a vault.json that changed since the launcher
// pinned it. The first time (no pin yet), it pins what's there.
func checkVaultPin(dir string) error {
	p := pinPath()
	if p == "" {
		return nil
	}
	fp, err := vaultFingerprint(dir)
	if err != nil {
		return err
	}
	want, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		if err := pinVault(dir); err != nil {
			fmt.Fprintln(os.Stderr, "tuon: couldn't record the lock's fingerprint:", err)
		}
		return nil
	}
	if err != nil {
		return err
	}
	if !bytes.Equal(bytes.TrimSpace(want), fp) {
		return vaultChanged()
	}
	return nil
}
