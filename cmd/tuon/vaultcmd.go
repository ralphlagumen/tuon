package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"golang.org/x/term"

	"github.com/ralph/tuon/internal/auth"
	"github.com/ralph/tuon/internal/keystore"
	"github.com/ralph/tuon/internal/store"
	"github.com/ralph/tuon/internal/vault"
)

// vlt is the unlocked vault for this run (nil until openVault).
var vlt *vault.Vault

// openVault unlocks tuon's vault, creating it the first time: Touch ID (or
// the Mac password) where the Secure Enclave helper is installed, a
// passphrase elsewhere.
func openVault() (*vault.Vault, error) {
	if vlt != nil {
		return vlt, nil
	}
	dir := store.Dir()
	var err error
	switch {
	case vault.Exists(dir) && vault.Kind(dir) == vault.KindEnclave:
		h := vault.FindHelper()
		if h == nil {
			return nil, errors.New("tuon-keyhelper is missing, so the Touch ID lock can't open: run ./install.sh again")
		}
		fmt.Println("  Unlock tuon with Touch ID or your Mac password…")
		vlt, err = vault.UnlockEnclave(dir, h, "unlock your chats")
		if errors.Is(err, vault.ErrNotConfirmed) {
			return nil, errors.New("not unlocked")
		}
	case vault.Exists(dir):
		vlt, err = unlockPassphrase(dir)
	default:
		vlt, err = createVault(dir)
	}
	return vlt, err
}

func createVault(dir string) (*vault.Vault, error) {
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return nil, errors.New("tuon needs to set up its lock: run `tuon` in a terminal")
	}
	if h := vault.FindHelper(); h != nil {
		fmt.Print(`
  tuon now locks your chats. Your session, messages and keys are encrypted
  with a key kept in this Mac's Secure Enclave, and opening tuon asks for
  Touch ID or your Mac password. Other apps can't read them anymore.
`)
		v, err := vault.CreateEnclave(dir, h)
		if err == nil {
			fmt.Println("  Lock set up.")
			return v, nil
		}
		fmt.Fprintln(os.Stderr, "  Touch ID lock unavailable:", err, "- using a passphrase instead.")
	}
	fmt.Print(`
  Choose a passphrase for tuon. It encrypts your session, messages and keys
  on this computer, and you'll type it when tuon opens. If you forget it,
  you log in again and restore your history with your Messenger PIN.
`)
	for {
		fmt.Print("  passphrase (8+ characters): ")
		p1, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Println()
		if err != nil {
			return nil, err
		}
		fmt.Print("  again: ")
		p2, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Println()
		if err != nil {
			return nil, err
		}
		if string(p1) != string(p2) {
			fmt.Println("  They don't match. Try again.")
			continue
		}
		v, err := vault.CreatePassphrase(dir, string(p1))
		if err != nil {
			fmt.Println(" ", err)
			continue
		}
		return v, nil
	}
}

// unlockPassphrase allows 5 tries, waiting longer after each wrong one.
func unlockPassphrase(dir string) (*vault.Vault, error) {
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return nil, errors.New("tuon is locked: run it in a terminal to type your passphrase")
	}
	for try := 1; try <= 5; try++ {
		fmt.Print("  tuon passphrase: ")
		p, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Println()
		if err != nil {
			return nil, err
		}
		v, err := vault.UnlockPassphrase(dir, strings.TrimRight(string(p), "\r\n"))
		if err == nil {
			return v, nil
		}
		if try < 5 {
			wait := time.Duration(1<<(try-1)) * time.Second
			fmt.Printf("  Wrong passphrase. Try again in %s.\n", wait)
			time.Sleep(wait)
		}
	}
	return nil, errors.New("too many wrong passphrases")
}

// loggedIn checks for a saved session (unlocking the vault if needed).
func loggedIn() (bool, error) {
	v, err := openVault()
	if err != nil {
		return false, err
	}
	if _, err := auth.LoadSealed(store.Dir(), v); err != nil {
		return false, err
	}
	return true, nil
}

// useVault turns on encryption in the store (unlocking first if the vault
// exists but this run got its login another way), seals anything still
// stored in the clear, and opens keys.db, moving the encrypted-chat keys
// into it the first time. It returns keys.db (nil without a vault) and
// whether registering a brand-new linked device is allowed.
func useVault(st *store.Store) (*sql.DB, bool, error) {
	dir := store.Dir()
	fresh := st.Account() == "" && !keystore.Exists(dir) && !keystore.HasDevice(st.DB)
	if vlt == nil && !vault.Exists(dir) {
		return nil, fresh, nil
	}
	v, err := openVault()
	if err != nil {
		return nil, false, err
	}
	st.SetCipher(v)
	if n, err := st.EncryptAll(); err != nil {
		return nil, false, fmt.Errorf("encrypting stored messages: %w", err)
	} else if n > 0 {
		fmt.Printf("  Encrypted %d stored items.\n", n)
	}
	// "/2": the first key was printed by a bug (2026-11-05), so it's retired
	kdb, err := keystore.Open(dir, v.DeriveKey("keys.db/2"))
	if err != nil {
		return nil, false, err
	}
	if err := keystore.Prepare(context.Background(), kdb); err != nil {
		kdb.Close()
		return nil, false, fmt.Errorf("preparing keys.db: %w", err)
	}
	if n, err := keystore.Move(st.DB, kdb); err != nil {
		kdb.Close()
		return nil, false, fmt.Errorf("moving encryption keys into keys.db: %w", err)
	} else if n > 0 {
		fmt.Printf("  Moved %d encryption keys into the encrypted key store.\n", n)
	}
	return kdb, fresh && !keystore.HasDevice(kdb), nil
}
