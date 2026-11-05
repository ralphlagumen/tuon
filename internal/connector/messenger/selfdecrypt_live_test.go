package messenger

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"go.mau.fi/libsignal/keys/identity"
	"go.mau.fi/libsignal/protocol"
	"go.mau.fi/libsignal/session"
	"go.mau.fi/libsignal/state/record"
	"go.mau.fi/whatsmeow/proto/waAdv"
	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/store/sqlstore"
	waTypes "go.mau.fi/whatsmeow/types"
	waLog "go.mau.fi/whatsmeow/util/log"
)

// readOnlyDevice is this device's real key store with every write turned
// off: a diagnostic decrypt must not create sessions, use up a one-time
// key or record identities.
type readOnlyDevice struct{ *store.Device }

func (readOnlyDevice) StoreSession(context.Context, *protocol.SignalAddress, *record.Session) error {
	return nil
}
func (readOnlyDevice) RemovePreKey(context.Context, uint32) error { return nil }
func (readOnlyDevice) SaveIdentity(context.Context, *protocol.SignalAddress, *identity.Key) error {
	return nil
}
func (readOnlyDevice) IsTrustedIdentity(context.Context, *protocol.SignalAddress, *identity.Key) (bool, error) {
	return true, nil
}

// TUON_LIVE=1 go test ./internal/connector/messenger -run TestLiveSelfDecrypt -v
// Does this device decrypt a first message built the way a contact's phone
// builds one? A throwaway sender (in memory) fetches this device's key
// bundle from Meta's server, encrypts "ping" to it, and tuon's real keys
// decrypt it, with nothing sent and nothing stored. Also checks the stored
// sessions with the contacts whose messages failed. tuon must be closed.
func TestLiveSelfDecrypt(t *testing.T) {
	if os.Getenv("TUON_LIVE") == "" {
		t.Skip("set TUON_LIVE=1")
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
	var dev *store.Device
	for i := 0; i < 120; i++ {
		c.mu.Lock()
		wa, d := c.wa, c.wadev
		c.mu.Unlock()
		if wa != nil {
			dev = d
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
	t.Logf("our device: %s, registration id %d", me, dev.RegistrationID)

	// stored sessions with the devices whose messages failed
	for _, who := range []string{"100001527910190:4", "100001527910190:5", "100002876447027:56"} {
		jid, _ := waTypes.ParseJID(who + "@msgr")
		raw, err := dev.Sessions.GetSession(ctx, jid.SignalAddress().String())
		if err != nil || raw == nil {
			t.Logf("session %s: none (err %v)", who, err)
			continue
		}
		sess, err := record.NewSessionFromBytes(raw, store.SignalProtobufSerializer.Session, store.SignalProtobufSerializer.State)
		if err != nil {
			t.Logf("session %s: unreadable: %v", who, err)
			continue
		}
		states := append([]*record.State{sess.SessionState()}, sess.PreviousSessionStates()...)
		for i, s := range states {
			local := s.LocalIdentityKey()
			ours := local != nil && bytes.Equal(local.PublicKey().Serialize()[1:], dev.IdentityKey.Pub[:])
			t.Logf("session %s state %d: version %d, has sender chain %v, local identity is ours=%v", who, i, s.Version(), s.HasSenderChain(), ours)
		}
	}

	// a first message from a throwaway sender, built from the server's bundle
	mem, err := sql.Open("sqlite3", "file:alice?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	defer mem.Close()
	mem.SetMaxOpenConns(1)
	container := sqlstore.NewWithDB(mem, "sqlite3", waLog.Noop)
	if err := container.Upgrade(ctx); err != nil {
		t.Fatal(err)
	}
	alice := container.NewDevice()
	aliceJID := waTypes.JID{User: "1", Device: 1, Server: waTypes.MessengerServer}
	alice.ID = &aliceJID
	alice.Account = &waAdv.ADVSignedDeviceIdentity{Details: []byte{}, AccountSignature: make([]byte, 64), AccountSignatureKey: make([]byte, 32), DeviceSignature: make([]byte, 64)}
	if err := alice.Save(ctx); err != nil { // sets up its stores
		t.Fatal(err)
	}

	for round := 1; round <= 2; round++ {
		bundle := wa.DangerousInternals().FetchPreKeysNoError(ctx, []waTypes.JID{me})[me]
		if bundle == nil {
			t.Fatal("server returned no bundle")
		}
		_ = alice.Sessions.DeleteSession(ctx, me.SignalAddress().String())
		ab := session.NewBuilderFromSignal(alice, me.SignalAddress(), store.SignalProtobufSerializer)
		if err := ab.ProcessBundle(ctx, bundle); err != nil {
			t.Fatalf("sender: process bundle: %v", err)
		}
		ct, err := session.NewCipher(ab, me.SignalAddress()).Encrypt(ctx, []byte("ping"))
		if err != nil {
			t.Fatalf("sender: encrypt: %v", err)
		}
		pk, err := protocol.NewPreKeySignalMessageFromBytes(ct.Serialize(), store.SignalProtobufSerializer.PreKeySignalMessage, store.SignalProtobufSerializer.SignalMessage)
		if err != nil {
			t.Fatalf("not a first (prekey) message: %v", err)
		}
		bb := session.NewBuilderFromSignal(readOnlyDevice{dev}, aliceJID.SignalAddress(), store.SignalProtobufSerializer)
		pt, err := session.NewCipher(bb, aliceJID.SignalAddress()).DecryptMessage(ctx, pk)
		otk := "none"
		if id := pk.PreKeyID(); id != nil && !id.IsEmpty {
			otk = fmt.Sprint(id.Value)
		}
		t.Logf("round %d: one-time key %s, signed key %d → decrypt ok=%v (%v)", round, otk, pk.SignedPreKeyID(), err == nil && string(pt) == "ping", err)
	}
}
