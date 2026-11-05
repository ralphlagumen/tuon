// Package auth keeps the Messenger login (session cookies) safe on disk.
//
// The login lives in the system keychain (macOS Keychain, Windows
// Credential Manager, Linux Secret Service). Where no keychain is
// available it falls back to a file only you can read. Tuon never stores
// your password, only the session Meta hands back after you log in.
package auth

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/zalando/go-keyring"
)

const (
	service = "tuon"
	account = "messenger"
)

// Creds is a saved login: which Messenger flavor it came from, and the
// session cookies.
type Creds struct {
	Platform string            `json:"platform"` // "messenger" / "facebook" (web logins) or "messenger-lite" (app login)
	Cookies  map[string]string `json:"cookies"`
}

func (c Creds) Valid() bool {
	return c.Cookies["c_user"] != "" && c.Cookies["xs"] != ""
}

// keyring is swappable in tests.
var keyringGet, keyringSet, keyringDelete = keyring.Get, keyring.Set, keyring.Delete

func filePath(dir string) string   { return filepath.Join(dir, "login.json") }
func legacyPath(dir string) string { return filepath.Join(dir, "cookies.txt") }

// Load finds the saved login: keychain first, then the fallback file, then
// an old cookies.txt (moved into the keychain on the way).
func Load(dir string) (Creds, error) {
	if s, err := keyringGet(service, account); err == nil {
		var c Creds
		if json.Unmarshal([]byte(s), &c) == nil && c.Valid() {
			return c, nil
		}
	}
	if b, err := os.ReadFile(filePath(dir)); err == nil {
		var c Creds
		if json.Unmarshal(b, &c) == nil && c.Valid() {
			if Save(dir, c) == nil && where == "keychain" { // move it into the keychain
				_ = os.Remove(filePath(dir))
			}
			return c, nil
		}
	}
	if b, err := os.ReadFile(legacyPath(dir)); err == nil {
		c := Creds{Platform: "messenger", Cookies: ParseCookieLine(string(b))}
		if c.Valid() {
			if Save(dir, c) == nil {
				_ = os.Remove(legacyPath(dir))
			}
			return c, nil
		}
	}
	return Creds{}, errors.New("not logged in")
}

// where says where the last Save put the login ("keychain" or "file").
var where string

// Where reports where the login is kept.
func Where() string { return where }

// Save stores the login in the keychain, or the private file if there's
// no keychain.
func Save(dir string, c Creds) error {
	b, err := json.Marshal(c)
	if err != nil {
		return err
	}
	if err := keyringSet(service, account, string(b)); err == nil {
		where = "keychain"
		_ = os.Remove(filePath(dir))
		return nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	where = "file"
	return os.WriteFile(filePath(dir), b, 0o600)
}

// Delete forgets the login everywhere.
func Delete(dir string) error {
	err := keyringDelete(service, account)
	if err != nil && !errors.Is(err, keyring.ErrNotFound) {
		err = fmt.Errorf("keychain: %w", err)
	} else {
		err = nil
	}
	for _, p := range []string{filePath(dir), legacyPath(dir)} {
		if e := os.Remove(p); e != nil && !os.IsNotExist(e) {
			err = e
		}
	}
	return err
}

// ParseCookieLine reads "datr=…; c_user=…; xs=…".
func ParseCookieLine(s string) map[string]string {
	out := map[string]string{}
	for _, part := range strings.Split(strings.TrimSpace(s), ";") {
		k, v, ok := strings.Cut(strings.TrimSpace(part), "=")
		if ok && k != "" {
			out[k] = v
		}
	}
	return out
}
