package main

import (
	"context"
	"testing"

	"github.com/ralph/tuon/internal/keystore"
	"github.com/ralph/tuon/internal/store"
	"github.com/ralph/tuon/internal/vault"
	"go.mau.fi/whatsmeow/proto/waAdv"
	"go.mau.fi/whatsmeow/store/sqlstore"
	waTypes "go.mau.fi/whatsmeow/types"
	waLog "go.mau.fi/whatsmeow/util/log"
)

func TestNewDeviceOnlyAfterRelink(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	v, err := vault.CreatePassphrase(store.Dir(), "correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	vlt = v
	defer func() { vlt = nil }()
	st, err := openStore()
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	_ = st.SetSetting(store.SettingAccount, "100000179693157") // an account that had keys

	kdb, allow, err := useVault(st)
	if err != nil {
		t.Fatal(err)
	}
	if allow {
		t.Fatal("keys gone without a relink: must not register a new device")
	}
	kdb.Close()

	_ = st.SetSetting(settingRelink, "1")
	kdb, allow, _ = useVault(st)
	if !allow {
		t.Fatal("after relink a new device should be allowed")
	}
	// the connector registers it...
	c := sqlstore.NewWithDB(kdb, "sqlite3", waLog.Noop)
	dev := c.NewDevice()
	dev.ID = &waTypes.JID{User: "100000179693157", Device: 53, Server: waTypes.MessengerServer}
	dev.Account = &waAdv.ADVSignedDeviceIdentity{Details: []byte{}, AccountSignature: make([]byte, 64), AccountSignatureKey: make([]byte, 32), DeviceSignature: make([]byte, 64)}
	if err := c.PutDevice(context.Background(), dev); err != nil {
		t.Fatal(err)
	}
	kdb.Close()
	// ...and the next start closes the door again
	kdb, allow, _ = useVault(st)
	defer kdb.Close()
	if allow || st.Setting(settingRelink, "") != "" || !keystore.HasDevice(kdb) {
		t.Fatalf("after registering: allow=%v relink=%q", allow, st.Setting(settingRelink, ""))
	}
}
