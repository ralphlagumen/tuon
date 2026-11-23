package messenger

import (
	"context"
	"encoding/hex"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"go.mau.fi/whatsmeow/store/sqlstore"
	waLog "go.mau.fi/whatsmeow/util/log"
)

// go run ./scripts/devsandbox --live -- go test ./internal/connector/messenger -run TestLiveContactKeys -v
// with TUON_CONTACT=<part of a name> (or * for every encrypted 1:1). Offline and read-only: prints the
// identity keys keys.db holds for that contact's devices (public keys) and
// ours, in a few formats, to compare with Messenger's "verify keys" screen
// (MESSENGER-MITM M4.0). Never connects. tuon must be closed.
func TestLiveContactKeys(t *testing.T) {
	name := os.Getenv("TUON_CONTACT")
	if os.Getenv("TUON_LIVE") == "" || name == "" {
		t.Skip("set TUON_LIVE=1 and TUON_CONTACT")
	}
	st, kdb, _, release := liveSetup(t)
	defer release()
	ctx := context.Background()
	dev, err := sqlstore.NewWithDB(kdb, "sqlite3", waLog.Noop).GetFirstDevice(ctx)
	if err != nil || dev == nil || dev.ID == nil {
		t.Fatalf("no encrypted-chat device: %v", err)
	}
	t.Logf("we are %s; our identity key:", dev.ID)
	showKey(t, dev.IdentityKey.Pub[:])
	rows, err := st.DB.Query(`SELECT id FROM tuon_chats WHERE encrypted = 1 AND grp = 0 ORDER BY last_ts DESC`)
	if err != nil {
		t.Fatal(err)
	}
	var all []string
	for rows.Next() {
		var id string
		_ = rows.Scan(&id)
		all = append(all, id)
	}
	rows.Close()
	var ids []string
	for _, id := range all {
		c, err := st.Chat(id) // names are sealed in tuon.db: read them the store's way
		if err != nil {
			continue
		}
		if name != "*" && !strings.Contains(strings.ToLower(c.Display), strings.ToLower(name)) {
			continue
		}
		t.Logf("chat %s %q", id, c.Display)
		ids = append(ids, id)
	}
	for _, id := range ids {
		r, err := kdb.Query(`SELECT their_id, identity FROM whatsmeow_identity_keys WHERE their_id LIKE ? OR their_id LIKE ? ESCAPE '\'`, id+":%", id+"\\_%")
		if err != nil {
			t.Fatal(err)
		}
		n := 0
		for r.Next() {
			var who string
			var key []byte
			_ = r.Scan(&who, &key)
			t.Logf("%s: device %s", id, who)
			showKey(t, key)
			n++
		}
		r.Close()
		t.Logf("%s: %d device keys in keys.db", id, n)
	}
}

func showKey(t *testing.T, k []byte) {
	h := hex.EncodeToString(k)
	var g []string
	for i := 0; i < len(h); i += 4 {
		g = append(g, h[i:min(i+4, len(h))])
	}
	t.Logf("   hex     %s", strings.Join(g, " "))
	t.Logf("   with 05 05%s", h)
}

// TUON_CHATS=<id,id>: ContactKeys through a live connection (the device
// list from Messenger's server), what the :keys panel shows. Read-only.
func TestLiveContactKeysConn(t *testing.T) {
	if os.Getenv("TUON_LIVE") == "" || os.Getenv("TUON_CHATS") == "" {
		t.Skip("set TUON_LIVE=1 and TUON_CHATS")
	}
	st, kdb, creds, release := liveSetup(t)
	defer release()
	c := New(creds, st.DB, zerolog.New(zerolog.NewTestWriter(t)).Level(zerolog.ErrorLevel))
	c.SetKeyDB(kdb) // never AllowNewDevice
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	if err := c.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	go func() {
		for range c.Events() {
		}
	}()
	for i := 0; i < 120; i++ {
		c.mu.Lock()
		up := c.wa != nil && c.wa.IsConnected()
		c.mu.Unlock()
		if up {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	for _, id := range strings.Split(os.Getenv("TUON_CHATS"), ",") {
		set, err := c.ContactKeys(ctx, id)
		if err != nil {
			t.Fatalf("%s: %v", id, err)
		}
		for _, k := range set.Theirs {
			t.Logf("%s theirs device %d key %x…", id, k.Device, k.Key[:min(4, len(k.Key))])
		}
		for _, k := range set.Mine {
			t.Logf("%s mine device %d this=%v key %x…", id, k.Device, k.This, k.Key[:min(4, len(k.Key))])
		}
	}
}
