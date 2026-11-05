package main

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/ralph/tuon/internal/keystore"
	"github.com/ralph/tuon/internal/store"
)

// settingRelink lets the next start register tuon as a new linked device,
// which the guard otherwise refuses once keys have existed.
const settingRelink = "relink"

// relink drops this device's encryption keys so tuon registers again as a
// brand-new linked device. Contacts' phones then fetch fresh keys, which
// fixes chats where someone's phone kept outdated keys for tuon.
func relink() error {
	release, err := store.Lock()
	if err != nil {
		return err
	}
	defer release()
	fmt.Print(`
  Re-link tuon as a new device

  Tuon forgets its encryption keys and registers as a new linked device on
  its next start. Your chats, messages and folders stay. Afterwards:
    - Messenger may show a new login;
    - remove the old "tuon" device in Messenger's settings;
    - run ` + "`tuon history`" + ` once with your PIN to load older encrypted messages.

  Type relink to continue: `)
	if strings.TrimSpace(readLine()) != "relink" {
		return errors.New("cancelled")
	}
	st, err := store.Open()
	if err != nil {
		return err
	}
	defer st.Close()
	if _, err := openVault(); err != nil {
		return err
	}
	dir := store.Dir()
	for _, f := range []string{"keys.db", "keys.db-wal", "keys.db-shm"} {
		if err := os.Remove(dir + "/" + f); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	// whatsmeow tables left in tuon.db by an older version, and the backup
	// enrollment (tied to the old device's identity key)
	tables, _ := keystore.Tables(st.DB)
	for _, t := range append(tables, "tuon_history_device") {
		if _, err := st.DB.Exec(`DROP TABLE IF EXISTS "` + t + `"`); err != nil {
			return err
		}
	}
	if err := st.SetSetting(settingRelink, "1"); err != nil {
		return err
	}
	_, _ = st.DB.Exec(`VACUUM`)
	fmt.Println("  Done. Start tuon to register the new device, then run `tuon history`.")
	return nil
}
