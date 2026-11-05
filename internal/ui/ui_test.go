package ui

import (
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"io"
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
	reply    *connector.ReplyRef
}

type reacted struct{ chat, msg, sender, emoji string }

type fakeConn struct {
	known       []connector.Chat
	directory   []connector.Person // what SearchPeople finds
	opened      []string
	looked      []string
	failFile    string // SendFile fails for this name
	failSend    bool   // Send fails
	media       []byte // what FetchMedia returns
	mediaErr    map[string]error
	fetched     []string
	onLoadOlder func()
	files       []connector.Attachment
	sent        []sent
	reacted     []reacted
}

func (f *fakeConn) Start(context.Context) error { return nil }
func (f *fakeConn) Events() <-chan any          { return nil }
func (f *fakeConn) SelfID() string              { return "1" }
func (f *fakeConn) LoadOlder(context.Context, string, time.Time, string) error {
	if f.onLoadOlder != nil {
		f.onLoadOlder()
	}
	return nil
}
func (f *fakeConn) Send(_ context.Context, _ string, text string, ms []connector.Mention, reply *connector.ReplyRef) error {
	if f.failSend {
		return errors.New("network down")
	}
	f.sent = append(f.sent, sent{text, ms, reply})
	return nil
}
func (f *fakeConn) KnowChat(c connector.Chat) { f.known = append(f.known, c) }
func (f *fakeConn) SearchPeople(_ context.Context, q string) ([]connector.Person, error) {
	var out []connector.Person
	for _, p := range f.directory {
		if strings.Contains(strings.ToLower(p.Name), strings.ToLower(q)) {
			out = append(out, p)
		}
	}
	return out, nil
}
func (f *fakeConn) LookupPeople(_ context.Context, ids []string) { f.looked = append(f.looked, ids...) }
func (f *fakeConn) OpenDirect(_ context.Context, userID string) (string, error) {
	f.opened = append(f.opened, userID)
	return userID, nil
}
func (f *fakeConn) SendFile(_ context.Context, _ string, a connector.Attachment) error {
	if a.Name == f.failFile {
		return errors.New("upload rejected")
	}
	f.files = append(f.files, a)
	return nil
}
func (f *fakeConn) React(_ context.Context, chat, msg, sender, emoji string) error {
	f.reacted = append(f.reacted, reacted{chat, msg, sender, emoji})
	return nil
}
func (f *fakeConn) FetchMedia(_ context.Context, md connector.Media) ([]byte, error) {
	f.fetched = append(f.fetched, md.Ref)
	if err := f.mediaErr[md.Ref]; err != nil {
		return nil, err
	}
	return f.media, nil
}
func (f *fakeConn) Close() {}

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
	if !strings.Contains(s, "Inbox  1 ") && !strings.Contains(s, "Inb…  1 ") {
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
	for _, want := range []string{"Dev Team", "pushed the fix", "thanks, ping me when green", "deploy done", "View photo", "in Work", "😆 2", "│"} {
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
	// without real-photo support, a photo is a clean tag (no blocky preview)
	if s := screen(m); !strings.Contains(s, "▸ View photo") || strings.Contains(s, "▀") {
		t.Fatalf("photo tag:\n%s", s)
	}
	if nm, _ := m.runCommand("images blocks"); !strings.Contains(nm.(Model).toast, "unknown command") {
		t.Fatalf(":images blocks should be gone, got %q", nm.(Model).toast)
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

	// o on a photo pops up the real photo in the system viewer
	var previewed string
	previewFiles = func(p []string) error { previewed = p[0]; return nil }
	defer func() { previewFiles = openFilesOS }()
	m = key(m, "up")
	next, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("o")})
	m = run(next.(Model), cmd)
	if previewed == "" || m.mode == modeImage {
		t.Fatalf("o should open the viewer popup (got %q, mode %v)", previewed, m.mode)
	}
	if b, _ := os.ReadFile(previewed); len(b) != 1 {
		t.Fatalf("viewer got the wrong file contents: %v", b)
	}
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

	// a file path completes after "@~/" style paths; the file becomes a token
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "photo.jpg"), []byte("\xff\xd8\xff fake jpeg"), 0o600); err != nil {
		t.Fatal(err)
	}
	m = typeText(m, "look @"+dir+"/ph")
	if len(m.sugg) != 1 || !strings.HasSuffix(m.sugg[0].Label, "photo.jpg") {
		t.Fatalf("path suggestions = %+v", m.sugg)
	}
	next, cmd = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m = run(next.(Model), cmd)
	if got := m.compose.Value(); got != "look [Image #1] " {
		t.Fatalf("draft = %q", got)
	}
	if s := screen(m); !strings.Contains(s, "[Image #1] photo.jpg") {
		t.Fatalf("token legend missing:\n%s", s)
	}

	// drag and drop: a pasted path with an escaped space attaches too
	spaced := filepath.Join(dir, "my notes.txt")
	_ = os.WriteFile(spaced, []byte("hello"), 0o600)
	next, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(strings.ReplaceAll(spaced, " ", "\\ ")), Paste: true})
	m = run(next.(Model), cmd)
	if got := m.compose.Value(); got != "look [Image #1] [File #2] " {
		t.Fatalf("draft after drop = %q", got)
	}
	// backspace right after a token removes the whole token (and its file)
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyBackspace}) // the space
	m = next.(Model)
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyBackspace}) // [File #2]
	m = next.(Model)
	if got := m.compose.Value(); got != "look [Image #1] " {
		t.Fatalf("backspace should drop the token, draft = %q", got)
	}

	// a new line with alt+enter, then send
	m = typeText(m, "see")
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter, Alt: true})
	m = next.(Model)
	m = typeText(m, "attached")
	if m.compose.Height() != 2 {
		t.Fatalf("box should grow to 2 lines, is %d", m.compose.Height())
	}
	next, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = run(next.(Model), cmd)
	if len(fc.files) != 1 || fc.files[0].Name != "photo.jpg" || len(fc.sent) != 1 || fc.sent[0].text != "look see\nattached" {
		t.Fatalf("files=%d sent=%+v", len(fc.files), fc.sent)
	}
	if len(m.atts) != 0 || m.compose.Value() != "" {
		t.Fatal("draft should clear after sending")
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
	m, st, _ := newTestModel(t, 170, 26) // wide enough for every tab name
	m = seed(m)
	m = key(m, "f")
	if s := screen(m); !strings.Contains(s, "Folders") || !strings.Contains(s, "(built in)") {
		t.Fatalf("manager:\n%s", s)
	}
	m = key(m, "a")
	m = typeText(m, "Family")
	m = key(m, "enter")
	if sp := st.Spaces(); len(sp) != 4 || sp[2] != "Family" {
		t.Fatalf("new folder missing: %v", sp)
	}
	// move mode works with plain keys (Terminal.app eats shift+arrows)
	m = key(m, "m")
	m = key(m, "up")
	m = key(m, "up") // Family (3rd) moves up twice, to first
	m = key(m, "enter")
	if sp := st.Spaces(); sp[0] != "Family" {
		t.Fatalf("move mode: %v", sp)
	}
	m = key(m, "m")
	m = key(m, "down")
	m = key(m, "down") // can't pass Inbox
	m = key(m, "down")
	m = key(m, "enter")
	if sp := st.Spaces(); sp[len(sp)-1] != store.Inbox || sp[2] != "Family" {
		t.Fatalf("Inbox must stay last: %v", sp)
	}
	m = key(m, "m")
	m = key(m, "up")
	m = key(m, "enter")
	m = key(m, "K") // shortcut still works too
	if sp := st.Spaces(); sp[0] != "Family" {
		t.Fatalf("reorder: %v", sp)
	}
	m = key(m, "r")
	m.input.SetValue("")
	m = typeText(m, "Fam")
	m = key(m, "enter")
	if sp := st.Spaces(); sp[0] != "Fam" {
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

func TestSoundPicker(t *testing.T) {
	var played []string
	playSound = func(name string, vol int) { played = append(played, fmt.Sprintf("%s@%d", name, vol)) }
	defer func() { playSound = playSoundOS }()

	m, st, _ := newTestModel(t, 100, 30)
	if m.soundVol != 100 {
		t.Fatalf("default volume = %d, want 100", m.soundVol)
	}
	m = key(m, ":")
	m = typeText(m, "sound")
	m = key(m, "enter")
	if !m.soundPick || !strings.Contains(screen(m), "New message sound") {
		t.Fatalf("picker not open:\n%s", screen(m))
	}
	press := func(k tea.KeyMsg) {
		next, cmd := m.Update(k)
		m = next.(Model)
		if cmd != nil {
			cmd()
		}
	}
	press(tea.KeyMsg{Type: tea.KeyDown})
	press(tea.KeyMsg{Type: tea.KeyRight})
	press(tea.KeyMsg{Type: tea.KeyRight})
	if m.soundVol != 120 || len(played) < 2 {
		t.Fatalf("vol=%d played=%v", m.soundVol, played)
	}
	chosen := m.soundName
	press(tea.KeyMsg{Type: tea.KeyEnter})
	if st.Setting("sound_volume", "") != "120" || st.Setting("sound_name", "") != chosen {
		t.Fatalf("not saved: %s %s", st.Setting("sound_volume", ""), st.Setting("sound_name", ""))
	}
	// esc throws changes away
	m = key(m, ":")
	m = typeText(m, "sound")
	m = key(m, "enter")
	press(tea.KeyMsg{Type: tea.KeyLeft})
	m = key(m, "esc")
	if m.soundVol != 120 || m.soundName != chosen {
		t.Fatalf("esc should restore: %s@%d", m.soundName, m.soundVol)
	}
}

func TestOpenPhotoWithoutSelecting(t *testing.T) {
	var opened []string
	previewFiles = func(p []string) error { opened = append(opened, p...); return nil }
	defer func() { previewFiles = openFilesOS }()

	m, _, _ := newTestModel(t, 100, 26)
	m = seed(m)
	m = key(m, "3")
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = run(next.(Model), cmd)
	next, _ = m.Update(mediaMsg{ref: "img1", raw: []byte{1, 2}, img: image.NewRGBA(image.Rect(0, 0, 4, 4))})
	m = next.(Model)

	// ctrl+o while typing opens the newest photo
	next, cmd = m.Update(tea.KeyMsg{Type: tea.KeyCtrlO})
	m = run(next.(Model), cmd)
	if len(opened) != 1 {
		t.Fatalf("ctrl+o should open the newest photo, opened %v", opened)
	}

	// clicking the "View photo" line opens it too
	y := -1
	for i, l := range strings.Split(screen(m), "\n") {
		if strings.Contains(l, "View photo") {
			y = i
		}
	}
	if y < 0 {
		t.Fatalf("no View photo line:\n%s", screen(m))
	}
	x := m.leftWidth() + 20
	for _, a := range []tea.MouseAction{tea.MouseActionPress, tea.MouseActionRelease} {
		next, cmd = m.Update(tea.MouseMsg{X: x, Y: y, Action: a, Button: tea.MouseButtonLeft})
		m = run(next.(Model), cmd)
	}
	if len(opened) != 2 {
		t.Fatalf("click should open the photo, opened %v", opened)
	}

	// clicking a plain text message selects it instead
	for i, l := range strings.Split(screen(m), "\n") {
		if strings.Contains(l, "thanks, ping me") {
			y = i
		}
	}
	for _, a := range []tea.MouseAction{tea.MouseActionPress, tea.MouseActionRelease} {
		next, cmd = m.Update(tea.MouseMsg{X: x, Y: y, Action: a, Button: tea.MouseButtonLeft})
		m = run(next.(Model), cmd)
	}
	if !m.selecting || m.msgs[m.selIndex()].ID != "m2" {
		t.Fatalf("click on text should select it (selecting=%v)", m.selecting)
	}
}

func TestPhotoAlbum(t *testing.T) {
	var opened [][]string
	previewFiles = func(p []string) error { opened = append(opened, p); return nil }
	defer func() { previewFiles = openFilesOS }()

	m, _, _ := newTestModel(t, 100, 26)
	now := time.Now()
	evts := []any{
		connector.ContactEvent{ID: "300", Name: "Ann Lim"},
		connector.ChatEvent{Chat: connector.Chat{ID: "50", Name: "Market", Group: true, LastActivity: now}},
		connector.MessageEvent{Message: connector.Message{ID: "t", ChatID: "50", SenderID: "300", Text: "new stock!", Time: now.Add(-3 * time.Minute)}},
	}
	// 3 photos sent back to back, then one more much later
	for i, ref := range []string{"p1", "p2", "p3"} {
		evts = append(evts, connector.MessageEvent{Message: connector.Message{ID: ref, ChatID: "50", SenderID: "300",
			Time: now.Add(-2*time.Minute + time.Duration(i)*time.Second), Media: []connector.Media{{Kind: connector.Photo, Ref: ref}}}})
	}
	evts = append(evts, connector.MessageEvent{Message: connector.Message{ID: "p4", ChatID: "50", SenderID: "300",
		Time: now, Media: []connector.Media{{Kind: connector.Photo, Ref: "p4"}}}})
	next, _ := m.Update(EventsMsg(evts))
	m = next.(Model)
	m = key(m, "3")
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = run(next.(Model), cmd)

	s := screen(m)
	if strings.Count(s, "View 3 photos") != 1 || strings.Count(s, "▸ View photo") != 1 {
		t.Fatalf("want one album line and one single photo:\n%s", s)
	}
	for _, ref := range []string{"p1", "p2", "p3", "p4"} {
		next, _ = m.Update(mediaMsg{ref: ref, raw: []byte(ref), img: image.NewRGBA(image.Rect(0, 0, 2, 2))})
		m = next.(Model)
	}

	// click the album: all three open together, in order
	y := -1
	for i, l := range strings.Split(screen(m), "\n") {
		if strings.Contains(l, "View 3 photos") {
			y = i
		}
	}
	for _, a := range []tea.MouseAction{tea.MouseActionPress, tea.MouseActionRelease} {
		next, cmd = m.Update(tea.MouseMsg{X: m.leftWidth() + 20, Y: y, Action: a, Button: tea.MouseButtonLeft})
		m = run(next.(Model), cmd)
	}
	if len(opened) != 1 || len(opened[0]) != 3 {
		t.Fatalf("album should open 3 photos at once: %v", opened)
	}
	for i, want := range []string{"p1", "p2", "p3"} {
		if b, _ := os.ReadFile(opened[0][i]); string(b) != want {
			t.Fatalf("photo %d = %q, want %q", i+1, b, want)
		}
	}

	// in a terminal that draws photos, the full view flips through the album only
	m.kitty = newKittyImages(io.Discard, t.TempDir())
	m.focus, m.selecting = focusChat, true
	m.selectIndex(1) // p1
	m = key(m, "o")
	if m.mode != modeImage || !strings.Contains(screen(m), "1 / 3") {
		t.Fatalf("gallery view:\n%s", screen(m))
	}
	m = key(m, "right")
	m = key(m, "right")
	m = key(m, "right") // stops at the album's end
	if !strings.Contains(screen(m), "3 / 3") {
		t.Fatalf("gallery should stay within the album:\n%s", screen(m))
	}
}

func TestPeopleSearch(t *testing.T) {
	m, st, fc := newTestModel(t, 100, 30)
	fc.directory = []connector.Person{{ID: "900", Name: "Markus Tan"}, {ID: "100", Name: "Mark Reyes"}}
	m = seed(m)
	m = key(m, "/")
	var cmd tea.Cmd
	for _, r := range "mark" {
		var next tea.Model
		next, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = next.(Model)
	}
	// your contact shows straight away (no chat with Mark yet)
	if s := screen(m); !strings.Contains(s, "@ Mark Reyes") || !strings.Contains(s, "Person · enter to message") {
		t.Fatalf("contact not listed:\n%s", s)
	}
	m = run(m, cmd) // the search pause, then Messenger's results
	s := screen(m)
	if !strings.Contains(s, "@ Markus Tan") || strings.Count(s, "@ Mark Reyes") != 1 {
		t.Fatalf("Messenger results missing or duplicated:\n%s", s)
	}

	// move onto Markus (arrows work inside the search box) and open his chat
	for i := 0; i < 10 && (m.personAt(m.cursor) == nil || m.personAt(m.cursor).Name != "Markus Tan"); i++ {
		m = key(m, "down")
	}
	if !strings.Contains(screen(m), "Press enter to message Markus Tan") {
		t.Fatalf("hint missing:\n%s", screen(m))
	}
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = run(next.(Model), cmd)
	if len(fc.opened) != 1 || fc.opened[0] != "900" || m.chat == nil || m.chat.ID != "900" || !m.composing() {
		t.Fatalf("should open a 1:1 with Markus ready to type (opened %v)", fc.opened)
	}
	if m.chat.Display != "Markus Tan" || m.query != "" {
		t.Fatalf("chat = %q, query = %q", m.chat.Display, m.query)
	}
	m = typeText(m, "hey")
	next, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	_ = run(next.(Model), cmd)
	if len(fc.sent) != 1 || fc.sent[0].text != "hey" {
		t.Fatalf("sent = %+v", fc.sent)
	}
	if c, _ := st.Chat("900"); !c.Encrypted {
		t.Fatal("a 1:1 opened from search is an encrypted chat")
	}
}

func TestSharedLinkCard(t *testing.T) {
	var opened string
	openURL = func(u string) error { opened = u; return nil }
	defer func() { openURL = openFile }()

	m, _, _ := newTestModel(t, 100, 26)
	now := time.Now()
	next, _ := m.Update(EventsMsg{
		connector.ContactEvent{ID: "400", Name: "Sherwin Cruz"},
		connector.ChatEvent{Chat: connector.Chat{ID: "70", Name: "Oplan Budulan", Group: true, LastActivity: now}},
		connector.MessageEvent{Message: connector.Message{ID: "x1", ChatID: "70", SenderID: "400", Time: now,
			Media: []connector.Media{{Kind: connector.Link, Title: "YOUR FAVORITE TOYS ARE ON SALE! 🎁🧸", Source: "Facebook", URL: "https://www.facebook.com/p/1"}}}},
	})
	m = next.(Model)
	m = key(m, "3")
	if s := screen(m); !strings.Contains(s, "Shared: YOUR FA") {
		t.Fatalf("list snippet:\n%s", s)
	}
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = run(next.(Model), cmd)
	s := screen(m)
	if !strings.Contains(s, "▸ YOUR FAVORITE TOYS ARE ON SALE!") || !strings.Contains(s, "www.facebook.com · click to open") {
		t.Fatalf("link card missing:\n%s", s)
	}
	y := -1
	for i, l := range strings.Split(s, "\n") {
		if strings.Contains(l, "FAVORITE TOYS") && strings.Contains(l, "▸") {
			y = i
		}
	}
	for _, a := range []tea.MouseAction{tea.MouseActionPress, tea.MouseActionRelease} {
		next, cmd = m.Update(tea.MouseMsg{X: m.leftWidth() + 20, Y: y, Action: a, Button: tea.MouseButtonLeft})
		m = run(next.(Model), cmd)
	}
	if opened != "https://www.facebook.com/p/1" {
		t.Fatalf("click should open the link, opened %q", opened)
	}
}

func TestHostileTextIsDefused(t *testing.T) {
	m, _, _ := newTestModel(t, 100, 26)
	now := time.Now()
	evil := "hi \x1b]52;c;ZXZpbA==\x07\x1b[2J\x1b]0;pwned\x07 ‮gpj.exe"
	next, _ := m.Update(EventsMsg{
		connector.ContactEvent{ID: "666", Name: "Mallory\x1b[31m"},
		connector.ChatEvent{Chat: connector.Chat{ID: "66", Name: "Trap\x1b]0;x\x07", Snippet: evil, LastActivity: now}},
		connector.MessageEvent{Message: connector.Message{ID: "e1", ChatID: "66", SenderID: "666", Text: evil, Time: now}},
		connector.ReactionEvent{ChatID: "66", MessageID: "e1", ActorID: "666", Emoji: "\x1b[2J"},
		connector.StatusEvent{Text: "x", Err: errors.New("bad\x1b]52;c;AAAA\x07")},
	})
	m = next.(Model)
	m = key(m, "3")
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = run(next.(Model), cmd)
	v := m.View()
	for _, bad := range []string{"\x1b]52", "\x1b]0;", "\x1b[2J", "\x1b[31m", "‮", "\x07"} {
		if strings.Contains(v, bad) {
			t.Fatalf("screen still carries %q", bad)
		}
	}
}

func TestSearchKeepsYourPick(t *testing.T) {
	m, _, fc := newTestModel(t, 100, 30)
	// Messenger's results arrive later and include people sorted before Mark
	fc.directory = []connector.Person{{ID: "901", Name: "Ann Markson"}, {ID: "902", Name: "Bea Marks"}}
	m = seed(m)
	m = key(m, "/")
	var cmd tea.Cmd
	for _, r := range "mark" {
		var next tea.Model
		next, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = next.(Model)
	}
	// pick Mark Reyes (a contact, shown right away) while still in the box
	for i := 0; i < 10 && m.rowKey(m.cursor) != "p:100"; i++ {
		m = key(m, "down")
	}
	m = run(m, cmd) // now Messenger's results land
	if got := m.rowKey(m.cursor); got != "p:100" {
		t.Fatalf("highlight jumped to %s", got)
	}
	// enter in the search box opens it straight away
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = run(next.(Model), cmd)
	if m.chat == nil || m.chat.ID != "100" || !m.composing() {
		t.Fatalf("enter should open Mark's chat ready to type, got %+v", m.chat)
	}
}

func TestUnreadFolder(t *testing.T) {
	m, st, _ := newTestModel(t, 170, 26)
	m = seed(m) // Dev Team is unread
	_ = st.SetSpace("10", store.Work)
	m.reloadChats()
	if s := screen(m); !strings.Contains(s, "Unread  1 ") || !strings.Contains(s, "Work  1 ") {
		t.Fatalf("unread tab/badge missing:\n%s", s)
	}
	m = key(m, "2") // Personal: empty, but Unread still collects it
	m = key(m, "u")
	if s := screen(m); !strings.Contains(s, "Dev Team") {
		t.Fatalf("Unread should list unread chats from every folder:\n%s", s)
	}
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = run(next.(Model), cmd)
	m = key(m, "esc")
	if s := screen(m); !strings.Contains(s, "All caught up.") {
		t.Fatalf("after reading, Unread should say all caught up:\n%s", s)
	}
	m = key(m, "1")
	if s := screen(m); strings.Contains(s, "Unread") {
		t.Fatalf("the Unread tab should hide once nothing is unread:\n%s", s)
	}
}

func TestReplies(t *testing.T) {
	m, _, fc := newTestModel(t, 110, 30)
	m = seed(m)
	m = key(m, "3")
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = run(next.(Model), cmd)

	// pick "pushed the fix" (Mark) and press enter to reply
	m = key(m, "up")
	m = key(m, "up")
	m = key(m, "up")
	m = key(m, "enter")
	if !m.composing() || m.replyTo == nil || m.replyTo.ID != "m1" {
		t.Fatalf("enter on a message should start a reply (replyTo=%+v)", m.replyTo)
	}
	if s := screen(m); !strings.Contains(s, "↩ Replying to Mark: pushed the fix") {
		t.Fatalf("reply banner missing:\n%s", s)
	}
	m = typeText(m, "nice")
	next, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = run(next.(Model), cmd)
	if len(fc.sent) != 1 || fc.sent[0].reply == nil || fc.sent[0].reply.ID != "m1" || fc.sent[0].reply.SenderID != "100" {
		t.Fatalf("reply not sent: %+v", fc.sent)
	}
	if m.replyTo != nil {
		t.Fatal("reply should clear after sending")
	}

	// esc cancels a reply without leaving the chat
	m = key(m, "up")
	m = key(m, "enter")
	m = key(m, "esc")
	if m.replyTo != nil || !m.composing() {
		t.Fatal("esc should cancel the reply and keep you typing")
	}

	// a reply that arrives shows what it answers
	next, _ = m.Update(EventsMsg{connector.MessageEvent{Message: connector.Message{
		ID: "m9", ChatID: "10", SenderID: "200", Text: "agreed", Time: time.Now(), ReplyToID: "m1"}}})
	m = next.(Model)
	if s := screen(m); !strings.Contains(s, "↩ Mark: pushed the fix") || !strings.Contains(s, "agreed") {
		t.Fatalf("incoming reply should quote its message:\n%s", s)
	}
}

func TestOpensOnFirstFolder(t *testing.T) {
	_, st, _ := newTestModel(t, 120, 30)
	if got := firstFolder(st); got != store.Work {
		t.Fatalf("first = %q, want Work", got)
	}
	_ = st.MoveSpace(store.Personal, -1) // Personal now first
	if got := firstFolder(st); got != store.Personal {
		t.Fatalf("after reorder first = %q, want Personal", got)
	}
}

func TestInboxCantBeDeleted(t *testing.T) {
	m, st, _ := newTestModel(t, 120, 30)
	m = key(m, "f")
	for i := 0; i < 10; i++ {
		m = key(m, "down") // Inbox is last
	}
	if !strings.Contains(m.View(), "Inbox is built in") {
		t.Fatal("footer should say Inbox is built in")
	}
	m = key(m, "d")
	m = key(m, "y")
	found := false
	for _, sp := range st.Spaces() {
		found = found || sp == store.Inbox
	}
	if !found || st.DeleteSpace(store.Inbox) == nil {
		t.Fatal("Inbox must not be deletable")
	}
}

func TestGroupSearch(t *testing.T) {
	m, st, fc := newTestModel(t, 100, 30)
	fc.directory = []connector.Person{{ID: "777", Name: "Jayson & Friends", Group: true}}
	m = seed(m)
	m = key(m, "/")
	var cmd tea.Cmd
	for _, r := range "jayson & friends" {
		var next tea.Model
		next, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = next.(Model)
	}
	m = run(m, cmd)
	if s := screen(m); !strings.Contains(s, "Jayson & Friends") || !strings.Contains(s, "Group chat · enter to") {
		t.Fatalf("group not listed:\n%s", s)
	}
	for i := 0; i < 10 && (m.personAt(m.cursor) == nil || !m.personAt(m.cursor).Group); i++ {
		m = key(m, "down")
	}
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = run(next.(Model), cmd)
	if len(fc.opened) != 0 {
		t.Fatalf("a group isn't a 1:1: opened %v", fc.opened)
	}
	if m.chat == nil || m.chat.ID != "777" || m.chat.Display != "Jayson & Friends" || !m.chat.Group || !m.composing() {
		t.Fatalf("group not opened ready to type: %+v", m.chat)
	}
	if c, _ := st.Chat("777"); c.Encrypted {
		t.Fatal("a plain group must not be marked encrypted")
	}
}

func TestImagesCommandSaysWhatHappened(t *testing.T) {
	m, st, _ := newTestModel(t, 160, 30)
	m.kitty = nil // a terminal without photo support
	for _, c := range []string{"images off", "images on"} {
		next, _ := m.runCommand(c)
		m = next.(Model)
		if !strings.Contains(m.toast, "can't show photos") {
			t.Fatalf(":%s gave no feedback: %q", c, m.toast)
		}
	}
	next, _ := m.runCommand("images off")
	if m = next.(Model); m.imagesOn || st.Setting(settingImage, "on") != "off" {
		t.Fatal(":images off didn't stick")
	}
}

func TestUnknownSenderNameIsLookedUp(t *testing.T) {
	m, st, fc := newTestModel(t, 120, 30)
	now := time.Now()
	_ = st.UpsertChat(connector.Chat{ID: "g1", Name: "Jayson & Friends", Group: true, LastActivity: now})
	_, _ = st.AddMessage(connector.Message{ID: "x1", ChatID: "g1", SenderID: "100000109217940", Text: "Mga 5 years", Time: now}, false)
	m.reloadChats()
	c, _ := st.Chat("g1")
	m = run(m, m.openChat(c))
	if len(fc.looked) != 1 || fc.looked[0] != "100000109217940" {
		t.Fatalf("looked up %v", fc.looked)
	}
	_ = m.reloadMessages() // asks only once per run
	if len(fc.looked) != 1 {
		t.Fatalf("asked again: %v", fc.looked)
	}
	next, _ := m.Update(EventsMsg{connector.ContactEvent{ID: "100000109217940", Name: "Ed Hipolito"}})
	if s := screen(next.(Model)); !strings.Contains(s, "Ed") || strings.Contains(s, "…7940") {
		t.Fatalf("name not shown:\n%s", s)
	}
}

func TestPlaceholdersRetriedAtStart(t *testing.T) {
	m, st, _ := newTestModel(t, 120, 30)
	now := time.Now()
	_ = st.UpsertChat(connector.Chat{ID: "j", LastActivity: now, Encrypted: true})
	_, _ = st.AddMessage(connector.Message{ID: "m1", ChatID: "j", SenderID: "2", Time: now,
		Media: []connector.Media{{Kind: connector.Missing}}}, false)
	next, cmd := m.Update(startedMsg{})
	m = next.(Model)
	if !m.refilling["j"] || cmd == nil {
		t.Fatal("a placeholder from an earlier run should start backup checks")
	}
	// checks keep going well past the first few, then stop
	if got := refillAfter[min(maxRefills-1, len(refillAfter)-1)]; got != 5*time.Minute {
		t.Fatalf("late checks every %v, want 5m", got)
	}
}

func TestFailedFileKeepsDraft(t *testing.T) {
	m, st, fc := newTestModel(t, 120, 30)
	fc.failFile = "big.zip"
	now := time.Now()
	_ = st.UpsertChat(connector.Chat{ID: "c1", Name: "Test", LastActivity: now})
	m.reloadChats()
	c, _ := st.Chat("c1")
	m = run(m, m.openChat(c))
	next, _ := m.Update(attachedMsg{files: []connector.Attachment{
		{Name: "a.png", Mime: "image/png", Data: []byte("x")},
		{Name: "big.zip", Mime: "application/zip", Data: []byte("y")},
	}})
	m = typeText(next.(Model), "here you go")
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = run(next.(Model), cmd)
	if len(fc.files) != 1 || fc.files[0].Name != "a.png" || len(fc.sent) != 0 {
		t.Fatalf("files=%v sent=%v", fc.files, fc.sent)
	}
	if v := m.compose.Value(); !strings.Contains(v, "[File #2]") || !strings.Contains(v, "here you go") || strings.Contains(v, "#1") {
		t.Fatalf("draft not restored: %q", v)
	}
	if len(m.atts) != 1 || m.atts[0].file.Name != "big.zip" {
		t.Fatalf("atts = %+v", m.atts)
	}
	// (run also fires the toast's clear timer, so check the message directly)
	if nm, _ := m.Update(sentMsg{err: errors.New("x"), chatID: "c9", restore: "y"}); !strings.Contains(nm.(Model).toast, "back in the box") {
		t.Fatalf("toast = %q", nm.(Model).toast)
	}
	// sending again sends the rest
	fc.failFile = ""
	next, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	_ = run(next.(Model), cmd)
	if len(fc.files) != 2 || len(fc.sent) != 1 || fc.sent[0].text != "here you go" {
		t.Fatalf("resend: files=%d sent=%+v", len(fc.files), fc.sent)
	}
}

func TestNotSentShowsWithoutInput(t *testing.T) {
	m, st, _ := newTestModel(t, 120, 30)
	_ = st.UpsertChat(connector.Chat{ID: "c1", Name: "Test", LastActivity: time.Now()})
	_, _ = st.AddMessage(connector.Message{ID: "pending:1", ChatID: "c1", SenderID: "1", FromMe: true, Pending: true,
		Text: "hello", Time: time.Now().Add(-44 * time.Second)}, false)
	m.reloadChats()
	c, _ := st.Chat("c1")
	cmd := m.openChat(c)
	if m.checkAt.IsZero() || time.Until(m.checkAt) > 2*time.Second {
		t.Fatalf("no timer for the pending message (checkAt %v)", m.checkAt)
	}
	if strings.Contains(screen(m), "not sent") {
		t.Fatal("too early for not sent")
	}
	m = run(m, cmd) // the timer fires: no key pressed
	if !strings.Contains(screen(m), "not sent") {
		t.Fatalf("not sent missing after the timer:\n%s", screen(m))
	}
}

func TestUnsentMessages(t *testing.T) {
	m, st, fc := newTestModel(t, 140, 30)
	_ = st.UpsertChat(connector.Chat{ID: "c1", Name: "Test", LastActivity: time.Now()})
	m.reloadChats()
	c, _ := st.Chat("c1")
	m = run(m, m.openChat(c))

	// a send that fails outright: the text comes back
	fc.failSend = true
	m = typeText(m, "are you there")
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = run(next.(Model), cmd)
	if m.compose.Value() != "are you there" {
		t.Fatalf("text not restored: %q", m.compose.Value())
	}
	fc.failSend = false
	m.compose.SetValue("")

	// one stuck on "not sent": retry it
	_, _ = st.AddMessage(connector.Message{ID: "pending:7", ChatID: "c1", SenderID: "1", FromMe: true, Pending: true,
		Text: "hello?", Time: time.Now().Add(-time.Minute)}, false)
	_ = m.reloadMessages()
	m = key(m, "up") // select it
	if s := screen(m); !strings.Contains(s, "send again") || !strings.Contains(s, "not sent") {
		t.Fatalf("no retry hint:\n%s", s)
	}
	next, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = run(next.(Model), cmd)
	if len(fc.sent) != 1 || fc.sent[0].text != "hello?" {
		t.Fatalf("retry sent %+v", fc.sent)
	}
	if strings.Contains(screen(m), "not sent") {
		t.Fatal("old copy still there")
	}

	// another: delete it
	_, _ = st.AddMessage(connector.Message{ID: "pending:8", ChatID: "c1", SenderID: "1", FromMe: true, Pending: true,
		Text: "nvm", Time: time.Now().Add(-time.Minute)}, false)
	_ = m.reloadMessages()
	m = key(m, "up")
	next, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	m = run(next.(Model), cmd)
	if strings.Contains(screen(m), "nvm") || len(fc.sent) != 1 {
		t.Fatalf("delete failed:\n%s", screen(m))
	}
}

func TestSafeFileName(t *testing.T) {
	for in, want := range map[string]string{
		"report.pdf":       "report.pdf",
		"../../etc/passwd": "_.._etc_passwd",
		".hidden":          "hidden",
		"-rf":              "rf",
		"a\x1b[31mred.txt": "a[31mred.txt",
		"":                 "file",
		"photo‮gpj.exe":    "photogpj.exe",
	} {
		if got := safeFileName(in); got != want {
			t.Errorf("safeFileName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestReceivedFileSaves(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir) // Windows
	_ = os.MkdirAll(filepath.Join(dir, "Downloads"), 0o700)
	var revealed string
	revealFile = func(p string) error { revealed = p; return nil }
	defer func() { revealFile = func(string) error { return nil } }()

	m, st, fc := newTestModel(t, 120, 30)
	fc.media = []byte("zipdata")
	now := time.Now()
	_ = st.UpsertChat(connector.Chat{ID: "c1", Name: "Raine", LastActivity: now, Encrypted: true})
	_, _ = st.AddMessage(connector.Message{ID: "f1", ChatID: "c1", SenderID: "2", Time: now,
		Media: []connector.Media{{Kind: connector.File, Name: "tuon.zip", Ref: "wa:doc:x"}}}, false)
	m.reloadChats()
	c, _ := st.Chat("c1")
	m = run(m, m.openChat(c))
	if s := screen(m); !strings.Contains(s, "tuon.zip") || !strings.Contains(s, "to save") {
		t.Fatalf("file row:\n%s", s)
	}
	m = key(m, "up")
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})
	_ = run(next.(Model), cmd)
	want := filepath.Join(dir, "Downloads", "tuon.zip")
	if b, err := os.ReadFile(want); err != nil || string(b) != "zipdata" || revealed != want {
		t.Fatalf("saved %q (%v), revealed %q", b, err, revealed)
	}
	// a second save never overwrites
	next, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})
	_ = run(next.(Model), cmd)
	if _, err := os.Stat(filepath.Join(dir, "Downloads", "tuon (2).zip")); err != nil {
		t.Fatal("second save should be tuon (2).zip")
	}
}

func TestSendErrorWording(t *testing.T) {
	if got := sendError(errors.New("dgw: socket is not open")); got != "you're offline or Messenger isn't reachable" {
		t.Fatalf("got %q", got)
	}
	if got := sendError(errors.New("big.zip: upload rejected")); got != "big.zip: upload rejected" {
		t.Fatalf("got %q", got)
	}
}

func TestMuteSaysWhichWay(t *testing.T) {
	m, st, _ := newTestModel(t, 120, 30)
	_ = st.UpsertChat(connector.Chat{ID: "c1", Name: "Family", LastActivity: time.Now()})
	m.switchSpace(store.Inbox)
	next, _ := m.runCommand("mute")
	if m = next.(Model); !strings.HasPrefix(m.toast, "Muted Family") {
		t.Fatalf("toast %q", m.toast)
	}
	next, _ = m.runCommand("mute")
	if m = next.(Model); m.toast != "Unmuted Family" {
		t.Fatalf("toast %q", m.toast)
	}
	next, _ = m.runCommand("work")
	if !strings.Contains(next.(Model).toast, "unknown command") {
		t.Fatal(":work should be gone")
	}
}

func click(m Model, x, y int) Model {
	next, cmd := m.Update(tea.MouseMsg{X: x, Y: y, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	return run(next.(Model), cmd)
}

func TestClickTabsAndPeople(t *testing.T) {
	m, _, fc := newTestModel(t, 120, 30)
	fc.directory = []connector.Person{{ID: "900", Name: "Markus Tan"}}
	m = seed(m)
	_, spans := m.tabLayout()
	var inbox tabSpan
	for _, s := range spans {
		if s.sp == store.Inbox {
			inbox = s
		}
	}
	if inbox.to == 0 {
		t.Fatalf("no inbox tab in %+v", spans)
	}
	m = click(m, inbox.from+1, 2)
	if m.space != store.Inbox {
		t.Fatalf("clicking Inbox: space = %q", m.space)
	}

	// a person found by search opens on click
	m = key(m, "/")
	var cmd tea.Cmd
	for _, r := range "markus" {
		var next tea.Model
		next, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = next.(Model)
	}
	m = run(m, cmd)
	row := -1
	for i := 0; i < m.rowCount(); i++ {
		if p := m.personAt(i); p != nil && p.ID == "900" {
			row = i
		}
	}
	if row < 0 {
		t.Fatal("Markus not listed")
	}
	m = click(m, 3, 4+1+1+row*rowLines) // header, blank line, search box
	if m.chat == nil || m.chat.ID != "900" {
		t.Fatalf("click didn't open Markus (opened %v)", fc.opened)
	}
}

func TestDraftsSurviveRestart(t *testing.T) {
	m, st, _ := newTestModel(t, 120, 30)
	_ = st.UpsertChat(connector.Chat{ID: "c1", Name: "Raine", LastActivity: time.Now()})
	m.reloadChats()
	c, _ := st.Chat("c1")
	m = run(m, m.openChat(c))
	m = typeText(m, "half a thought")
	m.Cleanup() // quitting with text in the box
	again := New(context.Background(), st, &fakeConn{}, "messenger")
	if again.drafts["c1"] != "half a thought" {
		t.Fatalf("draft after restart = %q", again.drafts["c1"])
	}
}

func TestSelectedMessageKeys(t *testing.T) {
	m, st, _ := newTestModel(t, 140, 30)
	now := time.Now()
	_ = st.UpsertChat(connector.Chat{ID: "c1", Name: "Raine", LastActivity: now})
	_, _ = st.AddMessage(connector.Message{ID: "p1", ChatID: "c1", SenderID: "2", Time: now.Add(-time.Hour),
		Media: []connector.Media{{Kind: connector.Photo, Ref: "img"}}}, false)
	_, _ = st.AddMessage(connector.Message{ID: "t1", ChatID: "c1", SenderID: "2", Text: "just text", Time: now.Add(-time.Minute)}, false)
	_, _ = st.AddMessage(connector.Message{ID: "pending:1", ChatID: "c1", SenderID: "1", FromMe: true, Pending: true, Text: "sending", Time: now.Add(-44 * time.Second)}, false)
	m.reloadChats()
	c, _ := st.Chat("c1")
	m = run(m, m.openChat(c))
	m = key(m, "up") // the pending one
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	if m = next.(Model); m.reacting || !strings.Contains(m.toast, "Wait until") {
		t.Fatalf("react on pending: reacting=%v toast=%q", m.reacting, m.toast)
	}
	m = key(m, "up") // "just text"
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})
	if m = next.(Model); m.mode == modeImage || !strings.Contains(m.toast, "Nothing to open") {
		t.Fatalf("o on a text message: mode=%v toast=%q", m.mode, m.toast)
	}
	if m.compose.ta.CharLimit < 20000 {
		t.Fatal("message cap still low")
	}
}

// stallConn never answers a send until its context ends.
type stallConn struct{ fakeConn }

func (s *stallConn) Send(ctx context.Context, _ string, _ string, _ []connector.Mention, _ *connector.ReplyRef) error {
	<-ctx.Done()
	return ctx.Err()
}

func TestStalledSendTimesOut(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := &stallConn{}
	old := sendTimeout
	sendTimeout = 200 * time.Millisecond
	defer func() { sendTimeout = old }()
	start := time.Now()
	msg := sendAll(c, ctx, connector.Chat{ID: "c1"}, "hi", nil, nil, nil)()
	sm := msg.(sentMsg)
	if sm.err == nil || sm.restore != "hi" || time.Since(start) > sendTimeout+5*time.Second {
		t.Fatalf("got %+v after %v", sm, time.Since(start))
	}
	if !strings.Contains(sendError(sm.err), "offline") {
		t.Fatalf("wording: %q", sendError(sm.err))
	}
}

func TestExpiredPhotoLinkRenews(t *testing.T) {
	var opened []string
	previewFiles = func(p []string) error { opened = p; return nil }
	defer func() { previewFiles = openFilesOS }()
	m, st, fc := newTestModel(t, 120, 30)
	m.kitty = nil
	fc.media = []byte("\x89PNG....")
	fc.mediaErr = map[string]error{"https://x.fbcdn.net/old.jpg": errors.New("media: HTTP 403 (link may have expired)")}
	now := time.Now()
	_ = st.UpsertChat(connector.Chat{ID: "g1", Name: "Plain group", LastActivity: now, Group: true})
	msg := connector.Message{ID: "p1", ChatID: "g1", SenderID: "2", Time: now.Add(-40 * 24 * time.Hour),
		Media: []connector.Media{{Kind: connector.Photo, Ref: "https://x.fbcdn.net/old.jpg"}}}
	_, _ = st.AddMessage(msg, false)
	fc.onLoadOlder = func() { // Messenger sends the message again with a fresh link
		fresh := msg
		fresh.Media = []connector.Media{{Kind: connector.Photo, Ref: "https://x.fbcdn.net/new.jpg"}}
		_, _ = st.AddMessage(fresh, false)
	}
	m.reloadChats()
	c, _ := st.Chat("g1")
	m = run(m, m.openChat(c))
	m = run(m, m.openPhoto(connector.Media{Kind: connector.Photo, Ref: "https://x.fbcdn.net/old.jpg"}))
	if len(fc.fetched) < 2 || fc.fetched[len(fc.fetched)-1] != "https://x.fbcdn.net/new.jpg" {
		t.Fatalf("fetched %v", fc.fetched)
	}
	if len(opened) != 1 {
		t.Fatalf("photo not opened after renewing the link (opened %v)", opened)
	}
}

func TestLinkRisk(t *testing.T) {
	for url, want := range map[string]string{
		"https://www.reddit.com/r/x":         "",
		"http://example.com":                 "isn't encrypted (http)",
		"https://192.168.1.5/login":          "is a bare IP address, not a site name",
		"https://xn--fcebook-8va.com/":       "uses look-alike letters (punycode)",
		"https://facebook.com@evil.example/": "hides its real address behind a name@ part",
		"https://example.com:8443/":          "uses an unusual port 8443",
		"javascript:alert(1)":                "isn't a web link",
	} {
		if _, got := linkRisk(url); got != want {
			t.Errorf("linkRisk(%q) = %q, want %q", url, got, want)
		}
	}
	if host, _ := linkRisk("https://facebook.com@evil.example/"); host != "evil.example" {
		t.Errorf("host = %q, want the real one", host)
	}
}

func TestRiskyLinkNeedsSecondPress(t *testing.T) {
	var opened string
	openURL = func(u string) error { opened = u; return nil }
	defer func() { openURL = openFile }()
	m, _, _ := newTestModel(t, 100, 26)
	cmd := m.openLinkSafe("http://example.com")
	if cmd == nil || opened != "" || !strings.Contains(m.toast, "isn't encrypted") {
		t.Fatalf("first press: opened=%q toast=%q", opened, m.toast)
	}
	m = run(m, m.openLinkSafe("http://example.com"))
	if opened != "http://example.com" {
		t.Fatal("second press should open it")
	}
	opened = ""
	m = run(m, m.openLinkSafe("https://www.reddit.com/r/x"))
	if opened == "" {
		t.Fatal("an ordinary https link opens on the first press")
	}
}

func TestClipboardClearsOnlyOurText(t *testing.T) {
	clip := ""
	readClipboard = func() string { return clip }
	writeClipboard = func(s string) error { clip = s; return nil }
	old := clipboardKeep
	clipboardKeep = 10 * time.Millisecond
	defer func() { readClipboard, writeClipboard, clipboardKeep = clipboardText, copyToClipboard, old }()

	m, _, _ := newTestModel(t, 100, 26)
	m = run(m, copyText("the PIN is 4821", 1))
	if clip != "" {
		t.Fatalf("clipboard should be cleared, still %q", clip)
	}
	// something else copied since: left alone
	next, cmd := m.Update(copiedMsg{n: 1, text: "old"})
	clip = "my own thing"
	_ = run(next.(Model), cmd)
	if clip != "my own thing" {
		t.Fatalf("wiped someone else's clipboard: %q", clip)
	}
}

func TestIdleLockHidesChats(t *testing.T) {
	m, _, _ := newTestModel(t, 120, 30)
	m = seed(m)
	m = key(m, "3")
	if !strings.Contains(screen(m), "Dev Team") {
		t.Fatal("setup: chat not shown")
	}
	asked := 0
	m.SetLock(func() error { asked++; return nil }, nil, time.Minute)
	m.lastInput = time.Now().Add(-2 * time.Minute)
	next, cmd := m.Update(idleTick{})
	m = next.(Model)
	_ = cmd
	if !m.locked || strings.Contains(screen(m), "Dev Team") || !strings.Contains(screen(m), "tuon is locked") {
		t.Fatalf("not locked:\n%s", screen(m))
	}
	// keys do nothing but unlock while locked
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	if m = next.(Model); !m.locked {
		t.Fatal("q unlocked it")
	}
	next, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = run(next.(Model), cmd)
	if m.locked || asked != 1 || !strings.Contains(screen(m), "Dev Team") {
		t.Fatalf("Touch ID unlock failed (asked %d)", asked)
	}
}

func TestPassphraseLock(t *testing.T) {
	m, _, _ := newTestModel(t, 120, 30)
	m.SetLock(nil, func(p string) bool { return p == "correct horse" }, time.Minute)
	next, _ := m.runCommand("lock")
	m = next.(Model)
	if !m.locked {
		t.Fatal(":lock didn't lock")
	}
	m = typeTextLocked(m, "nope")
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m = next.(Model); !m.locked || m.lockErr == "" {
		t.Fatal("wrong passphrase unlocked")
	}
	m.unlocking = false // skip the wait
	m = typeTextLocked(m, "correct horse")
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m = run(next.(Model), cmd); m.locked {
		t.Fatal("right passphrase didn't unlock")
	}
}

func typeTextLocked(m Model, s string) Model {
	for _, r := range s {
		next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = next.(Model)
	}
	return m
}

func TestKeysTroubleWarning(t *testing.T) {
	m, st, _ := newTestModel(t, 200, 30)
	_ = st.UpsertContact("100002876447027", "Jayson Vcr")
	_ = st.UpsertChat(connector.Chat{ID: "100002876447027", LastActivity: time.Now(), Encrypted: true})
	next, _ := m.Update(EventsMsg{connector.KeysTroubleEvent{UserID: "100002876447027", Failed: 3}})
	if s := screen(next.(Model)); !strings.Contains(s, "Messages from Jayson keep failing") || !strings.Contains(s, "tuon relink") {
		t.Fatalf("no warning:\n%s", s)
	}
}

func TestPreviewsOff(t *testing.T) {
	m, _, _ := newTestModel(t, 140, 30)
	m = seed(m)
	m = key(m, "3")
	if !strings.Contains(screen(m), "deploy done") {
		t.Fatalf("setup: snippet not shown\n%s", screen(m))
	}
	next, _ := m.runCommand("previews off")
	m = next.(Model)
	m.reloadChats()
	m.chat = nil
	_ = m.preview()
	s := screen(m)
	if strings.Contains(s, "deploy done") || strings.Contains(s, "pushed the fix") || !strings.Contains(s, "Previews are off") {
		t.Fatalf("message text visible with previews off:\n%s", s)
	}
	if !strings.Contains(s, "new message") {
		t.Fatalf("unread chats should still say so:\n%s", s)
	}
	m = run(m, m.openChat(m.chats[m.cursor])) // opening shows the chat
	if !strings.Contains(screen(m), "pushed the fix") {
		t.Fatalf("opened chat hides messages:\n%s", screen(m))
	}
}

func TestReactionFromBackupReplacesPlaceholder(t *testing.T) {
	m, st, _ := newTestModel(t, 140, 30)
	now := time.Now()
	_ = st.UpsertChat(connector.Chat{ID: "j", Name: "Jayson", LastActivity: now.Add(-time.Minute), Encrypted: true})
	_, _ = st.AddMessage(connector.Message{ID: "mine", ChatID: "j", SenderID: "1", FromMe: true, Text: "so di ko need silipin", Time: now.Add(-time.Minute)}, false)
	// his reaction failed to decrypt live: a placeholder under the reaction's ID
	_, _ = st.AddMessage(connector.Message{ID: "react1", ChatID: "j", SenderID: "2", Time: now,
		Media: []connector.Media{{Kind: connector.Missing}}}, false)
	m.reloadChats()
	c, _ := st.Chat("j")
	m = run(m, m.openChat(c))
	if !strings.Contains(screen(m), "loading from your backup") {
		t.Fatal("setup: no placeholder")
	}
	// the backup has it: a ❤ on my message
	next, _ := m.Update(EventsMsg{
		connector.ReactionEvent{ChatID: "j", MessageID: "mine", ActorID: "2", Emoji: "❤"},
		connector.MessageRemovedEvent{ID: "react1"},
	})
	m = next.(Model)
	s := screen(m)
	if strings.Contains(s, "loading from your backup") || !strings.Contains(s, "❤") {
		t.Fatalf("placeholder still there or reaction missing:\n%s", s)
	}
	if c, _ := st.Chat("j"); strings.Contains(c.Snippet, "loading") || st.HasMissing("j") {
		t.Fatalf("preview still %q, missing=%v", c.Snippet, st.HasMissing("j"))
	}
}

func TestPlaceholderGiveUpAndRemove(t *testing.T) {
	m, st, _ := newTestModel(t, 140, 30)
	now := time.Now()
	_ = st.UpsertChat(connector.Chat{ID: "j", Name: "Jayson", LastActivity: now, Encrypted: true})
	_, _ = st.AddMessage(connector.Message{ID: "real", ChatID: "j", SenderID: "2", Text: "hello", Time: now.Add(-time.Minute)}, false)
	_, _ = st.AddMessage(connector.Message{ID: "ph", ChatID: "j", SenderID: "2", Time: now,
		Media: []connector.Media{{Kind: connector.Missing}}}, false)
	m.reloadChats()
	c, _ := st.Chat("j")
	m = run(m, m.openChat(c))
	// the backup checks ran out
	next, cmd := m.Update(refillDoneMsg{chatID: "j", attempt: maxRefills - 1})
	m = run(next.(Model), cmd)
	if s := screen(m); !strings.Contains(s, "Couldn't load this message") || strings.Contains(s, "loading from your backup") {
		t.Fatalf("not given up:\n%s", s)
	}
	if st.HasMissing("j") {
		t.Fatal("still counted as waiting")
	}
	m = key(m, "up") // select it, d removes it
	next, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	m = run(next.(Model), cmd)
	if strings.Contains(screen(m), "Couldn't load") {
		t.Fatal("d didn't remove the line")
	}
	if c, _ := st.Chat("j"); c.Snippet != "hello" {
		t.Fatalf("preview = %q, want the newest real message", c.Snippet)
	}
}
