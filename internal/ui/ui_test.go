package ui

import (
	"context"
	"errors"
	"image"
	"image/color"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/ralph/tuon/internal/connector"
	"github.com/ralph/tuon/internal/store"
)

type sent struct {
	text     string
	mentions []connector.Mention
}

type reacted struct{ chat, msg, sender, emoji string }

type fakeConn struct {
	files   []connector.Attachment
	sent    []sent
	reacted []reacted
}

func (f *fakeConn) Start(context.Context) error { return nil }
func (f *fakeConn) Events() <-chan any          { return nil }
func (f *fakeConn) SelfID() string              { return "1" }
func (f *fakeConn) LoadOlder(context.Context, string, time.Time, string) error {
	return nil
}
func (f *fakeConn) Send(_ context.Context, _ string, text string, ms []connector.Mention) error {
	f.sent = append(f.sent, sent{text, ms})
	return nil
}
func (f *fakeConn) SendFile(_ context.Context, _ string, a connector.Attachment) error {
	f.files = append(f.files, a)
	return nil
}
func (f *fakeConn) React(_ context.Context, chat, msg, sender, emoji string) error {
	f.reacted = append(f.reacted, reacted{chat, msg, sender, emoji})
	return nil
}
func (f *fakeConn) FetchMedia(context.Context, connector.Media) ([]byte, error) { return nil, nil }
func (f *fakeConn) Close()                                                      {}

func newTestModel(t *testing.T, w, h int) (Model, *store.Store, *fakeConn) {
	t.Helper()
	st, err := store.OpenPath(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	fc := &fakeConn{}
	m := New(context.Background(), st, fc, "messenger")
	next, _ := m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	return next.(Model), st, fc
}

func key(m Model, k string) Model {
	var msg tea.KeyMsg
	switch k {
	case "enter":
		msg = tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		msg = tea.KeyMsg{Type: tea.KeyEsc}
	case "tab":
		msg = tea.KeyMsg{Type: tea.KeyTab}
	case "up":
		msg = tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		msg = tea.KeyMsg{Type: tea.KeyDown}
	default:
		msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
	}
	next, _ := m.Update(msg)
	return next.(Model)
}

func typeText(m Model, s string) Model {
	for _, r := range s {
		m = key(m, string(r))
	}
	return m
}

// run executes a command chain synchronously and feeds results back.
func run(m Model, cmd tea.Cmd) Model {
	if cmd == nil {
		return m
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, c := range batch {
			m = run(m, c)
		}
		return m
	}
	if msg == nil {
		return m
	}
	next, c2 := m.Update(msg)
	return run(next.(Model), c2)
}

func screen(m Model) string { return ansi.Strip(m.View()) }

func seed(m Model) Model {
	now := time.Now()
	evts := []any{
		connector.ContactEvent{ID: "100", Name: "Mark Reyes"},
		connector.ContactEvent{ID: "200", Name: "Jen Cruz"},
		connector.StatusEvent{Text: "connected"},
		connector.ChatEvent{Chat: connector.Chat{ID: "10", Name: "Dev Team", Snippet: "Mark: deploy done", LastActivity: now.Add(-2 * time.Minute), Unread: true, Group: true}},
		connector.ChatEvent{Chat: connector.Chat{ID: "200", Snippet: "ok see you at 3", LastActivity: now.Add(-time.Hour), Encrypted: true}},
		connector.ChatEvent{Chat: connector.Chat{ID: "10", Name: "Dev Team", Snippet: "dup", LastActivity: now.Add(-3 * time.Minute), Group: true}}, // older dup
		connector.ParticipantEvent{ChatID: "10", UserID: "200"},
		connector.MessageEvent{Message: connector.Message{ID: "m1", ChatID: "10", SenderID: "100", Text: "pushed the fix", Time: now.Add(-30 * time.Minute)}},
		connector.MessageEvent{Message: connector.Message{ID: "m2", ChatID: "10", SenderID: "1", Text: "thanks, ping me when green", Time: now.Add(-29 * time.Minute), FromMe: true}},
		connector.MessageEvent{Message: connector.Message{ID: "m3", ChatID: "10", SenderID: "100", Text: "deploy done", Time: now.Add(-2 * time.Minute), Media: []connector.Media{{Kind: connector.Photo, Ref: "img1"}}}},
		connector.ReactionEvent{ChatID: "10", MessageID: "m1", ActorID: "200", Emoji: "😆"},
		connector.ReactionEvent{ChatID: "10", MessageID: "m1", ActorID: "100", Emoji: "😆"},
	}
	next, _ := m.Update(EventsMsg(evts))
	return next.(Model)
}

func TestFlow(t *testing.T) {
	m, st, fc := newTestModel(t, 100, 26)
	m = seed(m)

	// New chats land in inbox; work is empty.
	if s := screen(m); !strings.Contains(s, "Empty.") {
		t.Fatalf("work should be empty:\n%s", s)
	}
	m = key(m, "3")
	s := screen(m)
	if !strings.Contains(s, "Dev Team") || !strings.Contains(s, "Jen Cruz") {
		t.Fatalf("inbox should list both chats (1:1 named from contact):\n%s", s)
	}
	if strings.Contains(s, "dup") {
		t.Fatalf("duplicate rows or older snippet won:\n%s", s)
	}
	if !strings.Contains(s, "3 Inbox 1") {
		t.Fatalf("tabs:\n%s", s)
	}

	// Tag Dev Team as work.
	m = key(m, "t")
	if s := screen(m); !strings.Contains(s, `Move "Dev Team" to`) {
		t.Fatalf("tag popup missing:\n%s", s)
	}
	m = key(m, "1")
	if sp := mustChat(t, st, "10").Space; sp != store.Work {
		t.Fatalf("space = %s, want work", sp)
	}
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("1")})
	m = run(next.(Model), cmd)
	// moving onto a chat previews it without opening (stays unread)
	if s := screen(m); !strings.Contains(s, "pushed the fix") {
		t.Fatalf("preview missing:\n%s", s)
	}
	if !mustChat(t, st, "10").Unread {
		t.Fatal("preview should not mark the chat read")
	}

	// Open: list stays on the left, chat on the right, cursor in the box.
	next, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = run(next.(Model), cmd)
	s = screen(m)
	for _, want := range []string{"Dev Team", "pushed the fix", "thanks, ping me when green", "deploy done", "Photo", "in Work", "😆 2", "│"} {
		if !strings.Contains(s, want) {
			t.Fatalf("chat view missing %q:\n%s", want, s)
		}
	}
	if mustChat(t, st, "10").Unread {
		t.Fatal("opening a chat should mark it read")
	}
	// mine is right-aligned, theirs left
	for _, l := range strings.Split(s, "\n") {
		if strings.Contains(l, "pushed the fix") && !strings.Contains(l, "😆 2") {
			t.Fatalf("reaction should sit at the end of its row: %q", l)
		}
		if strings.Contains(l, "thanks, ping me") && !strings.Contains(l, "You") {
			t.Fatalf("my messages are labeled You: %q", l)
		}

	}

	// photo arrives -> pixel thumbnail
	img := image.NewRGBA(image.Rect(0, 0, 40, 30))
	for y := 0; y < 30; y++ {
		for x := 0; x < 40; x++ {
			img.Set(x, y, color.RGBA{uint8(x * 6), uint8(y * 8), 120, 255})
		}
	}
	next, _ = m.Update(mediaMsg{ref: "img1", raw: []byte{1}, img: img})
	m = next.(Model)
	// without real-photo support, a photo is a one-line tag (o opens it)
	if s := screen(m); !strings.Contains(s, "Photo   press o to view") || strings.Contains(s, "▀") {
		t.Fatalf("photo tag:\n%s", s)
	}
	t.Logf("chat:\n%s", screen(m))

	// React: ↑ from the empty box steps into the messages; pick 😆 (2).
	m = key(m, "up")
	m = key(m, "up")
	m = key(m, "up")
	if s := screen(m); !strings.Contains(s, "▎Mark          pushed the fix") {
		t.Fatalf("selection marker missing:\n%s", s)
	}
	m = key(m, "r")
	if s := screen(m); !strings.Contains(s, "1 ❤") || !strings.Contains(s, "6 👍") {
		t.Fatalf("picker missing:\n%s", s)
	}
	t.Logf("picker:\n%s", screen(m))
	next, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("2")})
	m = run(next.(Model), cmd)
	if len(fc.reacted) != 1 || fc.reacted[0] != (reacted{"10", "m1", "100", "😆"}) {
		t.Fatalf("reacted = %+v", fc.reacted)
	}

	// Mention: back to the box, type straight away; "@je" suggests Jen.
	m = key(m, "esc")
	if !m.composing() {
		t.Fatal("esc from messages should return to typing")
	}
	m = typeText(m, "hi @je")
	if s := screen(m); !strings.Contains(s, "@Jen Cruz") {
		t.Fatalf("suggestion missing:\n%s", s)
	}
	t.Logf("mention:\n%s", screen(m))
	m = key(m, "tab")
	m = typeText(m, "see this")
	if got := m.compose.Value(); got != "hi @Jen Cruz see this" {
		t.Fatalf("draft = %q", got)
	}
	next, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = run(next.(Model), cmd)
	if len(fc.sent) != 1 || fc.sent[0].text != "hi @Jen Cruz see this" ||
		len(fc.sent[0].mentions) != 1 || fc.sent[0].mentions[0].UserID != "200" {
		t.Fatalf("sent = %+v", fc.sent)
	}

	// Draft kept when leaving: type, esc to list, come back.
	m = typeText(m, "draft")
	m = key(m, "esc")
	if m.focus != focusList || m.chat == nil {
		t.Fatal("esc should focus the list and keep the chat showing")
	}
	next, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = run(next.(Model), cmd)
	if m.compose.Value() != "draft" {
		t.Fatalf("draft lost: %q", m.compose.Value())
	}
	m.compose.SetValue("")

	// Image view from a selected photo message.
	m = key(m, "up")
	m = key(m, "o")
	if m.mode != modeImage || !strings.Contains(screen(m), "1 / 1") {
		t.Fatalf("image view:\n%s", screen(m))
	}
	m = key(m, "esc")
	m = key(m, "esc")
	m = key(m, "esc")
	if m.focus != focusList {
		t.Fatal("esc, esc, esc should end on the list")
	}

	// Search and commands.
	m = key(m, "/")
	m = typeText(m, "jen") // searching looks in every space (Jen is in inbox, we're in work)
	if s := screen(m); !strings.Contains(s, "Jen Cruz") {
		t.Fatalf("search across spaces:\n%s", s)
	}
	m = key(m, "esc")
	m = key(m, "3")
	m = key(m, ":")
	m = typeText(m, "mute")
	m = key(m, "enter")
	if !mustChat(t, st, "200").Muted {
		t.Fatal(":mute did not mute")
	}
	m = key(m, "?")
	if s := screen(m); !strings.Contains(s, "keys") || !strings.Contains(s, "react") {
		t.Fatalf("help:\n%s", s)
	}
}

func mustChat(t *testing.T, st *store.Store, id string) store.ChatRow {
	t.Helper()
	c, err := st.Chat(id)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestViewFitsTerminal(t *testing.T) {
	m, _, _ := newTestModel(t, 100, 20)
	m = seed(m)
	m = key(m, "3")
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = run(next.(Model), cmd)
	for _, w := range []int{40, 72, 120, 300} {
		next, _ := m.Update(tea.WindowSizeMsg{Width: w, Height: 14})
		mm := next.(Model)
		lines := strings.Split(mm.View(), "\n")
		if len(lines) != 14 {
			t.Fatalf("w=%d: %d lines, want 14", w, len(lines))
		}
		for i, l := range lines {
			if ansi.StringWidth(l) > w {
				t.Fatalf("w=%d line %d too wide (%d): %q", w, i, ansi.StringWidth(l), ansi.Strip(l))
			}
		}
	}
}

func init() { _ = os.Setenv("COLORTERM", "truecolor") }

func TestAttachAndSendFiles(t *testing.T) {
	m, _, fc := newTestModel(t, 100, 26)
	m = seed(m)
	m = key(m, "3")
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = run(next.(Model), cmd)

	// a file path completes after "@~/" style paths
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "photo.jpg"), []byte("\xff\xd8\xff fake jpeg"), 0o600); err != nil {
		t.Fatal(err)
	}
	m = typeText(m, "@"+dir+"/ph")
	if len(m.sugg) != 1 || !strings.HasSuffix(m.sugg[0].Label, "photo.jpg") {
		t.Fatalf("path suggestions = %+v", m.sugg)
	}
	next, cmd = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m = run(next.(Model), cmd)
	if len(m.atts) != 1 || m.atts[0].Name != "photo.jpg" || m.compose.Value() != "" {
		t.Fatalf("attach: atts=%d draft=%q", len(m.atts), m.compose.Value())
	}
	if s := screen(m); !strings.Contains(s, "photo.jpg") {
		t.Fatalf("chip missing:\n%s", s)
	}

	// drag and drop: a pasted path with an escaped space attaches too
	spaced := filepath.Join(dir, "my notes.txt")
	_ = os.WriteFile(spaced, []byte("hello"), 0o600)
	next, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(strings.ReplaceAll(spaced, " ", "\\ ")), Paste: true})
	m = run(next.(Model), cmd)
	if len(m.atts) != 2 || m.atts[1].Name != "my notes.txt" {
		t.Fatalf("drop: %+v", m.atts)
	}
	// backspace on an empty box removes the last chip
	m = key(m, "x")
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	m = next.(Model)
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	m = next.(Model)
	if len(m.atts) != 1 {
		t.Fatalf("backspace should remove one chip, have %d", len(m.atts))
	}

	m = typeText(m, "see attached")
	next, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = run(next.(Model), cmd)
	if len(fc.files) != 1 || fc.files[0].Name != "photo.jpg" || len(fc.sent) != 1 || fc.sent[0].text != "see attached" {
		t.Fatalf("files=%d sent=%+v", len(fc.files), fc.sent)
	}
	if len(m.atts) != 0 {
		t.Fatal("chips should clear after sending")
	}
}

func TestDroppedPaths(t *testing.T) {
	dir := t.TempDir()
	a, b := filepath.Join(dir, "a b.png"), filepath.Join(dir, "c.txt")
	_ = os.WriteFile(a, []byte("x"), 0o600)
	_ = os.WriteFile(b, []byte("x"), 0o600)
	cases := map[string]int{
		strings.ReplaceAll(a, " ", "\\ ") + " " + b: 2,
		"'" + a + "'": 1,
		"hello world": 0,
		dir:           0, // folders aren't files
	}
	for in, want := range cases {
		got, ok := droppedPaths(in)
		if (want == 0) == ok || len(got) != want && want > 0 {
			t.Errorf("droppedPaths(%q) = %v, %v; want %d paths", in, got, ok, want)
		}
	}
}

func TestNoStrayLineBreaks(t *testing.T) {
	m, _, _ := newTestModel(t, 90, 20)
	evts := []any{
		connector.ContactEvent{ID: "5", Name: "Line\nBreak"},
		connector.ChatEvent{Chat: connector.Chat{ID: "5", Snippet: "a\nb", LastActivity: time.Now()}},
		connector.StatusEvent{Text: "oops", Err: errors.New("multi\nline\nerror")},
	}
	next, _ := m.Update(EventsMsg(evts))
	m = next.(Model)
	m = key(m, "3")
	if n := strings.Count(m.View(), "\n"); n != 19 {
		t.Fatalf("view has %d line breaks, want 19 (exactly 20 rows)", n)
	}
}

func TestFolderManager(t *testing.T) {
	m, st, _ := newTestModel(t, 100, 26)
	m = seed(m)
	m = key(m, "f")
	if s := screen(m); !strings.Contains(s, "Folders") || !strings.Contains(s, "(built in)") {
		t.Fatalf("manager:\n%s", s)
	}
	m = key(m, "a")
	m = typeText(m, "Family")
	m = key(m, "enter")
	if s := screen(m); !strings.Contains(s, "3 Family") {
		t.Fatalf("new folder tab missing:\n%s", s)
	}
	m = key(m, "K") // move Family before Personal
	if s := screen(m); !strings.Contains(s, "2 Family") {
		t.Fatalf("reorder:\n%s", s)
	}
	m = key(m, "r")
	m.input.SetValue("")
	m = typeText(m, "Fam")
	m = key(m, "enter")
	if sp := st.Spaces(); sp[1] != "Fam" {
		t.Fatalf("rename: %v", sp)
	}
	m = key(m, "d")
	m = key(m, "y")
	if sp := st.Spaces(); len(sp) != 3 {
		t.Fatalf("delete: %v", sp)
	}
	m = key(m, "esc")
	// chats can be moved into any folder by number
	m = key(m, "3") // Inbox
	moved := m.chats[m.cursor].ID
	m = key(m, "t")
	if s := screen(m); !strings.Contains(s, "Personal") || !strings.Contains(s, "i  ") {
		t.Fatalf("tag popup:\n%s", s)
	}
	m = key(m, "2")
	if c, _ := st.Chat(moved); c.Space != "Personal" {
		t.Fatal("chat should have moved to Personal")
	}
}
