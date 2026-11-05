package messenger

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/rs/zerolog"
	waTypes "go.mau.fi/whatsmeow/types"
)

// TUON_LIVE=1 go test ./internal/connector/messenger -run TestLiveKeys -v
// Read-only: asks Messenger's server for the key bundle it hands to people
// who message this device, and checks each key against tuon.db. Prints
// only IDs and match/mismatch, never key material. tuon must be closed.
func TestLiveKeys(t *testing.T) {
	if os.Getenv("TUON_LIVE") == "" {
		t.Skip("set TUON_LIVE=1")
	}
	st, kdb, creds, release := liveSetup(t)
	defer release()
	c := New(creds, st.DB, zerolog.New(zerolog.NewTestWriter(t)).Level(zerolog.ErrorLevel))
	c.SetKeyDB(kdb) // never AllowNewDevice: a diagnostic must not register anything
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
	var dev = c.wadev
	for i := 0; i < 120; i++ {
		c.mu.Lock()
		wa := c.wa
		dev = c.wadev
		c.mu.Unlock()
		if wa != nil {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	c.mu.Lock()
	wa := c.wa
	c.mu.Unlock()
	if wa == nil || dev == nil || dev.ID == nil {
		t.Fatal("encrypted connection didn't come up")
	}
	me := *dev.ID
	t.Logf("our device: %s", me.String())

	// every device on the account, as Meta's server lists them
	if devs, err := wa.GetUserDevicesContext(ctx, []waTypes.JID{me.ToNonAD()}); err == nil {
		var nums []uint16
		for _, d := range devs {
			nums = append(nums, d.Device)
		}
		t.Logf("devices on the account (server): %v", nums)
	} else {
		t.Logf("device list: %v", err)
	}
	for round := 2; round <= 4; round++ { // a few more bundles: each hands out a different one-time key
		b := wa.DangerousInternals().FetchPreKeysNoError(ctx, []waTypes.JID{me})[me]
		if b == nil || b.PreKeyID() == nil || b.PreKeyID().IsEmpty {
			t.Logf("bundle %d: no one-time key", round)
			continue
		}
		local, err := dev.PreKeys.GetPreKey(ctx, b.PreKeyID().Value)
		t.Logf("bundle %d: one-time key %d match=%v (local err %v), signed key %d match=%v", round, b.PreKeyID().Value,
			err == nil && local != nil && b.PreKey().PublicKey() == *local.Pub, err,
			b.SignedPreKeyID(), b.SignedPreKey().PublicKey() == *dev.SignedPreKey.Pub)
	}
	bundles := wa.DangerousInternals().FetchPreKeysNoError(ctx, []waTypes.JID{me})
	b := bundles[me]
	if b == nil {
		t.Fatalf("server returned no bundle for %s (got %d)", me, len(bundles))
	}
	idOK := b.IdentityKey().PublicKey().PublicKey() == *dev.IdentityKey.Pub
	spkOK := b.SignedPreKeyID() == dev.SignedPreKey.KeyID && b.SignedPreKey().PublicKey() == *dev.SignedPreKey.Pub
	t.Logf("registration id: server=%d local=%d match=%v", b.RegistrationID(), dev.RegistrationID, b.RegistrationID() == dev.RegistrationID)
	t.Logf("identity key match=%v", idOK)
	t.Logf("signed prekey: server id=%d local id=%d match=%v", b.SignedPreKeyID(), dev.SignedPreKey.KeyID, spkOK)
	if id := b.PreKeyID(); id != nil && !id.IsEmpty {
		local, err := dev.PreKeys.GetPreKey(ctx, id.Value)
		switch {
		case err != nil:
			t.Logf("one-time prekey %d: local lookup error: %v", id.Value, err)
		case local == nil:
			t.Logf("one-time prekey %d: MISSING locally", id.Value)
		default:
			t.Logf("one-time prekey %d: match=%v", id.Value, b.PreKey().PublicKey() == *local.Pub)
		}
	} else {
		t.Log("one-time prekey: none offered")
	}
}
