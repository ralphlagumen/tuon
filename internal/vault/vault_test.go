package vault

import (
	"crypto/ecdh"
	"crypto/rand"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// fakeEnclave does the key agreement in software, to test the wrapping.
type fakeEnclave struct {
	priv    *ecdh.PrivateKey
	confirm bool
}

func (f *fakeEnclave) Create() ([]byte, []byte, error) {
	f.priv, _ = ecdh.P256().GenerateKey(rand.Reader)
	return []byte("opaque-chip-blob"), f.priv.PublicKey().Bytes(), nil
}

func (f *fakeEnclave) Derive(key, peer []byte) ([]byte, error) {
	if !f.confirm {
		return nil, ErrNotConfirmed
	}
	p, err := ecdh.P256().NewPublicKey(peer)
	if err != nil {
		return nil, err
	}
	return f.priv.ECDH(p)
}

func TestEnclaveVault(t *testing.T) {
	dir := t.TempDir()
	se := &fakeEnclave{confirm: true}
	v, err := CreateEnclave(dir, se)
	if err != nil {
		t.Fatal(err)
	}
	sealed := v.SealString("meet at 7")
	if !Sealed(sealed) || strings.Contains(sealed, "meet") {
		t.Fatalf("not sealed: %q", sealed)
	}
	again, err := UnlockEnclave(dir, se)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := again.OpenString(sealed); err != nil || got != "meet at 7" {
		t.Fatalf("open = %q, %v", got, err)
	}
	se.confirm = false
	if _, err := UnlockEnclave(dir, se); err != ErrNotConfirmed {
		t.Fatalf("cancelled Touch ID should fail, got %v", err)
	}
	// the vault file alone holds nothing readable
	b, _ := os.ReadFile(filepath.Join(dir, "vault.json"))
	if strings.Contains(string(b), "meet") {
		t.Fatal("plaintext in vault file")
	}
}

func TestPassphraseVault(t *testing.T) {
	dir := t.TempDir()
	if _, err := CreatePassphrase(dir, "short"); err == nil {
		t.Fatal("short passphrase accepted")
	}
	v, err := CreatePassphrase(dir, "correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	s := v.SealString("secret")
	if _, err := UnlockPassphrase(dir, "wrong passphrase!"); err != ErrWrongPassphrase {
		t.Fatalf("wrong passphrase: %v", err)
	}
	again, err := UnlockPassphrase(dir, "correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := again.OpenString(s); got != "secret" {
		t.Fatalf("got %q", got)
	}
	if !v.CheckPassphrase("correct horse battery") || v.CheckPassphrase("nope nope nope") {
		t.Fatal("CheckPassphrase wrong")
	}
	if got, _ := v.OpenString("plain old text"); got != "plain old text" {
		t.Fatal("unsealed text should pass through")
	}
}

func TestTamperedDataFails(t *testing.T) {
	v, _ := CreatePassphrase(t.TempDir(), "correct horse battery")
	s := []byte(v.SealString("hello"))
	s[len(s)-3] ^= 1
	if _, err := v.OpenString(string(s)); err == nil {
		t.Fatal("tampered data opened")
	}
}

func TestHandoffAdopt(t *testing.T) {
	dir := t.TempDir()
	v, err := CreatePassphrase(dir, "correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	s := v.SealString("hello")
	child, err := Adopt(dir, v.Handoff())
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := child.OpenString(s); got != "hello" || child.Kind() != KindPassphrase {
		t.Fatalf("adopted vault can't read: %q", got)
	}
	if string(child.DeriveKey("keys.db/2")) != string(v.DeriveKey("keys.db/2")) {
		t.Fatal("derived keys differ")
	}
	if _, err := Adopt(dir, []byte("short")); err == nil {
		t.Fatal("bad key size accepted")
	}
}

func TestFindHelperOnlyNextToTuon(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS only")
	}
	exe, _ := os.Executable()
	exe, _ = filepath.EvalSymlinks(exe)
	next := filepath.Join(filepath.Dir(exe), "tuon-keyhelper")
	defer os.Remove(next)

	// one on PATH only: not used
	onPath := t.TempDir()
	if err := os.WriteFile(filepath.Join(onPath, "tuon-keyhelper"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", onPath)
	if h := FindHelper(); h != nil {
		t.Fatalf("used a helper from PATH: %s", h.Path)
	}
	// next to tuon but writable by others: not used
	if err := os.WriteFile(next, []byte("#!/bin/sh\n"), 0o777); err != nil {
		t.Fatal(err)
	}
	_ = os.Chmod(next, 0o777)
	if h := FindHelper(); h != nil {
		t.Fatal("used a helper others can change")
	}
	_ = os.Chmod(next, 0o755)
	if h := FindHelper(); h == nil || h.Path != next {
		t.Fatalf("didn't find the helper next to tuon: %v", h)
	}
}

// R12: the lock screen falls back to the passphrase check when the Touch ID
// helper is missing; on a Touch ID vault that must refuse, not crash.
func TestEnclavePassphraseCheckRefuses(t *testing.T) {
	v, err := CreateEnclave(t.TempDir(), &fakeEnclave{confirm: true})
	if err != nil {
		t.Fatal(err)
	}
	if v.CheckPassphrase("") || v.CheckPassphrase("anything") {
		t.Fatal("a Touch ID vault accepted a passphrase")
	}
}

// TestPlantedArgonSettings: vault.json is writable by the sandboxed app; huge
// or zero Argon2 settings must be refused, not crash the launcher.
func TestPlantedArgonSettings(t *testing.T) {
	for name, edit := range map[string]func(m map[string]any){
		"4 TiB":     func(m map[string]any) { m["memory"] = 4294967295 },
		"0 threads": func(m map[string]any) { delete(m, "threads") },
		"0 time":    func(m map[string]any) { delete(m, "time") },
		"no salt":   func(m map[string]any) { m["salt"] = "" },
	} {
		dir := t.TempDir()
		if _, err := CreatePassphrase(dir, "correct horse battery"); err != nil {
			t.Fatal(err)
		}
		p := filepath.Join(dir, "vault.json")
		b, _ := os.ReadFile(p)
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		edit(m)
		b, _ = json.Marshal(m)
		_ = os.WriteFile(p, b, 0o600)
		if _, err := UnlockPassphrase(dir, "correct horse battery"); !errors.Is(err, ErrBadSettings) {
			t.Errorf("%s: got %v", name, err)
		}
	}
}
