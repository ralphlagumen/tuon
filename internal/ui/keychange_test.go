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
	if !strings.Contains(m.warning, "Jen's security key changed") {
		t.Fatalf("header warning = %q", m.warning)
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
