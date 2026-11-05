// Package ui is Tuon's terminal interface: chat list on the left, the open
// chat on the right (Messenger-style), all keyboard driven.
package ui

import (
	"context"
	"fmt"
	"image"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/ralph/tuon/internal/connector"
	"github.com/ralph/tuon/internal/store"
)

type mode int

const (
	modeMain mode = iota
	modeImage
	modeHelp
)

type focus int

const (
	focusList focus = iota
	focusChat
)

// What the input line is doing, if anything.
type inputKind int

const (
	inputNone inputKind = iota
	inputSearch
	inputCommand
	inputMessage
	inputFolder // naming a folder (add / rename)
)

const (
	pageSize     = 200 // messages kept on screen per chat
	thumbMaxCols = 64  // widest inline photo, in cells (1 cell = 1 px wide)
	settingImage = "images"
	settingSound = "sound"
	maxSugg      = 5
)

// Quick reactions, same set as Messenger.
var reactions = []string{"❤", "😆", "😮", "😢", "😠", "👍"}

type Model struct {
	ctx      context.Context
	st       *store.Store
	conn     connector.Connector
	platform string

	w, h  int
	mode  mode
	focus focus

	// list
	space  store.Space
	chats  []store.ChatRow
	cursor int
	query  string
	counts map[store.Space]int
	tagFor string        // chat ID while the tag popup is open
	spaces []store.Space // your folders, then Inbox

	// folder manager (f)
	folders     bool
	folderCur   int
	folderOp    string // "add" or "rename" while naming
	folderAskRm bool   // waiting for y to confirm delete

	// chat
	chat         *store.ChatRow
	msgs         []store.MessageRow
	selID        string // selected message; "" = follow the newest
	scroll       int    // lines scrolled up from the bottom
	loadingOlder bool
	reacting     bool // reaction picker open
	selecting    bool // stepping through messages (react, photo); else typing

	// compose
	input     textinput.Model // search and : commands
	inputKind inputKind
	compose   textinput.Model // the message box; focused whenever a chat is open
	drafts    map[string]string
	people    []store.Person         // who can be @mentioned in the open chat
	mentions  []connector.Mention    // tagged so far in the draft
	sugg      []suggestion           // @ pop-up: people or files
	atts      []connector.Attachment // files waiting to go with the next send
	dragFrom  int                    // mouse drag over messages (copy); -1 = none
	dragTo    int
	suggIdx   int
	suggOff   bool // esc closed suggestions for this draft position

	// images
	imagesOn bool
	raw      map[string][]byte
	imgs     map[string]image.Image
	pending  map[string]bool
	failed   map[string]string // ref -> error, so we don't retry forever
	imgIdx   int               // in image view: index into chatImages()
	kitty    *kittyImages      // real photos (Ghostty/kitty); nil = pixel blocks

	status    string
	statusErr bool
	toast     string

	started   time.Time // messages older than this never chime
	soundOn   bool
	lastChime time.Time
}

// Bubble Tea messages.
type (
	EventsMsg  []any
	startedMsg struct{ err error }
	mediaMsg   struct {
		ref string
		raw []byte
		img image.Image
		err error
	}
	sentMsg      struct{ err error }
	toastMsg     struct{ text string }
	clearToast   struct{}
	olderDoneMsg struct{ err error }
)

func New(ctx context.Context, st *store.Store, conn connector.Connector, platform string) Model {
	in := textinput.New()
	in.Prompt = ""
	in.CharLimit = 4000
	co := textinput.New()
	co.Prompt = ""
	co.CharLimit = 4000
	m := Model{
		ctx:      ctx,
		st:       st,
		conn:     conn,
		platform: platform,
		space:    store.Work,
		imagesOn: st.Setting(settingImage, "on") == "on",
		soundOn:  st.Setting(settingSound, "on") == "on",
		started:  time.Now(),
		raw:      map[string][]byte{},
		imgs:     map[string]image.Image{},
		pending:  map[string]bool{},
		failed:   map[string]string{},
		input:    in,
		compose:  co,
		drafts:   map[string]string{},
		dragFrom: -1,
		dragTo:   -1,
		status:   "connecting…",
	}
	if kittyMode {
		m.kitty = newKittyImages(os.Stdout, filepath.Join(store.Dir(), "cache", "img"))
	}
	m.reloadChats()
	return m
}

// Cleanup frees photos held by the terminal. Call after the program ends.
func (m Model) Cleanup() {
	if m.kitty != nil {
		m.kitty.clearAll()
	}
}

func (m Model) Init() tea.Cmd {
	return func() tea.Msg { return startedMsg{m.conn.Start(m.ctx)} }
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		// leave the last column empty: if a character is ever wider than
		// we think, the line still won't wrap and push the screen down
		m.w, m.h = max(1, msg.Width-1), msg.Height
		m.sizeInput()
		return m, nil

	case startedMsg:
		if msg.err != nil {
			m.status, m.statusErr = "login failed: "+msg.err.Error()+" — update ~/.config/tuon/cookies.txt", true
		} else {
			m.status, m.statusErr = "syncing…", false
			m.st.Self = m.conn.SelfID()
		}
		m.reloadChats()
		return m, nil

	case EventsMsg:
		return m, m.applyEvents(msg)

	case mediaMsg:
		delete(m.pending, msg.ref)
		if msg.err != nil {
			m.failed[msg.ref] = msg.err.Error()
		} else {
			m.raw[msg.ref] = msg.raw
			m.imgs[msg.ref] = msg.img
		}
		m.keepSelectionVisible()
		return m, nil

	case sentMsg:
		if msg.err != nil {
			return m, m.flash("send failed: " + msg.err.Error())
		}
		return m, nil

	case olderDoneMsg:
		m.loadingOlder = false
		return m, nil

	case toastMsg:
		return m, m.flash(msg.text)

	case attachedMsg:
		if msg.err != nil {
			return m, m.flash("couldn't attach: " + msg.err.Error())
		}
		m.atts = append(m.atts, msg.files...)
		return m, nil

	case pasteTextMsg:
		v, pos := []rune(m.compose.Value()), m.compose.Position()
		ins := []rune(strings.TrimRight(string(msg), "\n"))
		pos = min(pos, len(v))
		m.compose.SetValue(string(v[:pos]) + string(ins) + string(v[pos:]))
		m.compose.SetCursor(pos + len(ins))
		m.updateSuggestions()
		return m, nil

	case copiedMsg:
		if msg.err != nil {
			return m, m.flash("couldn't copy: " + msg.err.Error())
		}
		if msg.n == 1 {
			return m, m.flash("Copied")
		}
		return m, m.flash(fmt.Sprintf("Copied %d messages", msg.n))

	case clearToast:
		m.toast = ""
		return m, nil

	case tea.KeyMsg:
		return m.onKey(msg)

	case tea.MouseMsg:
		return m.onMouse(msg)
	}
	return m, nil
}

func (m *Model) flash(text string) tea.Cmd {
	m.toast = text
	return tea.Tick(3*time.Second, func(time.Time) tea.Msg { return clearToast{} })
}

func (m *Model) applyEvents(evts []any) tea.Cmd {
	openID := "" // a chat you're in (not just previewing) stays read
	if m.chat != nil && m.focus == focusChat {
		openID = m.chat.ID
	}
	chime := false
	for _, e := range evts {
		switch e := e.(type) {
		case connector.ContactEvent:
			_ = m.st.UpsertContact(e.ID, e.Name)
		case connector.ChatEvent:
			if e.Chat.ID == openID {
				e.Chat.Unread = false
			}
			_ = m.st.UpsertChat(e.Chat)
		case connector.MessageEvent:
			isNew, _ := m.st.AddMessage(e.Message, e.Message.ChatID == openID)
			if isNew && !e.Message.FromMe && e.Message.Time.After(m.started) {
				if c, err := m.st.Chat(e.Message.ChatID); err == nil && !c.Muted {
					chime = true
				}
			}
		case connector.MessageRemovedEvent:
			_ = m.st.RemoveMessage(e.ID)
		case connector.ReactionEvent:
			_ = m.st.SetReaction(e)
		case connector.ParticipantEvent:
			_ = m.st.AddParticipant(e)
		case connector.StatusEvent:
			m.status, m.statusErr = e.Text, e.Err != nil
			if e.Err != nil {
				m.status += ": " + e.Err.Error()
			}
		}
	}
	if openID != "" {
		_ = m.st.MarkRead(openID)
	}
	m.reloadChats()
	cmd := m.reloadMessages()
	if chime && m.soundOn && time.Since(m.lastChime) > 2*time.Second {
		m.lastChime = time.Now()
		return tea.Batch(cmd, playChime)
	}
	return cmd
}

// playChime: one soft tick for new messages (macOS "Tink", half volume).
func playChime() tea.Msg {
	if runtime.GOOS == "darwin" {
		_ = exec.Command("afplay", "-v", "0.5", "/System/Library/Sounds/Tink.aiff").Start()
	} else {
		fmt.Print("\a")
	}
	return nil
}

func (m *Model) reloadChats() {
	selected := ""
	if m.cursor < len(m.chats) {
		selected = m.chats[m.cursor].ID
	}
	m.spaces = m.st.Spaces()
	known := false
	for _, sp := range m.spaces {
		known = known || sp == m.space
	}
	if !known { // the folder was renamed or deleted
		m.space = m.spaces[0]
	}
	m.chats, _ = m.st.Chats(m.space, m.query)
	m.counts = m.st.UnreadCounts()
	// keep the cursor on the same chat when the list reorders
	m.cursor = min(m.cursor, max(0, len(m.chats)-1))
	for i, c := range m.chats {
		if c.ID == selected {
			m.cursor = i
			break
		}
	}
}

// reloadMessages refreshes the open chat and starts fetching visible photos.
func (m *Model) reloadMessages() tea.Cmd {
	if m.chat == nil {
		return nil
	}
	if c, err := m.st.Chat(m.chat.ID); err == nil {
		m.chat = &c
	}
	m.msgs, _ = m.st.Messages(m.chat.ID, pageSize, m.conn.SelfID())
	m.people, _ = m.st.People(m.chat.ID, m.conn.SelfID())
	m.keepSelectionVisible()
	if !m.imagesOn {
		return nil
	}
	var cmds []tea.Cmd
	// only the most recent photos, so opening a chat stays fast
	for i := len(m.msgs) - 1; i >= 0 && i >= len(m.msgs)-30; i-- {
		for _, md := range m.msgs[i].Media {
			if cmd := m.fetch(md); cmd != nil {
				cmds = append(cmds, cmd)
			}
		}
	}
	return tea.Batch(cmds...)
}

func (m *Model) fetch(md connector.Media) tea.Cmd {
	if md.Kind != connector.Photo || md.Ref == "" || m.pending[md.Ref] || m.imgs[md.Ref] != nil || m.failed[md.Ref] != "" {
		return nil
	}
	m.pending[md.Ref] = true
	conn, ctx := m.conn, m.ctx
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		b, err := conn.FetchMedia(ctx, md)
		if err != nil {
			return mediaMsg{ref: md.Ref, err: err}
		}
		img, err := decodeImage(b)
		return mediaMsg{ref: md.Ref, raw: b, img: img, err: err}
	}
}

// ---- selection ----

// selIndex is the selected message's index (the newest when following).
func (m Model) selIndex() int {
	if m.selID != "" {
		for i, msg := range m.msgs {
			if msg.ID == m.selID {
				return i
			}
		}
	}
	return len(m.msgs) - 1
}

func (m *Model) selectIndex(i int) {
	if len(m.msgs) == 0 {
		return
	}
	i = max(0, min(i, len(m.msgs)-1))
	if i == len(m.msgs)-1 {
		m.selID = "" // back at the bottom: follow new messages again
	} else {
		m.selID = m.msgs[i].ID
	}
	m.keepSelectionVisible()
}

// keepSelectionVisible scrolls so the selected message is on screen.
func (m *Model) keepSelectionVisible() {
	if m.chat == nil {
		return
	}
	if m.selID == "" {
		if m.selecting {
			m.scroll = 0
		}
		return
	}
	lines, spans := m.chatLines()
	i := m.selIndex()
	if i < 0 || i >= len(spans) {
		return
	}
	ch := m.chatHeight()
	sp := spans[i]
	// visible window is [len-scroll-ch, len-scroll)
	end := len(lines) - m.scroll
	start := end - ch
	switch {
	case sp.start < start:
		m.scroll = len(lines) - sp.start - ch
	case sp.end > end:
		m.scroll = len(lines) - sp.end
	}
	m.scroll = max(0, min(m.scroll, max(0, len(lines)-ch)))
}

// ---- keys ----

func (m Model) onKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	if k.String() == "ctrl+c" {
		return m, tea.Quit
	}
	if m.inputKind != inputNone {
		return m.onInputKey(k)
	}
	if m.tagFor != "" {
		return m.onTagKey(k)
	}
	if m.folders {
		return m.onFolderKey(k)
	}
	if m.reacting {
		return m.onReactKey(k)
	}
	switch m.mode {
	case modeHelp:
		if k.String() == "esc" || k.String() == "?" || k.String() == "q" {
			m.mode = modeMain
		}
		return m, nil
	case modeImage:
		return m.onImageKey(k)
	}
	if m.focus == focusChat && m.chat != nil {
		if m.selecting {
			return m.onSelectKey(k)
		}
		return m.onComposeKey(k)
	}
	return m.onListKey(k)
}

// composing: the message box has the keyboard.
func (m Model) composing() bool {
	return m.focus == focusChat && m.chat != nil && !m.selecting && m.inputKind == inputNone &&
		m.mode == modeMain && m.tagFor == "" && !m.reacting
}

func (m *Model) sizeInput() {
	m.input.Width = max(10, m.rightWidth()-6)
	m.compose.Width = max(10, m.rightWidth()-6)
}

func (m *Model) startInput(kind inputKind, value string) tea.Cmd {
	m.inputKind = kind
	m.input.SetValue(value)
	m.input.CursorEnd()
	m.sizeInput()
	if kind == inputSearch || kind == inputCommand {
		m.input.Width = max(10, m.leftWidth()-4)
		if kind == inputCommand {
			m.input.Width = max(10, m.w-4)
		}
	}
	return m.input.Focus()
}

func (m *Model) stopInput() {
	m.inputKind = inputNone
	m.input.Blur()
	m.sugg = nil
}

func (m Model) onInputKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "esc":
		if m.inputKind == inputSearch {
			m.query = ""
			m.reloadChats()
		}
		m.stopInput()
		return m, m.preview()
	case "enter":
		val := strings.TrimSpace(m.input.Value())
		kind := m.inputKind
		m.stopInput()
		switch kind {
		case inputCommand:
			return m.runCommand(val)
		case inputFolder:
			return m.finishFolderName(val)
		}
		return m, m.preview()
	case "down", "up":
		if m.inputKind == inputSearch { // move through results while searching
			if k.String() == "down" {
				m.cursor = min(m.cursor+1, max(0, len(m.chats)-1))
			} else {
				m.cursor = max(m.cursor-1, 0)
			}
			return m, m.preview()
		}
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(k)
	if m.inputKind == inputSearch {
		m.query = m.input.Value()
		m.cursor = 0
		m.reloadChats()
		return m, tea.Batch(cmd, m.preview())
	}
	return m, cmd
}

// onComposeKey: typing in an open chat.
func (m Model) onComposeKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	// @ suggestions take the arrow keys, tab and enter while showing
	if len(m.sugg) > 0 {
		switch k.String() {
		case "up", "ctrl+p":
			m.suggIdx = (m.suggIdx - 1 + len(m.sugg)) % len(m.sugg)
			return m, nil
		case "down", "ctrl+n":
			m.suggIdx = (m.suggIdx + 1) % len(m.sugg)
			return m, nil
		case "tab", "enter":
			return m, m.acceptSuggestion(m.sugg[m.suggIdx])
		case "esc":
			m.sugg, m.suggOff = nil, true
			return m, nil
		}
	}
	// a drag-and-drop pastes file paths: attach them instead of typing them
	if k.Paste {
		if paths, ok := droppedPaths(string(k.Runes)); ok {
			return m, attachPaths(paths)
		}
	}
	switch k.String() {
	case "ctrl+v":
		return m, pasteClipboard()
	case "backspace":
		if m.compose.Value() == "" && len(m.atts) > 0 {
			m.atts = m.atts[:len(m.atts)-1]
			return m, nil
		}
	case "esc", "tab":
		m.leaveChat()
		return m, nil
	case "up":
		if m.compose.Value() == "" && len(m.msgs) > 0 {
			m.selecting = true
			m.compose.Blur()
			m.selectIndex(len(m.msgs) - 1)
			return m, nil
		}
	case "pgup", "ctrl+u":
		m.scroll += m.chatHeight() / 2
		return m, nil
	case "pgdown", "ctrl+d":
		m.scroll = max(0, m.scroll-m.chatHeight()/2)
		return m, nil
	case "ctrl+l":
		return m, tea.ClearScreen
	case "enter":
		val := strings.TrimSpace(m.compose.Value())
		if val == "" && len(m.atts) == 0 {
			return m, nil
		}
		mentions := keptMentions(val, m.mentions)
		files := m.atts
		m.compose.SetValue("")
		delete(m.drafts, m.chat.ID)
		m.mentions, m.sugg, m.suggOff, m.atts = nil, nil, false, nil
		m.selID, m.scroll = "", 0
		cmd := sendAll(m.conn, m.ctx, m.chat.ID, val, mentions, files)
		if len(files) > 0 {
			return m, tea.Batch(cmd, m.flash(fmt.Sprintf("Sending %d file(s)…", len(files))))
		}
		return m, cmd
	}
	before := m.compose.Value()
	var cmd tea.Cmd
	m.compose, cmd = m.compose.Update(k)
	if m.compose.Value() != before {
		m.suggOff = false
	}
	m.updateSuggestions()
	return m, cmd
}

// leaveChat goes back to the list, keeping the draft.
func (m *Model) leaveChat() {
	if m.chat != nil {
		if v := m.compose.Value(); v != "" {
			m.drafts[m.chat.ID] = v
		} else {
			delete(m.drafts, m.chat.ID)
		}
	}
	m.compose.Blur()
	m.focus, m.selecting, m.sugg = focusList, false, nil
	m.selID = ""
}

func (m Model) mentionQuery() (at int, query string, ok bool) {
	val := []rune(m.compose.Value())
	pos := min(m.compose.Position(), len(val))
	for i := pos - 1; i >= 0 && pos-i <= 300; i-- {
		if val[i] == '@' {
			if i > 0 && val[i-1] != ' ' {
				return 0, "", false
			}
			return i, string(val[i+1 : pos]), true
		}
	}
	return 0, "", false
}

func (m *Model) updateSuggestions() {
	m.sugg = nil
	_, q, ok := m.mentionQuery()
	if !ok || m.suggOff {
		return
	}
	if isPathQuery(q) {
		m.sugg = pathSuggestions(q)
	} else {
		lq := strings.ToLower(q)
		for i := range m.people {
			p := m.people[i]
			name := strings.ToLower(p.Name)
			match := lq == "" || strings.HasPrefix(name, lq)
			for _, w := range strings.Fields(name) {
				match = match || strings.HasPrefix(w, lq)
			}
			if match {
				m.sugg = append(m.sugg, suggestion{Label: "@" + p.Name, Person: &p})
			}
			if len(m.sugg) == maxSugg {
				break
			}
		}
	}
	m.suggIdx = min(m.suggIdx, max(0, len(m.sugg)-1))
}

// acceptSuggestion tags the person, opens the folder, or attaches the file.
func (m *Model) acceptSuggestion(sg suggestion) tea.Cmd {
	at, _, ok := m.mentionQuery()
	if !ok {
		return nil
	}
	val := []rune(m.compose.Value())
	pos := min(m.compose.Position(), len(val))
	replace := func(with string) {
		m.compose.SetValue(string(val[:at]) + with + string(val[pos:]))
		m.compose.SetCursor(at + len([]rune(with)))
	}
	switch {
	case sg.Person != nil:
		replace("@" + sg.Person.Name + " ")
		m.mentions = append(m.mentions, connector.Mention{UserID: sg.Person.ID, Name: sg.Person.Name})
		m.sugg = nil
	case sg.IsDir:
		replace("@" + shortHome(sg.Path) + "/") // keep going inside the folder
		m.updateSuggestions()
	default:
		replace("") // the path leaves the text; the file becomes a chip
		m.sugg = nil
		return attachPaths([]string{sg.Path})
	}
	return nil
}

func keptMentions(text string, ms []connector.Mention) []connector.Mention {
	var out []connector.Mention
	for _, mm := range ms {
		if strings.Contains(text, "@"+mm.Name) {
			out = append(out, mm)
		}
	}
	return out
}

func (m Model) runCommand(cmd string) (tea.Model, tea.Cmd) {
	switch cmd {
	case "q", "quit":
		return m, tea.Quit
	case "work", "personal", "inbox":
		m.switchSpace(store.Space(cmd))
	case "mute":
		id := m.targetChat()
		if id == "" {
			return m, nil
		}
		_ = m.st.ToggleMute(id)
		m.reloadChats()
		return m, m.flash("mute toggled")
	case "sound off", "sound on":
		m.soundOn = cmd == "sound on"
		_ = m.st.SetSetting(settingSound, strings.TrimPrefix(cmd, "sound "))
		return m, m.flash("chime " + strings.TrimPrefix(cmd, "sound "))
	case "images off", "images on":
		m.imagesOn = cmd == "images on"
		_ = m.st.SetSetting(settingImage, strings.TrimPrefix(cmd, "images "))
		return m, m.reloadMessages()
	case "help":
		m.mode = modeHelp
	case "":
	default:
		return m, m.flash("unknown command: " + cmd)
	}
	return m, nil
}

// targetChat is the chat being looked at: the open one when the chat pane
// has focus, else the one under the list cursor.
func (m Model) targetChat() string {
	if m.focus == focusChat && m.chat != nil {
		return m.chat.ID
	}
	if m.cursor < len(m.chats) {
		return m.chats[m.cursor].ID
	}
	return ""
}

func (m *Model) switchSpace(s store.Space) {
	m.space, m.cursor = s, 0
	m.reloadChats()
}

// preview shows the chat under the list cursor on the right, without
// opening it (it stays unread until you go in).
func (m *Model) preview() tea.Cmd {
	if m.focus != focusList || m.cursor >= len(m.chats) {
		return nil
	}
	c := m.chats[m.cursor]
	if m.chat != nil && m.chat.ID == c.ID {
		return nil
	}
	m.chat = &c
	m.selID, m.scroll, m.selecting, m.reacting = "", 0, false, false
	return m.reloadMessages()
}

func (m Model) onListKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	move := func(to int) (tea.Model, tea.Cmd) {
		m.cursor = max(0, min(to, len(m.chats)-1))
		return m, m.preview()
	}
	space := func(s store.Space) (tea.Model, tea.Cmd) {
		m.switchSpace(s)
		return m, tea.Batch(tea.ClearScreen, m.preview())
	}
	switch k.String() {
	case "q":
		return m, tea.Quit
	case "j", "down":
		return move(m.cursor + 1)
	case "k", "up":
		return move(m.cursor - 1)
	case "g", "home":
		return move(0)
	case "G", "end":
		return move(len(m.chats) - 1)
	case "ctrl+d", "pgdown":
		return move(m.cursor + m.listRows()/2)
	case "ctrl+u", "pgup":
		return move(m.cursor - m.listRows()/2)
	case "1", "2", "3", "4", "5", "6", "7", "8", "9":
		if i := int(k.String()[0] - '1'); i < len(m.spaces) {
			return space(m.spaces[i])
		}
	case "right", "l":
		return space(m.nextSpace(1))
	case "left", "h":
		return space(m.nextSpace(-1))
	case "f":
		m.folders, m.folderCur, m.folderAskRm = true, 0, false
		for i, sp := range m.spaces {
			if sp == m.space {
				m.folderCur = i
			}
		}
	case "/":
		return m, m.startInput(inputSearch, m.query)
	case ":":
		return m, m.startInput(inputCommand, "")
	case "?":
		m.mode = modeHelp
	case "t":
		if m.cursor < len(m.chats) {
			m.tagFor = m.chats[m.cursor].ID
		}
	case "ctrl+l":
		return m, tea.ClearScreen
	case "enter", "tab":
		if m.cursor < len(m.chats) {
			return m, m.openChat(m.chats[m.cursor])
		}
	}
	return m, nil
}

func (m Model) nextSpace(d int) store.Space {
	for i, x := range m.spaces {
		if x == m.space {
			return m.spaces[(i+d+len(m.spaces))%len(m.spaces)]
		}
	}
	return m.spaces[0]
}

func (m Model) onTagKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	var sp store.Space
	switch s := k.String(); {
	case s == "esc" || s == "q" || s == "t":
		m.tagFor = ""
		return m, nil
	case s == "i":
		sp = store.Inbox
	case len(s) == 1 && s[0] >= '1' && s[0] <= '9' && int(s[0]-'1') < len(m.spaces):
		sp = m.spaces[s[0]-'1']
	default:
		return m, nil
	}
	_ = m.st.SetSpace(m.tagFor, sp)
	m.tagFor = ""
	m.reloadChats()
	_ = m.reloadMessages()
	return m, m.flash("Moved to " + store.Label(sp))
}

// ---- folder manager ----

func (m Model) onFolderKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	n := len(m.spaces)
	cur := m.spaces[min(m.folderCur, n-1)]
	if m.folderAskRm {
		m.folderAskRm = false
		if k.String() == "y" {
			if err := m.st.DeleteSpace(cur); err != nil {
				return m, m.flash(err.Error())
			}
			m.reloadChats()
			m.folderCur = min(m.folderCur, len(m.spaces)-1)
			return m, m.flash("Deleted " + store.Label(cur) + ". Its chats are in Inbox.")
		}
		return m, nil
	}
	switch k.String() {
	case "esc", "f", "q":
		m.folders = false
	case "up", "k":
		m.folderCur = max(0, m.folderCur-1)
	case "down", "j":
		m.folderCur = min(n-1, m.folderCur+1)
	case "shift+up", "K":
		if cur != store.Inbox && m.st.MoveSpace(cur, -1) == nil && m.folderCur > 0 {
			m.folderCur--
		}
		m.reloadChats()
	case "shift+down", "J":
		if cur != store.Inbox && m.folderCur < n-2 && m.st.MoveSpace(cur, 1) == nil {
			m.folderCur++
		}
		m.reloadChats()
	case "a", "n":
		m.folderOp = "add"
		return m, m.startInput(inputFolder, "")
	case "r", "enter":
		if cur == store.Inbox {
			return m, m.flash("Inbox is built in and can't be renamed")
		}
		m.folderOp = "rename"
		return m, m.startInput(inputFolder, string(cur))
	case "d", "x":
		if cur == store.Inbox {
			return m, m.flash("Inbox is built in and can't be deleted")
		}
		m.folderAskRm = true
	}
	return m, nil
}

func (m Model) finishFolderName(name string) (tea.Model, tea.Cmd) {
	var (
		sp  store.Space
		err error
	)
	if m.folderOp == "rename" {
		old := m.spaces[min(m.folderCur, len(m.spaces)-1)]
		sp, err = m.st.RenameSpace(old, name)
		if err == nil && m.space == old {
			m.space = sp
		}
	} else {
		sp, err = m.st.AddSpace(name)
	}
	if err != nil {
		return m, m.flash(err.Error())
	}
	m.reloadChats()
	for i, x := range m.spaces {
		if x == sp {
			m.folderCur = i
		}
	}
	return m, nil
}

func (m *Model) openChat(c store.ChatRow) tea.Cmd {
	same := m.chat != nil && m.chat.ID == c.ID
	m.chat = &c
	m.focus = focusChat
	m.selecting, m.reacting, m.selID = false, false, ""
	if !same {
		m.scroll = 0
	}
	m.mentions, m.sugg = nil, nil
	m.compose.SetValue(m.drafts[c.ID])
	m.compose.CursorEnd()
	m.sizeInput()
	focus := m.compose.Focus()
	_ = m.st.MarkRead(c.ID)
	cmd := m.reloadMessages()
	m.reloadChats()
	if len(m.msgs) < 20 && !c.Encrypted {
		return tea.Batch(focus, cmd, m.loadOlder())
	}
	return tea.Batch(focus, cmd)
}

func (m *Model) loadOlder() tea.Cmd {
	if m.chat == nil || m.loadingOlder || m.chat.Encrypted {
		return nil
	}
	m.loadingOlder = true
	before, beforeID := time.Now(), ""
	if len(m.msgs) > 0 {
		before, beforeID = m.msgs[0].Time, m.msgs[0].ID
	}
	conn, ctx, id := m.conn, m.ctx, m.chat.ID
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		return olderDoneMsg{conn.LoadOlder(ctx, id, before, beforeID)}
	}
}

// onSelectKey: stepping through messages to react or open a photo.
func (m Model) onSelectKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	sel := m.selIndex()
	back := func() (tea.Model, tea.Cmd) { // to the message box
		m.selecting, m.selID = false, ""
		return m, m.compose.Focus()
	}
	switch k.String() {
	case "esc", "i", "enter":
		return back()
	case "tab":
		m.leaveChat()
	case "k", "up":
		if sel <= 0 {
			return m, m.loadOlder()
		}
		m.selectIndex(sel - 1)
	case "j", "down":
		if sel >= len(m.msgs)-1 {
			return back()
		}
		m.selectIndex(sel + 1)
	case "ctrl+u", "pgup":
		m.selectIndex(sel - 10)
	case "ctrl+d", "pgdown":
		m.selectIndex(sel + 10)
	case "G", "end":
		m.selectIndex(len(m.msgs) - 1)
	case "g", "home":
		m.selectIndex(0)
		return m, m.loadOlder()
	case "r":
		if sel >= 0 && sel < len(m.msgs) {
			m.reacting = true
		}
	case "y", "c":
		if sel >= 0 && sel < len(m.msgs) {
			return m, copyText(m.messagesText(sel, sel), 1)
		}
	case "t":
		m.tagFor = m.chat.ID
	case "ctrl+l":
		return m, tea.ClearScreen
	case "?":
		m.mode = modeHelp
	case "o":
		imgs := m.chatImages()
		if len(imgs) == 0 {
			return m, m.flash("no photos in this chat")
		}
		// the selected message's photo, else the newest
		m.imgIdx = len(imgs) - 1
		if sel >= 0 && sel < len(m.msgs) {
			for _, md := range m.msgs[sel].Media {
				for j, im := range imgs {
					if im.Ref == md.Ref {
						m.imgIdx = j
					}
				}
			}
		}
		m.mode = modeImage
		delete(m.failed, imgs[m.imgIdx].Ref) // o also retries a failed photo
		return m, m.fetch(imgs[m.imgIdx])
	}
	return m, nil
}

// onMouse: wheel scrolls the pane under the pointer; click opens a chat.
func (m Model) onMouse(e tea.MouseMsg) (tea.Model, tea.Cmd) {
	if m.mode != modeMain {
		return m, nil
	}
	lw := m.leftWidth()
	inList := lw > 0 && e.X < lw
	// drag over messages: highlight while dragging, copy on release
	if m.dragFrom >= 0 {
		switch e.Action {
		case tea.MouseActionMotion:
			if i := m.messageAt(e.Y); i >= 0 {
				m.dragTo = i
			}
			return m, nil
		case tea.MouseActionRelease:
			from, to := min(m.dragFrom, m.dragTo), max(m.dragFrom, m.dragTo)
			m.dragFrom, m.dragTo = -1, -1
			if from >= 0 && from != to || e.Y >= 0 && m.messageAt(e.Y) == from && from >= 0 {
				return m, copyText(m.messagesText(from, to), to-from+1)
			}
			return m, nil
		}
	}
	if e.Action != tea.MouseActionPress {
		return m, nil
	}
	if !inList && e.Button == tea.MouseButtonLeft && m.chat != nil {
		if i := m.messageAt(e.Y); i >= 0 {
			m.dragFrom, m.dragTo = i, i
		}
	}
	switch e.Button {
	case tea.MouseButtonWheelUp, tea.MouseButtonWheelDown:
		d := 1
		if e.Button == tea.MouseButtonWheelUp {
			d = -1
		}
		if inList {
			if m.focus == focusList {
				m.cursor = max(0, min(m.cursor+d, len(m.chats)-1))
				return m, m.preview()
			}
			return m, nil
		}
		m.scroll = max(0, m.scroll-3*d)
		return m, nil
	case tea.MouseButtonLeft:
		if inList {
			top := 4 + 1 // header rows + blank line above the list
			if m.searching() {
				top++
			}
			if e.Y < top {
				return m, nil
			}
			rows := m.listRows()
			off := 0
			if m.cursor >= rows {
				off = m.cursor - rows + 1
			}
			i := off + (e.Y-top)/rowLines
			if i >= 0 && i < len(m.chats) {
				m.leaveChat()
				m.cursor = i
				return m, m.openChat(m.chats[i])
			}
			return m, nil
		}
		if m.chat != nil && m.focus != focusChat {
			return m, m.openChat(*m.chat)
		}
	}
	return m, nil
}

func (m Model) onReactKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	m.reacting = false
	sel := m.selIndex()
	if sel < 0 || sel >= len(m.msgs) {
		return m, nil
	}
	msg := m.msgs[sel]
	emoji := ""
	switch s := k.String(); s {
	case "1", "2", "3", "4", "5", "6":
		emoji = reactions[s[0]-'1']
		if emoji == msg.MyReaction {
			emoji = "" // same one again = take it back
		}
	case "x", "0", "backspace":
		if msg.MyReaction == "" {
			return m, nil
		}
	default:
		return m, nil
	}
	conn, ctx, chatID := m.conn, m.ctx, m.chat.ID
	return m, func() tea.Msg {
		if err := conn.React(ctx, chatID, msg.ID, msg.SenderID, emoji); err != nil {
			return toastMsg{"reaction failed: " + err.Error()}
		}
		return nil
	}
}

func (m Model) chatImages() []connector.Media {
	var out []connector.Media
	for _, msg := range m.msgs {
		for _, md := range msg.Media {
			if md.Kind == connector.Photo && md.Ref != "" {
				out = append(out, md)
			}
		}
	}
	return out
}

func (m Model) onImageKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	imgs := m.chatImages()
	if len(imgs) == 0 {
		m.mode = modeMain
		return m, nil
	}
	m.imgIdx = min(m.imgIdx, len(imgs)-1)
	cur := imgs[m.imgIdx]
	switch k.String() {
	case "esc", "q", "o":
		m.mode = modeMain
	case "h", "left", "[":
		m.imgIdx = max(0, m.imgIdx-1)
		return m, m.fetch(imgs[m.imgIdx])
	case "l", "right", "]":
		m.imgIdx = min(len(imgs)-1, m.imgIdx+1)
		return m, m.fetch(imgs[m.imgIdx])
	case "O":
		return m, m.saveImage(cur.Ref, true)
	case "s":
		return m, m.saveImage(cur.Ref, false)
	}
	return m, nil
}

// saveImage writes the photo to ~/Downloads (s) or a temp file opened in the system viewer (O).
func (m Model) saveImage(ref string, open bool) tea.Cmd {
	b := m.raw[ref]
	if b == nil {
		return func() tea.Msg { return toastMsg{"photo still loading"} }
	}
	return func() tea.Msg {
		dir := os.TempDir()
		if !open {
			home, _ := os.UserHomeDir()
			dir = filepath.Join(home, "Downloads")
		}
		name := "tuon-" + time.Now().Format("20060102-150405") + extFor(b)
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, b, 0o600); err != nil {
			return toastMsg{"save failed: " + err.Error()}
		}
		if !open {
			return toastMsg{"saved " + path}
		}
		opener := "xdg-open"
		if runtime.GOOS == "darwin" {
			opener = "open"
		}
		if err := exec.Command(opener, path).Start(); err != nil {
			return toastMsg{"open failed: " + err.Error()}
		}
		return toastMsg{"opened in viewer"}
	}
}

func extFor(b []byte) string {
	switch {
	case len(b) > 3 && b[0] == 0x89 && b[1] == 'P':
		return ".png"
	case len(b) > 3 && string(b[:3]) == "GIF":
		return ".gif"
	case len(b) > 12 && string(b[8:12]) == "WEBP":
		return ".webp"
	default:
		return ".jpg"
	}
}

// messageAt maps a screen row in the chat pane to a message index (-1 if none).
func (m Model) messageAt(y int) int {
	if m.chat == nil {
		return -1
	}
	lines, spans := m.chatLines()
	ch := m.chatHeight()
	scroll := min(m.scroll, max(0, len(lines)-ch))
	end := len(lines) - scroll
	start := max(0, end-ch)
	pad := ch - (end - start) // blank rows above short chats
	line := start + (y - 4 - pad)
	for i, sp := range spans {
		if line >= sp.start && line < sp.end {
			return i
		}
	}
	return -1
}
