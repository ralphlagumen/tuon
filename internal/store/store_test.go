package store

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ralph/tuon/internal/connector"
)

func TestUnnamedChatsGetPeopleNames(t *testing.T) {
	st, err := OpenPath(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	st.Self = "1"
	now := time.Now()
	for id, name := range map[string]string{"10": "Sherwin Cruz", "11": "Ed Santos", "12": "Ann Lim", "13": "Bo Reyes", "1": "Ralph"} {
		_ = st.UpsertContact(id, name)
	}
	msg := func(id, chat, from string, ago time.Duration) {
		if _, err := st.AddMessage(connector.Message{ID: id, ChatID: chat, SenderID: from, Text: "hi", Time: now.Add(-ago)}, false); err != nil {
			t.Fatal(err)
		}
	}

	// a group we never got details for: only live messages from 2 people
	msg("a", "700", "10", 3*time.Minute)
	msg("b", "700", "11", 2*time.Minute)
	msg("c", "700", "1", time.Minute)
	c, err := st.Chat("700")
	if err != nil {
		t.Fatal(err)
	}
	if c.Display != "Ed and Sherwin" || !c.Group {
		t.Fatalf("got %q group=%v", c.Display, c.Group)
	}

	// known group, no name, many members
	_ = st.UpsertChat(connector.Chat{ID: "800", Group: true, LastActivity: now})
	for _, u := range []string{"10", "11", "12", "13"} {
		_ = st.AddParticipant(connector.ParticipantEvent{ChatID: "800", UserID: u})
	}
	c, _ = st.Chat("800")
	if c.Display != "Ann, Bo and 2 others" {
		t.Fatalf("got %q", c.Display)
	}

	// a type-only update must not wipe the name, snippet or time
	_ = st.UpsertChat(connector.Chat{ID: "900", Name: "Townhall", Snippet: "hello", LastActivity: now})
	_ = st.UpsertChat(connector.Chat{ID: "900", Group: true})
	c, _ = st.Chat("900")
	if c.Display != "Townhall" || c.Snippet != "hello" || !c.Group || c.LastActivity.IsZero() {
		t.Fatalf("type-only update clobbered chat: %+v", c)
	}

	// it shows in lists under that label
	rows, _ := st.Chats(Inbox, "")
	found := false
	for _, r := range rows {
		found = found || r.Display == "Ed and Sherwin"
	}
	if !found {
		t.Fatal("unnamed group missing from list")
	}
}

func TestPendingMessageIsReplacedByEcho(t *testing.T) {
	st, err := OpenPath(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	now := time.Now()
	_, _ = st.AddMessage(connector.Message{ID: "pending:42", ChatID: "c", SenderID: "1", Text: "hi", Time: now, FromMe: true, Pending: true}, true)
	msgs, _ := st.Messages("c", 10, "1")
	if len(msgs) != 1 || !msgs[0].Pending {
		t.Fatalf("pending not shown: %+v", msgs)
	}
	_, _ = st.AddMessage(connector.Message{ID: "mid.real", ChatID: "c", SenderID: "1", Text: "hi", Time: now, FromMe: true, ReplacesID: "pending:42"}, true)
	msgs, _ = st.Messages("c", 10, "1")
	if len(msgs) != 1 || msgs[0].ID != "mid.real" || msgs[0].Pending {
		t.Fatalf("echo should replace pending: %+v", msgs)
	}
	_, _ = st.AddMessage(connector.Message{ID: "pending:43", ChatID: "c", SenderID: "1", Text: "oops", Time: now, FromMe: true, Pending: true}, true)
	_ = st.RemoveMessage("pending:43")
	if msgs, _ = st.Messages("c", 10, "1"); len(msgs) != 1 {
		t.Fatalf("failed send should be removed: %d", len(msgs))
	}
}

func TestFolders(t *testing.T) {
	st, err := OpenPath(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	got := func() string {
		var names []string
		for _, s := range st.Spaces() {
			names = append(names, Label(s))
		}
		return strings.Join(names, ",")
	}
	if g := got(); g != "Work,Personal,Inbox" {
		t.Fatalf("start = %s", g)
	}
	_ = st.UpsertChat(connector.Chat{ID: "1", LastActivity: time.Now()})
	_ = st.SetSpace("1", Work)

	if _, err := st.AddSpace("  Family  "); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddSpace("family"); err == nil {
		t.Fatal("duplicate allowed")
	}
	if _, err := st.AddSpace("inbox"); err == nil {
		t.Fatal("inbox name should be refused")
	}
	if _, err := st.RenameSpace(Work, "Office"); err != nil {
		t.Fatal(err)
	}
	if c, _ := st.Chat("1"); c.Space != "Office" {
		t.Fatalf("chat should follow rename, in %q", c.Space)
	}
	_ = st.MoveSpace("Family", -1)
	if g := got(); g != "Office,Family,Personal,Inbox" {
		t.Fatalf("after move = %s", g)
	}
	_ = st.DeleteSpace("Office")
	if c, _ := st.Chat("1"); c.Space != Inbox {
		t.Fatalf("deleted folder's chats go to Inbox, got %q", c.Space)
	}
	if g := got(); g != "Family,Personal,Inbox" {
		t.Fatalf("after delete = %s", g)
	}
	if err := st.DeleteSpace(Inbox); err == nil {
		t.Fatal("Inbox must not be deletable")
	}
}

func TestReadStaysRead(t *testing.T) {
	st, err := OpenPath(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	t0 := time.Now().Add(-time.Hour)
	_ = st.UpsertChat(connector.Chat{ID: "c", Name: "Team", LastActivity: t0, Unread: true})
	_ = st.MarkRead("c")
	// Messenger still thinks it's unread (we sent no "seen") and says so again
	_ = st.UpsertChat(connector.Chat{ID: "c", Name: "Team", LastActivity: t0, Unread: true})
	_, _ = st.AddMessage(connector.Message{ID: "old", ChatID: "c", SenderID: "2", Text: "x", Time: t0}, false)
	if c, _ := st.Chat("c"); c.Unread {
		t.Fatal("a chat you read must stay read")
	}
	// a newer message makes it unread again
	_, _ = st.AddMessage(connector.Message{ID: "new", ChatID: "c", SenderID: "2", Text: "y", Time: t0.Add(time.Minute)}, false)
	if c, _ := st.Chat("c"); !c.Unread {
		t.Fatal("a newer message should mark it unread")
	}
}

func TestMergeOneToOne(t *testing.T) {
	st, err := OpenPath(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	st.Self = "1"
	// the thread number Messenger lists, unread, filed in Work
	_ = st.UpsertChat(connector.Chat{ID: "900", Encrypted: true, Unread: true, LastActivity: time.UnixMilli(2000)})
	_ = st.SetSpace("900", Work)
	_ = st.AddParticipant(connector.ParticipantEvent{ChatID: "900", UserID: "1"})
	_ = st.AddParticipant(connector.ParticipantEvent{ChatID: "900", UserID: "42"})
	// the messages, under the person's ID
	_, _ = st.AddMessage(connector.Message{ID: "m1", ChatID: "42", SenderID: "42", Text: "hi", Time: time.UnixMilli(1000)}, false)
	if err := st.MergeOneToOne(); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Chat("900"); err != nil {
		t.Fatal("old id should resolve:", err)
	}
	c, _ := st.Chat("42")
	if c.ID != "42" || c.Space != Work || !c.Unread {
		t.Fatalf("merged chat = %+v", c)
	}
	if got := st.Resolve("900"); got != "42" {
		t.Fatalf("resolve = %q", got)
	}
	rows, _ := st.Chats(Work, "")
	if len(rows) != 1 {
		t.Fatalf("want one row in Work, got %d", len(rows))
	}
	msgs, _ := st.Messages("42", 10, "1")
	if len(msgs) != 1 {
		t.Fatalf("messages = %d", len(msgs))
	}
}

func TestMissingPlaceholder(t *testing.T) {
	s, err := OpenPath(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	t0 := time.UnixMilli(1_000_000)
	_ = s.UpsertChat(connector.Chat{ID: "c", LastActivity: t0, Encrypted: true})
	_, _ = s.AddMessage(connector.Message{ID: "old", ChatID: "c", Text: "game?", Time: t0, FromMe: true}, false)

	// Messenger bumps the chat before the message itself shows up
	later := t0.Add(5 * time.Minute)
	_ = s.UpsertChat(connector.Chat{ID: "c", LastActivity: later, Encrypted: true})
	if !s.Behind("c", later) || !s.Waiting("c") {
		t.Fatal("chat should be waiting for a message")
	}

	// it arrives undecryptable: a placeholder
	ph := connector.Message{ID: "m1", ChatID: "c", SenderID: "raine", Time: later.Add(-time.Second),
		Media: []connector.Media{{Kind: connector.Missing}}}
	if isNew, _ := s.AddMessage(ph, false); !isNew {
		t.Fatal("placeholder should count as new")
	}
	if !s.HasMissing("c") {
		t.Fatal("placeholder not found")
	}

	// the backup has it: the real one replaces the placeholder, and names the chat
	real := ph
	real.Text, real.Media = "okiee", nil
	_, _ = s.AddMessage(real, false)
	if s.HasMissing("c") {
		t.Fatal("placeholder still there")
	}
	c, _ := s.Chat("c")
	if c.Snippet != "okiee" {
		t.Fatalf("snippet = %q, want the late message", c.Snippet)
	}

	// a late failed retry never overwrites the real message
	_, _ = s.AddMessage(ph, false)
	msgs, _ := s.Messages("c", 10, "me")
	for _, m := range msgs {
		if m.ID == "m1" && m.Text != "okiee" {
			t.Fatalf("real message overwritten: %+v", m)
		}
	}
}

func TestSearchIsLiteral(t *testing.T) {
	s, err := OpenPath(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := time.Now()
	for id, name := range map[string]string{"1": "a_b team", "2": "axb team", "3": "50% off"} {
		_ = s.UpsertChat(connector.Chat{ID: id, Name: name, LastActivity: now})
	}
	got := func(q string) int { rows, _ := s.Chats(Inbox, q); return len(rows) }
	if n := got("a_b"); n != 1 {
		t.Fatalf(`"a_b" matched %d chats, want 1`, n)
	}
	if n := got("50%"); n != 1 {
		t.Fatalf(`"50%%" matched %d chats, want 1`, n)
	}
	if n := got("team"); n != 2 {
		t.Fatalf(`"team" matched %d, want 2`, n)
	}
}

func TestRenameFolderMovesChats(t *testing.T) {
	s, err := OpenPath(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	work, err := s.AddSpace("Clients")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.RenameSpace("Nope", "X"); err == nil {
		t.Fatal("renaming a folder that doesn't exist should fail")
	}
	_, _ = s.AddSpace("Fam")
	_ = s.UpsertChat(connector.Chat{ID: "1", Name: "Dev", LastActivity: time.Now()})
	_ = s.SetSpace("1", work)
	if _, err := s.RenameSpace(work, "Fam"); err == nil {
		t.Fatal("renaming onto an existing folder should fail")
	}
	if rows, _ := s.Chats(work, ""); len(rows) != 1 {
		t.Fatal("a failed rename moved the chats anyway")
	}
	jobs, err := s.RenameSpace(work, "Jobs")
	if err != nil {
		t.Fatal(err)
	}
	if rows, _ := s.Chats(jobs, ""); len(rows) != 1 {
		t.Fatal("chats didn't follow the rename")
	}
}

func TestCompactGivesSpaceBack(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.db")
	s, err := OpenPath(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := time.Now()
	for i := 0; i < 3000; i++ {
		_, _ = s.AddMessage(connector.Message{ID: fmt.Sprint(i), ChatID: "c", SenderID: "2", Text: strings.Repeat("x", 400), Time: now.Add(time.Duration(i) * time.Second)}, false)
	}
	_ = s.Prune(100)
	var before, after int64
	_ = s.DB.QueryRow(`PRAGMA page_count`).Scan(&before)
	if did, err := s.Compact(); !did || err != nil {
		t.Fatalf("compact: did=%v err=%v", did, err)
	}
	_ = s.DB.QueryRow(`PRAGMA page_count`).Scan(&after)
	if after*2 > before {
		t.Fatalf("pages %d -> %d: not much smaller", before, after)
	}
	if did, _ := s.Compact(); did {
		t.Fatal("nothing to give back the second time")
	}
}

func TestMergeKeepsDraft(t *testing.T) {
	s, err := OpenPath(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := time.Now()
	_ = s.UpsertChat(connector.Chat{ID: "28147933184820682", LastActivity: now, Encrypted: true})
	_ = s.SaveDrafts(map[string]string{"28147933184820682": "see you at 7"})
	if err := s.Merge("28147933184820682", "100001279802241"); err != nil {
		t.Fatal(err)
	}
	if d := s.Drafts(); d["100001279802241"] != "see you at 7" || d["28147933184820682"] != "" {
		t.Fatalf("drafts after merge: %v", d)
	}
}
