package ui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/rs/zerolog"
	zlog "github.com/rs/zerolog/log"

	"github.com/ralph/tuon/internal/connector/messenger"
	"github.com/ralph/tuon/internal/store"
)

// TestLive runs the real Messenger connector against the real store and
// prints what the screens look like. Read-only: it never sends.
//
//	TUON_LIVE=1 go test ./internal/ui -run TestLive -v
func TestLive(t *testing.T) {
	if os.Getenv("TUON_LIVE") == "" {
		t.Skip("set TUON_LIVE=1 to run against the real account")
	}
	b, err := os.ReadFile(filepath.Join(store.Dir(), "cookies.txt"))
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	logf, _ := os.Create(filepath.Join(t.TempDir(), "live.log"))
	zerolog.SetGlobalLevel(zerolog.WarnLevel)
	log := zerolog.New(logf)
	zlog.Logger = log

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	conn := messenger.New(strings.TrimSpace(string(b)), st.DB, log)
	defer conn.Close()

	m := New(ctx, st, conn, "messenger")
	next, _ := m.Update(tea.WindowSizeMsg{Width: 90, Height: 24})
	m = next.(Model)
	next, _ = m.Update(m.Init()())
	m = next.(Model)
	if m.statusErr {
		t.Fatal(m.status)
	}

	// pump events for a while, like main does
	deadline := time.After(25 * time.Second)
	n := 0
pump:
	for {
		select {
		case e := <-conn.Events():
			next, _ = m.Update(EventsMsg{e})
			m = next.(Model)
			n++
		case <-deadline:
			break pump
		}
	}
	t.Logf("applied %d events; status=%q", n, m.status)

	m = key(m, "3")
	t.Logf("inbox:\n%s", ansi.Strip(m.View()))

	var total, enc int
	_ = st.DB.QueryRow(`SELECT COUNT(*), COALESCE(SUM(encrypted),0) FROM tuon_chats WHERE last_ts > 0`).Scan(&total, &enc)
	t.Logf("chats in store: %d (encrypted 1:1/groups: %d)", total, enc)

	if len(m.chats) > 0 {
		next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
		m = next.(Model)
		// run the open-chat commands (load older, photos), then drain events
		m = run(m, cmd)
		drain := time.After(8 * time.Second)
	more:
		for {
			select {
			case e := <-conn.Events():
				next, _ = m.Update(EventsMsg{e})
				m = next.(Model)
			case <-drain:
				break more
			}
		}
		t.Logf("chat %q: %d messages loaded\n%s", m.chat.Display, len(m.msgs), ansi.Strip(m.View()))
	}
}
