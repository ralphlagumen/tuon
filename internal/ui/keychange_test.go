package ui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ralph/tuon/internal/connector"
)

// A key change puts a notice in every encrypted chat with that person (not
// plain ones), warns in the header, and the notice can't be reacted or
// replied to.
func TestKeyChangeNotice(t *testing.T) {
	m, st, _ := newTestModel(t, 110, 30)
	m = seed(m)
	now := time.Now()
	next, _ := m.Update(EventsMsg([]any{
		connector.ChatEvent{Chat: connector.Chat{ID: "20", Name: "Secret Group", LastActivity: now.Add(-time.Hour), Group: true, Encrypted: true}},
		connector.ParticipantEvent{ChatID: "20", UserID: "200"},
		connector.KeyChangeEvent{UserID: "200", Time: now},
		connector.KeyChangeEvent{UserID: "200", Time: now.Add(time.Second)}, // second device, same minute
	}))
	m = next.(Model)

	count := func(chat string) int {
		msgs, err := st.Messages(chat, 50, "1")
		if err != nil {
			t.Fatal(err)
		}
		n := 0
		for _, msg := range msgs {
			if connector.IsLocalNotice(msg.Message) {
				n++
			}
		}
		return n
	}
	if count("200") != 1 || count("20") != 1 {
		t.Fatalf("notices: 1:1=%d group=%d, want 1 each (deduped per minute)", count("200"), count("20"))
	}
	if count("10") != 0 {
		t.Fatal("no notice in a plain (not encrypted) chat")
	}
	if !strings.Contains(strings.Join(m.warnings, "|"), "Jen's security key changed") {
		t.Fatalf("header warnings = %q", m.warnings)
	}

	// open the 1:1: the notice shows; picking it allows no react or reply
	m = key(m, "3")
	for i := 0; i < 5 && (m.chat == nil || m.chat.ID != "200"); i++ {
		m = key(m, "down")
		next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
		m = run(next.(Model), cmd)
		if m.chat != nil && m.chat.ID != "200" {
			m = key(m, "tab")
		}
	}
	if m.chat == nil || m.chat.ID != "200" {
		t.Fatalf("couldn't open Jen's chat:\n%s", screen(m))
	}
	if s := screen(m); !strings.Contains(s, "Security key changed") {
		t.Fatalf("notice not shown:\n%s", s)
	}
	m = key(m, "up") // pick the newest: the notice
	m = key(m, "r")
	if m.reacting {
		t.Fatal("reacting to tuon's own notice")
	}
	m = key(m, "enter")
	if m.replyTo != nil {
		t.Fatal("replying to tuon's own notice")
	}
}

// Several lasting warnings all show (A13-9); taking one down keeps the rest.
func TestWarningsStack(t *testing.T) {
	m, _, _ := newTestModel(t, 160, 30)
	m.Warn("ignored HTTPS_PROXY: tuon connects to Meta directly")
	m.Warn("Jen's security key changed: see the chat")
	m.Warn("Jen's security key changed: see the chat") // same again: once
	s := screen(m)
	if !strings.Contains(s, "HTTPS_PROXY") || !strings.Contains(s, "security key changed") || strings.Count(s, "security key changed") != 1 {
		t.Fatalf("warnings:\n%s", s)
	}
	m.unwarn("ignored HTTPS_PROXY: tuon connects to Meta directly")
	if s := screen(m); strings.Contains(s, "HTTPS_PROXY") || !strings.Contains(s, "security key changed") {
		t.Fatalf("after unwarn:\n%s", s)
	}
}

// The kept chat lines follow changes that don't change the message count
// (a new reaction), and wheel scrolling reuses them.
func TestChatLinesCacheFollowsChanges(t *testing.T) {
	m, _, _ := newTestModel(t, 110, 30)
	m = seed(m)
	m = key(m, "3")
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = run(next.(Model), cmd)
	if strings.Contains(screen(m), "👍") {
		t.Fatal("setup: no 👍 yet")
	}
	a, _ := m.chatLines()
	b, _ := m.chatLines()
	if &a[0] != &b[0] {
		t.Fatal("nothing changed: lines should be reused")
	}
	next, cmd = m.Update(EventsMsg{connector.ReactionEvent{ChatID: "10", MessageID: "m2", ActorID: "100", Emoji: "👍"}})
	m = run(next.(Model), cmd)
	if !strings.Contains(screen(m), "👍") {
		t.Fatalf("new reaction not shown (stale lines):\n%s", screen(m))
	}
}
