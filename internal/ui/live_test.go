package ui

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/rs/zerolog"
	zlog "github.com/rs/zerolog/log"

	"github.com/ralph/tuon/internal/auth"
	"github.com/ralph/tuon/internal/connector"
	"github.com/ralph/tuon/internal/connector/messenger"
	"github.com/ralph/tuon/internal/keystore"
	"github.com/ralph/tuon/internal/store"
	"github.com/ralph/tuon/internal/vault"
)

// TestLive runs the real Messenger connector against the real store and
// prints what the screens look like. Read-only: it never sends, not even
// "Seen" when it opens a chat (readOnly).
//
//	TUON_LIVE=1 go test ./internal/ui -run TestLive -v
func TestLive(t *testing.T) {
	if os.Getenv("TUON_LIVE") == "" {
		t.Skip("set TUON_LIVE=1 to run against the real account")
	}
	release, err := store.Lock()
	if err != nil {
		t.Skip(err) // tuon is open: don't share its encryption keys
	}
	defer release()
	st, creds, kdb := liveOpen(t)
	defer st.Close()
	defer kdb.Close()

	logf, _ := os.Create(filepath.Join(t.TempDir(), "live.log"))
	zerolog.SetGlobalLevel(zerolog.WarnLevel)
	log := zerolog.New(logf)
	zlog.Logger = log

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	conn := messenger.New(creds, st.DB, log)
	conn.SetKeyDB(kdb) // never AllowNewDevice
	defer conn.Close()

	m := New(ctx, st, readOnly{conn, t}, "messenger")
	next, _ := m.Update(tea.WindowSizeMsg{Width: 90, Height: 24})
	m = next.(Model)
	// Init batches the connect with timers; run the connect the way Bubble Tea would
	next, _ = m.Update(startedMsg{conn.Start(ctx)})
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

// readOnly keeps TestLive from reaching anyone: opening a chat would
// otherwise send "Seen" to a real person (with :seen on).
type readOnly struct {
	connector.Connector
	t *testing.T
}

func (r readOnly) MarkRead(context.Context, string, []connector.Message) error { return nil }
func (r readOnly) Send(context.Context, string, string, []connector.Mention, *connector.ReplyRef) error {
	r.t.Error("TestLive tried to send a message")
	return errors.New("read-only")
}
func (r readOnly) SendFile(context.Context, string, connector.Attachment) error {
	r.t.Error("TestLive tried to send a file")
	return errors.New("read-only")
}
func (r readOnly) React(context.Context, string, string, string, string) error {
	r.t.Error("TestLive tried to react")
	return errors.New("read-only")
}

// liveOpen opens the real data the way tuon does: the vault (one Touch
// ID), tuon.db, the sealed session and keys.db.
func liveOpen(t *testing.T) (*store.Store, auth.Creds, *sql.DB) {
	t.Helper()
	dir := store.Dir()
	h := liveHelper()
	if vault.Kind(dir) != vault.KindEnclave || h == nil {
		t.Skip("needs the Touch ID vault and its helper")
	}
	v, err := vault.UnlockEnclave(dir, h)
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(v.DeriveKey("tuon.db/1"))
	if err != nil {
		t.Fatal(err)
	}
	st.SetCipher(v)
	creds, err := auth.LoadSealed(dir, v, false)
	if err != nil {
		t.Skip("not logged in")
	}
	kdb, err := keystore.Open(dir, v.DeriveKey("keys.db/2"))
	if err != nil {
		t.Fatal(err)
	}
	return st, creds, kdb
}

// liveHelper: the Touch ID helper for live tests. A test binary runs from
// Go's build folder, never next to the helper, so fall back to the
// installed one (~/.local/bin, from install.sh), if only you can change it.
func liveHelper() *vault.Helper {
	if h := vault.FindHelper(); h != nil {
		return h
	}
	home, _ := os.UserHomeDir()
	p := filepath.Join(home, ".local", "bin", "tuon-keyhelper")
	if st, err := os.Stat(p); err == nil && st.Mode().IsRegular() && st.Mode().Perm()&0o022 == 0 {
		return &vault.Helper{Path: p}
	}
	return nil
}
