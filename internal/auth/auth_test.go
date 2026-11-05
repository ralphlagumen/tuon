package auth

import (
	"errors"
	"os"
	"path/filepath"
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
