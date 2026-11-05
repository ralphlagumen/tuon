package messenger

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/rs/zerolog"
	waTypes "go.mau.fi/whatsmeow/types"

	"github.com/ralph/tuon/internal/auth"
	"github.com/ralph/tuon/internal/store"
)

// TUON_LIVE=1 go test ./internal/connector/messenger -run TestLiveKeys -v
// Read-only: asks Messenger's server for the key bundle it hands to people
// who message this device, and checks each key against tuon.db. Prints
// only IDs and match/mismatch, never key material. tuon must be closed.
func TestLiveKeys(t *testing.T) {
	if os.Getenv("TUON_LIVE") == "" {
		t.Skip("set TUON_LIVE=1")
	}
	release, err := store.Lock()
	if err != nil {
		t.Skip(err)
	}
	defer release()
	st, err := store.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	creds, err := auth.Load(store.Dir())
	if err != nil {
		t.Fatal(err)
	}
	c := New(creds, st.DB, zerolog.New(zerolog.NewTestWriter(t)).Level(zerolog.ErrorLevel))
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
