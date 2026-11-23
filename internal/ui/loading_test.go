package ui

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/ralph/tuon/internal/connector"
)

func sync(m Model, part string, done bool, chats int, err error) Model {
	next, _ := m.Update(EventsMsg{connector.SyncEvent{Part: part, Done: done, Chats: chats, Err: err}})
	return next.(Model)
}

// TestLoadingScreen: until Messenger, encrypted chats and the chat list are
// in, tuon shows what it's syncing and ignores keys and the mouse; then the
// chats appear.
func TestLoadingScreen(t *testing.T) {
	m, st, _ := newTestModel(t, 100, 30)
	_ = st.UpsertChat(connector.Chat{ID: "1", Name: "Alice", LastActivity: time.Now()})
	_ = st.SetSpace("1", m.space) // the folder tuon opens on
	m.reloadChats()
	if !strings.Contains(ansi.Strip(m.View()), "Alice") {
		t.Fatal("setup: Alice should be in the list")
	}
	m.WaitForSync()
	screen := func() string { return ansi.Strip(m.View()) }
	if !strings.Contains(screen(), "syncing") || strings.Contains(screen(), "Alice") {
		t.Fatalf("loading screen should hide the chats:\n%s", screen())
	}
	if !strings.Contains(screen(), "░") {
		t.Fatalf("the chat screen should show blurred behind the card:\n%s", screen())
	}
	before := m.cursor
	m = key(m, "down")
	m = key(m, "enter")
	if !m.loading || m.cursor != before || m.focus != focusList {
		t.Fatal("keys must do nothing while syncing")
	}
	next, _ := m.Update(tea.MouseMsg{X: 5, Y: 5, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	m = next.(Model)

	m = sync(m, connector.SyncMessenger, true, 20, nil)
	m = sync(m, connector.SyncChats, false, 57, nil)
	if s := screen(); !strings.Contains(s, "✓ Connected to Messenger") || !strings.Contains(s, "57 so far") {
		t.Fatalf("progress not shown:\n%s", s)
	}
	m = sync(m, connector.SyncEncrypted, true, 0, errors.New("keys not here"))
	if !strings.Contains(screen(), "Encrypted chats unavailable: keys not here") {
		t.Fatalf("a failed step should say why:\n%s", screen())
	}
	if !m.loading {
		t.Fatal("still waiting for the chat list")
	}
	m = sync(m, connector.SyncChats, true, 63, nil)
	if m.loading {
		t.Fatal("all steps done: the loading screen should go")
	}
	if !strings.Contains(screen(), "Alice") {
		t.Fatalf("chats should show now:\n%s", screen())
	}
}

// TestLoadingEscape: enter lets you in after a failed start or a long
// wait, never before; q always quits.
func TestLoadingEscape(t *testing.T) {
	m, _, _ := newTestModel(t, 100, 30)
	m.WaitForSync()
	next, _ := m.Update(startedMsg{err: errors.New("no internet")})
	m = next.(Model)
	if s := ansi.Strip(m.View()); !strings.Contains(s, "Couldn't connect: no internet") || !strings.Contains(s, "read saved chats offline") {
		t.Fatalf("a failed start should offer the saved chats:\n%s", s)
	}
	m = key(m, "enter")
	if m.loading {
		t.Fatal("enter after a failed start should let you in")
	}

	m, _, _ = newTestModel(t, 100, 30)
	m.WaitForSync()
	m = key(m, "enter")
	if !m.loading {
		t.Fatal("enter must not skip a sync that's going fine")
	}
	m.loadStart = time.Now().Add(-loadSkipAfter - time.Second)
	if !strings.Contains(ansi.Strip(m.View()), "Taking longer than usual") {
		t.Fatal("a long wait should offer a way in")
	}
	m = key(m, "enter")
	if m.loading {
		t.Fatal("enter after a long wait should let you in")
	}

	m, _, _ = newTestModel(t, 100, 30)
	m.WaitForSync()
	if _, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")}); cmd == nil {
		t.Fatal("q should quit while syncing")
	}
}

// TestNoLoadingByDefault: without WaitForSync (tests, other front ends)
// there's no loading screen.
func TestNoLoadingByDefault(t *testing.T) {
	m, _, _ := newTestModel(t, 100, 30)
	if m.loading || strings.Contains(ansi.Strip(m.View()), "syncing") {
		t.Fatal("loading screen without WaitForSync")
	}
}
