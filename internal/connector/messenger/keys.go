package messenger

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"strconv"

	waTypes "go.mau.fi/whatsmeow/types"

	"github.com/ralph/tuon/internal/connector"
)

// ContactKeys lists the identity keys of a 1:1 chat's devices and ours,
// for comparing with Messenger's "Verify end-to-end encryption" screen
// (same hex, one key per device: MESSENGER-MITM M4.0). The device list
// comes from Messenger's server (what the phone lists too); the keys from
// keys.db, the ones tuon actually encrypts to. Read-only: it never fetches
// a key bundle, which would use up one of the person's one-time keys.
func (c *Conn) ContactKeys(ctx context.Context, chatID string) (connector.KeySet, error) {
	c.mu.Lock()
	wa, dev := c.wa, c.wadev
	c.mu.Unlock()
	if wa == nil || dev == nil || dev.ID == nil {
		return connector.KeySet{}, errors.New("encrypted chats aren't connected")
	}
	if _, err := strconv.ParseUint(chatID, 10, 64); err != nil {
		return connector.KeySet{}, errors.New("not a person's chat")
	}
	kdb := c.keyDB
	if kdb == nil {
		kdb = c.db
	}
	me := *dev.ID
	them := waTypes.JID{User: chatID, Server: waTypes.MessengerServer}
	list := func(user waTypes.JID) []waTypes.JID {
		devs, err := wa.GetUserDevicesContext(ctx, []waTypes.JID{user})
		if err != nil || len(devs) == 0 {
			return storedDevices(ctx, kdb, me.String(), user) // offline: what keys.db has
		}
		return devs
	}
	var set connector.KeySet
	for _, d := range list(them) {
		set.Theirs = append(set.Theirs, connector.DeviceKey{Device: d.Device, Key: storedKey(ctx, kdb, me.String(), d)})
	}
	for _, d := range list(me.ToNonAD()) {
		k := connector.DeviceKey{Device: d.Device, This: d.Device == me.Device}
		if k.This {
			k.Key = dev.IdentityKey.Pub[:]
		} else {
			k.Key = storedKey(ctx, kdb, me.String(), d)
		}
		set.Mine = append(set.Mine, k)
	}
	byDevice := func(ks []connector.DeviceKey) {
		sort.Slice(ks, func(i, j int) bool { return ks[i].Device < ks[j].Device })
	}
	byDevice(set.Theirs)
	byDevice(set.Mine)
	return set, nil
}

// storedKey: the key keys.db holds for one device, nil if none yet.
func storedKey(ctx context.Context, db *sql.DB, ourJID string, d waTypes.JID) []byte {
	var k []byte
	err := db.QueryRowContext(ctx, `SELECT identity FROM whatsmeow_identity_keys WHERE our_jid = ? AND their_id = ?`,
		ourJID, d.SignalAddress().String()).Scan(&k)
	if err != nil || len(k) != 32 {
		return nil
	}
	return k
}

// storedDevices: the devices keys.db has keys for, when the server can't
// be asked.
func storedDevices(ctx context.Context, db *sql.DB, ourJID string, user waTypes.JID) []waTypes.JID {
	rows, err := db.QueryContext(ctx, `SELECT their_id FROM whatsmeow_identity_keys WHERE our_jid = ? AND their_id LIKE ?`,
		ourJID, user.SignalAddressUser()+":%")
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []waTypes.JID
	for rows.Next() {
		var id string
		if rows.Scan(&id) != nil {
			continue
		}
		n, err := strconv.ParseUint(id[len(user.SignalAddressUser())+1:], 10, 16)
		if err != nil {
			continue
		}
		d := user
		d.Device = uint16(n)
		out = append(out, d)
	}
	return out
}

var _ connector.KeyLister = (*Conn)(nil)
