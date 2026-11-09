package auth

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zalando/go-keyring"
)

func fakeKeychain(t *testing.T, works bool) map[string]string {
	t.Helper()
	store := map[string]string{}
	keyringGet = func(s, a string) (string, error) {
		if v, ok := store[s+a]; ok {
			return v, nil
		}
		return "", keyring.ErrNotFound
	}
	keyringSet = func(s, a, v string) error {
		if !works {
			return errors.New("no keychain")
		}
		store[s+a] = v
		return nil
	}
	keyringDelete = func(s, a string) error { delete(store, s+a); return nil }
	t.Cleanup(func() { keyringGet, keyringSet, keyringDelete = keyring.Get, keyring.Set, keyring.Delete })
	return store
}

func TestOldCookieFileMovesToKeychain(t *testing.T) {
	store := fakeKeychain(t, true)
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "cookies.txt"), []byte("datr=a; c_user=1; xs=2\n"), 0o600)
	c, err := Load(dir)
	if err != nil || c.Cookies["c_user"] != "1" {
		t.Fatalf("load: %v %+v", err, c)
	}
	if _, err := os.Stat(filepath.Join(dir, "cookies.txt")); !os.IsNotExist(err) {
		t.Fatal("plain cookie file should be gone after moving to the keychain")
	}
	if len(store) != 1 {
		t.Fatal("login should be in the keychain")
	}
	if err := Delete(dir); err != nil || len(store) != 0 {
		t.Fatalf("logout should forget it: %v", err)
	}
	if _, err := Load(dir); err == nil {
		t.Fatal("should be logged out")
	}
}

func TestFallbackFileIsPrivate(t *testing.T) {
	fakeKeychain(t, false)
	dir := t.TempDir()
	if err := Save(dir, Creds{Platform: "messenger", Cookies: map[string]string{"c_user": "1", "xs": "2"}}); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(filepath.Join(dir, "login.json"))
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("fallback file must be owner-only, got %v %v", st.Mode(), err)
	}
	if c, err := Load(dir); err != nil || c.Cookies["xs"] != "2" {
		t.Fatalf("load from file: %v", err)
	}
}

type xorSealer struct{}

func (xorSealer) Seal(b []byte) []byte {
	out := make([]byte, len(b))
	for i := range b {
		out[i] = b[i] ^ 0x5a
	}
	return out
}
func (s xorSealer) Open(b []byte) ([]byte, error) { return s.Seal(b), nil }

func TestSessionMovesIntoVault(t *testing.T) {
	dir := t.TempDir()
	store := map[string]string{}
	keyringGet = func(s, a string) (string, error) {
		if v, ok := store[s+a]; ok {
			return v, nil
		}
		return "", keyring.ErrNotFound
	}
	keyringSet = func(s, a, v string) error { store[s+a] = v; return nil }
	keyringDelete = func(s, a string) error { delete(store, s+a); return nil }
	defer func() { keyringGet, keyringSet, keyringDelete = keyring.Get, keyring.Set, keyring.Delete }()

	old := Creds{Platform: "facebook", Cookies: map[string]string{"c_user": "1", "xs": "secret"}}
	if err := Save(dir, old); err != nil {
		t.Fatal(err)
	}
	c, err := LoadSealed(dir, xorSealer{}, true)
	if err != nil || c.Cookies["xs"] != "secret" {
		t.Fatalf("migrate: %+v %v", c, err)
	}
	if len(store) != 0 {
		t.Fatal("session still in the keychain after moving into the vault")
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "login.enc"))
	if strings.Contains(string(raw), "secret") {
		t.Fatal("login.enc holds the session in the clear")
	}
	if again, err := LoadSealed(dir, xorSealer{}, false); err != nil || again.Cookies["xs"] != "secret" {
		t.Fatalf("reload: %v", err)
	}
	if err := Delete(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "login.enc")); !os.IsNotExist(err) {
		t.Fatal("logout left login.enc")
	}
}

// R14: once migrated, a plain session planted in the old places is ignored
// and removed, never loaded.
func TestPlantedPlainSessionIgnored(t *testing.T) {
	dir := t.TempDir()
	store := map[string]string{}
	keyringGet = func(s, a string) (string, error) {
		if v, ok := store[s+a]; ok {
			return v, nil
		}
		return "", keyring.ErrNotFound
	}
	keyringSet = func(s, a, v string) error { store[s+a] = v; return nil }
	keyringDelete = func(s, a string) error { delete(store, s+a); return nil }
	defer func() { keyringGet, keyringSet, keyringDelete = keyring.Get, keyring.Set, keyring.Delete }()

	planted := Creds{Platform: "messenger", Cookies: map[string]string{"c_user": "666", "xs": "attacker"}}
	if err := Save(dir, planted); err != nil {
		t.Fatal(err)
	}
	if c, err := LoadSealed(dir, xorSealer{}, false); err == nil {
		t.Fatalf("loaded a planted session: %+v", c)
	}
	mine := Creds{Platform: "messenger", Cookies: map[string]string{"c_user": "1", "xs": "mine"}}
	if err := SaveSealed(dir, xorSealer{}, mine); err != nil {
		t.Fatal(err)
	}
	if err := Save(dir, planted); err != nil { // planted again, next to a real login.enc
		t.Fatal(err)
	}
	c, err := LoadSealed(dir, xorSealer{}, false)
	if err != nil || c.Cookies["xs"] != "mine" {
		t.Fatalf("got %+v %v", c, err)
	}
	if len(store) != 0 {
		t.Fatal("the planted copy wasn't removed")
	}
}

func TestDeleteIgnoresKeychainErrors(t *testing.T) {
	fakeKeychain(t, true)
	keyringDelete = func(string, string) error { return errors.New("fork/exec /usr/bin/security: operation not permitted") }
	dir := t.TempDir()
	sealed := filepath.Join(dir, "login.enc")
	_ = os.WriteFile(sealed, []byte("x"), 0o600)
	if err := Delete(dir); err != nil {
		t.Fatalf("a keychain error stopped the logout: %v", err)
	}
	if _, err := os.Stat(sealed); !os.IsNotExist(err) {
		t.Fatal("login.enc kept")
	}
}
