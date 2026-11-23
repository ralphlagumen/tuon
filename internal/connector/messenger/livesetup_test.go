package messenger

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/ralph/tuon/internal/auth"
	"github.com/ralph/tuon/internal/keystore"
	"github.com/ralph/tuon/internal/store"
	"github.com/ralph/tuon/internal/vault"
)

// liveSetup opens tuon's real data the way the app does: the vault (one
// Touch ID), the sealed session, the store with its cipher and keys.db.
// tuon must be closed (the lock is held here).
func liveSetup(t *testing.T) (st *store.Store, kdb *sql.DB, creds auth.Creds, release func()) {
	t.Helper()
	release, err := store.Lock()
	if err != nil {
		t.Skip(err)
	}
	dir := store.Dir()
	var v *vault.Vault
	if vault.Kind(dir) == vault.KindEnclave {
		h := liveHelper()
		if h == nil {
			t.Fatal("tuon-keyhelper not found")
		}
		v, err = vault.UnlockEnclave(dir, h)
	} else {
		t.Skip("passphrase vaults aren't supported in live tests")
	}
	if err != nil {
		release()
		t.Fatal(err)
	}
	if st, err = store.Open(v.DeriveKey("tuon.db/1")); err != nil {
		release()
		t.Fatal(err)
	}
	st.SetCipher(v)
	if creds, err = auth.LoadSealed(dir, v, false); err != nil {
		t.Fatal(err)
	}
	if kdb, err = keystore.Open(dir, v.DeriveKey("keys.db/2")); err != nil {
		t.Fatal(err)
	}
	unlock := release // not the named result: that would make the closure call itself
	return st, kdb, creds, func() { kdb.Close(); st.Close(); unlock() }
}

// liveHelper: the Touch ID helper for live tests. A test binary runs from
// Go's build folder, never next to the helper, so fall back to the
// installed one (~/.local/bin, from install.sh), if only you can change it.
func liveHelper() *vault.Helper {
	if h := vault.FindHelper(); h != nil {
		return h
	}
	home, _ := os.UserHomeDir()
	p := filepath.Join(home, ".local", "bin", "tuon-keyhelper")
	if st, err := os.Stat(p); err == nil && st.Mode().IsRegular() && st.Mode().Perm()&0o022 == 0 {
		return &vault.Helper{Path: p}
	}
	return nil
}
