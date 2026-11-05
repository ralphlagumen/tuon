package keystore

import (
	"bytes"
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"go.mau.fi/whatsmeow/proto/waAdv"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	waLog "go.mau.fi/whatsmeow/util/log"
	_ "modernc.org/sqlite"
)

func TestMoveKeysIntoEncryptedStore(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	// tuon.db as before: whatsmeow's tables in the plain database
	src, err := sql.Open("sqlite", "file:"+filepath.Join(dir, "tuon.db")+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	plain := sqlstore.NewWithDB(src, "sqlite3", waLog.Noop)
	if err := plain.Upgrade(ctx); err != nil {
		t.Fatal(err)
	}
	dev := plain.NewDevice()
	dev.ID = &types.JID{User: "100000179693157", Device: 52, Server: types.MessengerServer}
	dev.Account = &waAdv.ADVSignedDeviceIdentity{Details: []byte{}, // empty but not NULL, like a real Messenger device
		AccountSignature:    make([]byte, 64),
		AccountSignatureKey: make([]byte, 32), DeviceSignature: make([]byte, 64)}
	if err := plain.PutDevice(ctx, dev); err != nil {
		t.Fatal(err)
	}
	if err := dev.Sessions.PutSession(ctx, "100001279802241.90", []byte("session-record")); err != nil {
		t.Fatal(err)
	}

	key := bytes.Repeat([]byte{7}, 32)
	kdb, err := Open(dir, key)
	if err != nil {
		t.Fatal(err)
	}
	if err := Prepare(ctx, kdb); err != nil {
		t.Fatal(err)
	}
	n, err := Move(src, kdb)
	if err != nil || n == 0 {
		t.Fatalf("moved %d, %v", n, err)
	}
	if left, _ := Tables(src); len(left) != 0 {
		t.Fatalf("whatsmeow tables still in tuon.db: %v", left)
	}
	// whatsmeow finds the same device and session in keys.db
	enc := sqlstore.NewWithDB(kdb, "sqlite3", waLog.Noop)
	got, err := enc.GetFirstDevice(ctx)
	if err != nil || got.ID == nil || got.ID.Device != 52 || !bytes.Equal(got.IdentityKey.Priv[:], dev.IdentityKey.Priv[:]) {
		t.Fatalf("device after move: %+v %v", got.ID, err)
	}
	if rec, err := got.Sessions.GetSession(ctx, "100001279802241.90"); err != nil || string(rec) != "session-record" {
		t.Fatalf("session after move: %q %v", rec, err)
	}
	kdb.Close()

	// the file on disk is not a readable SQLite database
	head := make([]byte, 16)
	f, _ := os.Open(Path(dir))
	_, _ = f.Read(head)
	f.Close()
	if bytes.HasPrefix(head, []byte("SQLite format 3")) {
		t.Fatal("keys.db is plain SQLite")
	}
	raw, _ := os.ReadFile(Path(dir))
	if bytes.Contains(raw, []byte("session-record")) || bytes.Contains(raw, []byte("100001279802241")) {
		t.Fatal("keys.db leaks contents")
	}
	// and a wrong key can't open it
	if _, err := Open(dir, bytes.Repeat([]byte{8}, 32)); err == nil {
		t.Fatal("opened with the wrong key")
	}
	// running the move again is harmless
	kdb2, _ := Open(dir, key)
	defer kdb2.Close()
	if n, err := Move(src, kdb2); n != 0 || err != nil {
		t.Fatalf("second move: %d %v", n, err)
	}
}

func TestOpenWithAnyKey(t *testing.T) {
	// keys whose hex starts with a digit and has letters broke the DSN once
	for _, k := range [][]byte{bytes.Repeat([]byte{0x9c}, 32), bytes.Repeat([]byte{0xab}, 32), bytes.Repeat([]byte{0x07}, 32)} {
		dir := t.TempDir()
		db, err := Open(dir, k)
		if err != nil {
			t.Fatalf("key %x…: %v", k[:2], err)
		}
		db.Close()
		if _, err := Open(dir, bytes.Repeat([]byte{0x11}, 32)); err == nil {
			t.Fatal("wrong key opened it")
		} else if bytes.Contains([]byte(err.Error()), []byte("1111111111")) {
			t.Fatalf("error leaks the key: %v", err)
		}
	}
}
