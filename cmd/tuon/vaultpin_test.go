package main

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// TestVaultPin: the first unlock pins vault.json; a rewrite (say, a bad
// library downgrading it to a passphrase it chose) is refused after that.
func TestVaultPin(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("pinned only where there's a sandbox")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".config", "tuon")
	_ = os.MkdirAll(dir, 0o700)
	vj := filepath.Join(dir, "vault.json")
	_ = os.WriteFile(vj, []byte(`{"kind":"enclave","wrapped":"AAAA"}`), 0o600)

	if err := checkVaultPin(dir); err != nil { // first time: pins
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, "Library", "Application Support", "tuon", "vault-pin")); err != nil {
		t.Fatal("no pin written:", err)
	}
	if err := checkVaultPin(dir); err != nil { // unchanged: fine
		t.Fatal(err)
	}
	_ = os.WriteFile(vj, []byte(`{"kind":"passphrase","salt":"evil"}`), 0o600) // rewritten behind the launcher's back
	if err := checkVaultPin(dir); !errors.Is(err, errVaultChanged) {
		t.Fatalf("a rewritten vault.json must be refused, got %v", err)
	}
	// the launcher itself re-creating the vault (after logout --wipe) pins anew
	if err := pinVault(dir); err != nil {
		t.Fatal(err)
	}
	if err := checkVaultPin(dir); err != nil {
		t.Fatal(err)
	}
}
