package ui

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/ralph/tuon/internal/connector"
)

func (f *fakeConn) ContactKeys(context.Context, string) (connector.KeySet, error) {
	return f.keySet, nil
}

func keyOf(b byte) []byte { return bytes.Repeat([]byte{b}, 32) }

// TestKeysPanel: :keys shows their devices' keys and ours in Messenger's
// hex; v marks them verified; a later change is a lasting warning until
// you verify again.
func TestKeysPanel(t *testing.T) {
	m, st, fc := newTestModel(t, 120, 40)
	_ = st.UpsertChat(connector.Chat{ID: "42", Name: "Raine Villaver", Encrypted: true, LastActivity: time.Now()})
	_ = st.SetSpace("42", m.space)
	m.reloadChats()
	fc.keySet = connector.KeySet{
		Theirs: []connector.DeviceKey{{Device: 3, Key: keyOf(0xab)}, {Device: 7}},
		Mine:   []connector.DeviceKey{{Device: 56, Key: keyOf(0xf7), This: true}},
	}
	run := func(m Model, command string) Model {
		next, cmd := m.runCommand(command)
		m = next.(Model)
		if cmd != nil {
			if km, ok := cmd().(keysMsg); ok {
				next, _ = m.Update(km)
				m = next.(Model)
			}
		}
		return m
	}
	m = run(m, "k")
	s := ansi.Strip(m.View())
	for _, want := range []string{"Keys · Raine Villaver", "device 3", "05 AB AB AB", "1 more device Messenger lists", "05 F7 F7", "← tuon", "Not verified yet"} {
		if !strings.Contains(s, want) {
			t.Fatalf("panel misses %q:\n%s", want, s)
		}
	}
	m = key(m, "v")
	if _, _, ok := st.Verified("42"); !ok {
		t.Fatal("v should mark them verified")
	}
	if !strings.Contains(m.toast, "Marked Raine Villaver verified") {
		t.Fatalf("toast: %q", m.toast)
	}
	if s := ansi.Strip(m.View()); !strings.Contains(s, "✓ Verified") {
		t.Fatalf("panel should say verified:\n%s", s)
	}
	m = key(m, "esc")
	if m.keys != nil {
		t.Fatal("esc closes the panel")
	}

	// their key changes (a new phone, or someone in the middle)
	fc.keySet.Theirs[0].Key = keyOf(0xcd)
	fc.keySet.Theirs = append(fc.keySet.Theirs, connector.DeviceKey{Device: 9, Key: keyOf(0x11)})
	cmd := m.checkVerified("42")
	if cmd == nil || m.checkVerified("42") != nil {
		t.Fatal("a verified person is checked once a run")
	}
	next, _ := m.Update(cmd())
	m = next.(Model)
	if !strings.Contains(strings.Join(m.warnings, "|"), "Raine's keys changed since you verified them") {
		t.Fatalf("change not warned: %v", m.warnings)
	}
	m = run(m, "keys")
	if s := ansi.Strip(m.View()); !strings.Contains(s, "device 3 changed") || !strings.Contains(s, "device 9 is new") {
		t.Fatalf("panel should say what changed:\n%s", s)
	}
	m = key(m, "v")
	if len(m.warnings) != 0 {
		t.Fatalf("verifying again takes the warning down: %v", m.warnings)
	}
	m = key(m, "u")
	if _, _, ok := st.Verified("42"); ok {
		t.Fatal("u should unmark")
	}
}

func TestKeysOnlyForEncrypted1to1(t *testing.T) {
	m, st, _ := newTestModel(t, 120, 40)
	_ = st.UpsertChat(connector.Chat{ID: "5", Name: "Plain", LastActivity: time.Now()})
	_ = st.SetSpace("5", m.space)
	m.reloadChats()
	next, _ := m.runCommand("keys")
	m = next.(Model)
	if m.keys != nil || !strings.Contains(m.toast, "isn't end-to-end encrypted") {
		t.Fatalf("plain chat: %q", m.toast)
	}
}

// TestUpdateNotice: the launcher's notice shows in the header; :update
// outside the launcher says how to update instead of quitting.
func TestUpdateNotice(t *testing.T) {
	m, _, _ := newTestModel(t, 120, 30)
	next, _ := m.Update(UpdateMsg("v0.5.0"))
	m = next.(Model)
	if !strings.Contains(ansi.Strip(m.View()), "tuon v0.5.0 is out: :update") {
		t.Fatalf("no notice:\n%s", ansi.Strip(m.View()))
	}
	next, cmd := m.runCommand("update")
	m = next.(Model)
	if m.WantsUpdate() || cmd == nil || !strings.Contains(m.toast, "tuon update") {
		t.Fatalf("not under the launcher: should say how, not quit (toast %q)", m.toast)
	}
}
