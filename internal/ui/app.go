// Package ui is Tuon's terminal interface: chat list on the left, the open
// chat on the right (Messenger-style), all keyboard driven.
package ui

import (
	"context"
	"errors"
	"fmt"
	"image"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/ralph/tuon/internal/connector"
	"github.com/ralph/tuon/internal/connector/messenger"
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
	pageSize        = 200 // messages kept on screen per chat
	thumbMaxCols    = 64  // widest inline photo, in cells (1 cell = 1 px wide)
	settingImage    = "images"
	settingPreviews = "previews"
	settingSound    = "sound"
	maxSugg         = 5
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

	// people search: people shown under the chats while searching
	found      []store.Person // people shown under the chats while searching
	remote     []store.Person // from Messenger's search, for the current query
	searchSeq  int
	personView *store.Person // the person under the cursor (right pane hint)

	// folder manager (f)
	folders     bool
	folderCur   int
	folderOp    string // "add" or "rename" while naming
	folderAskRm bool   // waiting for y to confirm delete
	folderMove  bool   // m: carrying the folder; ↑↓ move it, enter drops

	// chat
	chat         *store.ChatRow
	msgs         []store.MessageRow
	selID        string // selected message; "" = follow the newest
	scroll       int    // lines scrolled up from the bottom
	loadingOlder bool
	synced       map[string]bool // encrypted chats already filled from the PIN backup this run
	refilling    map[string]bool // chats waiting to refill a message that couldn't be decrypted
	askedName    map[string]bool // senders whose name we already asked Messenger for
	checkAt      time.Time       // a redraw is due then, to flip "sending…" to "not sent"
	warning      string          // a lasting safety warning shown in the header
	lock         lockConfig      // the lock screen, once main turns it on
	locked       bool
	unlocking    bool
	lockErr      string
	lockFails    int
	lockIn       textinput.Model
	lastInput    time.Time
	reacting     bool // reaction picker open
	selecting    bool // stepping through messages (react, photo); else typing

	// compose
	input     textinput.Model // search and : commands
	inputKind inputKind
	compose   box // the message box; focused whenever a chat is open
	drafts    map[string]string
	draftAtts map[string][]pendingFile
	attSeq    int                 // numbers [Image #n] tokens in the current draft
	people    []store.Person      // who can be @mentioned in the open chat
	mentions  []connector.Mention // tagged so far in the draft
	sugg      []suggestion        // @ pop-up: people or files
	atts      []pendingFile       // files in the draft, shown as [Image #n] tokens
	replyTo   *store.MessageRow   // the message the draft replies to
	dragFrom  int                 // mouse drag over messages (copy); -1 = none
	dragTo    int
	suggIdx   int
	suggOff   bool // esc closed suggestions for this draft position

	// images
	imagesOn   bool
	previewsOn bool // message text in the chat list and when browsing
	raw        map[string][]byte
	imgs       map[string]image.Image
	pending    map[string]bool
	failed     map[string]string // ref -> error, so we don't retry forever
	relinked   map[string]bool   // expired photo links we already asked Messenger to renew
	confirmURL string            // a risky link waiting for a second press to open
	imgIdx     int               // in image view: index into chatImages()
	recent     []string          // photo refs, oldest first (for letting old ones go)
	kitty      *kittyImages      // real photos (Ghostty/kitty); nil = View photo buttons
	albumWait  []connector.Media // photos to open in the viewer once all have loaded
	gallery    []connector.Media // photos the full-screen view flips through

	status    string
	statusErr bool
	toast     string

	loggedOut bool      // Messenger rejected the login: main runs the login again
	started   time.Time // messages older than this never chime
	soundOn   bool
	soundName string
	soundVol  int // percent, 10-200
	lastChime time.Time

	// sound picker (:sound)
	soundPick  bool
	soundCur   int
	soundNames []string
	soundUndo  [3]string // name, volume, on — restored by esc
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
	sentMsg struct {
		err         error
		chatID      string        // with restore: the chat whose draft comes back
		restore     string        // the draft to put back after a failed file
		restoreAtts []pendingFile // the files in it that weren't sent
	}
	toastMsg      struct{ text string }
	clearToast    struct{}
	olderDoneMsg  struct{ err error }
	newestDoneMsg struct{ err error }
	pendingCheck  struct{}
	pruneMsg      struct{}
	relinkedMsg   struct{ chatID, msgID string }
	refillMsg     struct {
		chatID  string
		attempt int
	}
	refillDoneMsg struct {
		chatID  string
		attempt int
	}
	searchTick struct{ seq int }
	peopleMsg  struct {
		seq    int
		people []connector.Person
	}
)

func New(ctx context.Context, st *store.Store, conn connector.Connector, platform string) Model {
	in := textinput.New()
	in.Prompt = ""
	in.CharLimit = 4000
	m := Model{
		ctx:        ctx,
		st:         st,
		conn:       conn,
		platform:   platform,
		space:      firstFolder(st),
		imagesOn:   st.Setting(settingImage, "on") == "on",
		previewsOn: st.Setting(settingPreviews, "on") == "on",
		soundOn:    st.Setting(settingSound, "on") == "on",
		soundName:  st.Setting("sound_name", defaultSound),
		soundVol:   atoiOr(st.Setting("sound_volume", "100"), 100),
		started:    time.Now(),
		raw:        map[string][]byte{},
		imgs:       map[string]image.Image{},
		pending:    map[string]bool{},
		failed:     map[string]string{},
		input:      in,
		compose:    newBox(),
		draftAtts:  map[string][]pendingFile{},
		drafts:     st.Drafts(), // text left unsent last time
		dragFrom:   -1,
		dragTo:     -1,
		status:     "connecting…",
	}
	if kittyMode {
		m.kitty = newKittyImages(os.Stdout, filepath.Join(store.Dir(), "cache", "img"))
	}
	m.reloadChats()
	return m
}

// LoggedOut reports that tuon quit because Messenger logged you out.
func (m Model) LoggedOut() bool { return m.loggedOut }

// Warn shows a lasting warning in the status line (e.g. disk not encrypted).
func (m *Model) Warn(text string) {
	if text != "" {
		m.warning = text
	}
}

// Cleanup frees photos held by the terminal. Call after the program ends.
func (m Model) Cleanup() {
	if m.kitty != nil {
		m.kitty.clearAll()
	}
	// keep unsent text for next time; attached files aren't kept
	drafts := map[string]string{}
	for id, v := range m.drafts {
		drafts[id] = draftText(v)
	}
	if m.chat != nil && m.compose.Value() != "" {
		drafts[m.chat.ID] = draftText(m.compose.Value())
	}
	_ = m.st.SaveDrafts(drafts)
}

// draftText: a draft without its [Image #n] / [File #n] tokens.
func draftText(v string) string { return strings.TrimSpace(tokenRe.ReplaceAllString(v, "")) }

func (m Model) Init() tea.Cmd {
	return tea.Batch(func() tea.Msg { return startedMsg{m.conn.Start(m.ctx)} }, pruneLater(), idleLater())
}

// KeepMessages: the newest messages kept per chat (older ones load again
// from Messenger when you scroll back).
const KeepMessages = 1000

// pruneEvery: how often a long session trims back to KeepMessages.
const pruneEvery = 30 * time.Minute

func pruneLater() tea.Cmd {
	return tea.Tick(pruneEvery, func(time.Time) tea.Msg { return pruneMsg{} })
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
		if errors.Is(msg.err, messenger.ErrLoggedOut) { // quit; main asks you to log in again
			m.loggedOut = true
			return m, tea.Quit
		}
		if msg.err != nil {
			m.status, m.statusErr = "login failed: "+msg.err.Error()+" — update ~/.config/tuon/cookies.txt", true
		} else {
			m.status, m.statusErr = "syncing…", false
			m.st.Self = m.conn.SelfID()
			_ = m.st.SetSetting(store.SettingAccount, m.st.Self) // so a login as someone else can tell
		}
		m.reloadChats()
		if msg.err != nil {
			return m, nil
		}
		// messages still loading from an earlier run: keep trying the backup
		var refills []tea.Cmd
		for _, id := range m.st.MissingChats() {
			refills = append(refills, m.startRefill(id))
		}
		return m, tea.Batch(refills...)

	case EventsMsg:
		return m, m.applyEvents(msg)

	case mediaMsg:
		delete(m.pending, msg.ref)
		if msg.err != nil {
			m.failed[msg.ref] = msg.err.Error()
			if cmd := m.relink(msg.ref, msg.err); cmd != nil {
				return m, cmd
			}
		} else {
			m.raw[msg.ref] = msg.raw
			if msg.img != nil {
				m.imgs[msg.ref] = msg.img
			}
			m.remember(msg.ref)
		}
		m.keepSelectionVisible()
		if len(m.albumWait) > 0 {
			return m, m.previewAlbum(m.albumWait) // opens once every photo is in
		}
		return m, nil

	case sentMsg:
		if msg.err == nil {
			return m, nil
		}
		if msg.restore != "" {
			m.restoreDraft(msg.chatID, msg.restore, msg.restoreAtts)
			return m, m.flash("Not sent: " + sendError(msg.err) + ". Your message is back in the box.")
		}
		return m, m.flash("Not sent: " + sendError(msg.err))

	case relinkedMsg:
		// the chat's messages came back with fresh links: retry that photo
		cmd := m.reloadMessages()
		if m.chat == nil || m.chat.ID != msg.chatID {
			return m, cmd
		}
		for _, x := range m.msgs {
			if x.ID == msg.msgID && len(m.albumWait) > 0 {
				m.albumWait = nil
				for _, md := range x.Media {
					if md.Kind == connector.Photo && md.Ref != "" {
						return m, tea.Batch(cmd, m.openPhoto(md))
					}
				}
			}
		}
		return m, cmd

	case pruneMsg:
		_ = m.st.Prune(KeepMessages)
		return m, tea.Batch(m.reloadMessages(), pruneLater())

	case pendingCheck:
		m.checkAt = time.Time{}
		return m, m.reloadMessages()

	case refillMsg:
		return m, m.refill(msg.chatID, msg.attempt)

	case refillDoneMsg:
		if msg.attempt+1 < maxRefills && m.st.Waiting(msg.chatID) {
			return m, m.scheduleRefill(msg.chatID, msg.attempt+1)
		}
		delete(m.refilling, msg.chatID)
		return m, nil

	case newestDoneMsg:
		if msg.err != nil && m.chat != nil && m.chat.Encrypted {
			return m, m.flash("couldn't load messages from the backup: " + msg.err.Error())
		}
		return m, nil

	case olderDoneMsg:
		m.loadingOlder = false
		if msg.err != nil && m.chat != nil && m.chat.Encrypted {
			return m, m.flash("couldn't load older messages: " + msg.err.Error())
		}
		return m, nil

	case searchTick:
		if msg.seq != m.searchSeq || len([]rune(m.query)) < 2 {
			return m, nil
		}
		conn, ctx, q, seq := m.conn, m.ctx, m.query, msg.seq
		return m, func() tea.Msg {
			ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()
			found, _ := conn.SearchPeople(ctx, q)
			return peopleMsg{seq: seq, people: found}
		}

	case peopleMsg:
		if msg.seq != m.searchSeq {
			return m, nil // an older search
		}
		selected := m.rowKey(m.cursor) // keep the highlight on the same row's person
		m.remote = nil
		for _, p := range msg.people {
			if !p.Group {
				_ = m.st.UpsertContact(p.ID, p.Name)
			}
			m.remote = append(m.remote, store.Person{ID: p.ID, Name: p.Name, Group: p.Group, Encrypted: p.Encrypted})
		}
		m.updatePeople()
		m.restoreCursor(selected)
		return m, m.preview()

	case toastMsg:
		return m, m.flash(msg.text)

	case attachedMsg:
		if msg.err != nil {
			return m, m.flash("couldn't attach: " + msg.err.Error())
		}
		for _, f := range msg.files {
			m.attSeq++
			kind := "File"
			if f.IsImage() {
				kind = "Image"
			}
			tok := fmt.Sprintf("[%s #%d]", kind, m.attSeq)
			m.atts = append(m.atts, pendingFile{token: tok, file: f})
			ins := tok + " "
			if v := []rune(m.compose.Value()); m.compose.Position() > 0 && v[m.compose.Position()-1] != ' ' && v[m.compose.Position()-1] != '\n' {
				ins = " " + ins
			}
			m.compose.InsertString(ins)
		}
		return m, nil

	case pasteTextMsg:
		m.compose.InsertString(strings.TrimRight(string(msg), "\n"))
		m.updateSuggestions()
		return m, nil

	case copiedMsg:
		if msg.err != nil {
			return m, m.flash("couldn't copy: " + msg.err.Error())
		}
		clear := tea.Tick(clipboardKeep, func(time.Time) tea.Msg { return clipClear{msg.text} })
		if msg.n == 1 {
			return m, tea.Batch(clear, m.flash("Copied (clears in a minute)"))
		}
		return m, tea.Batch(clear, m.flash(fmt.Sprintf("Copied %d messages (clears in a minute)", msg.n)))

	case clipClear:
		text := msg.text
		return m, func() tea.Msg {
			// only if it's still ours: never wipe something copied since
			if text != "" && readClipboard() == strings.TrimRight(text, "\r\n") {
				_ = writeClipboard("")
			}
			return nil
		}

	case clearToast:
		m.toast = ""
		return m, nil

	case tea.KeyMsg:
		if m.locked {
			return m.onLockedKey(msg)
		}
		m.lastInput = time.Now()
		return m.onKey(msg)

	case tea.MouseMsg:
		if m.locked {
			return m, nil
		}
		m.lastInput = time.Now()
		return m.onMouse(msg)

	case idleTick:
		if m.lockOn() && m.lock.after > 0 && !m.locked && time.Since(m.lastInput) >= m.lock.after {
			return m, tea.Batch(m.lockNow(), idleLater())
		}
		return m, idleLater()

	case unlockedMsg:
		return m.onUnlocked(msg)
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
	var refills []tea.Cmd
	for _, e := range evts {
		switch e := e.(type) {
		case connector.ContactEvent:
			_ = m.st.UpsertContact(e.ID, e.Name)
		case connector.ChatAliasEvent:
			_ = m.st.Merge(e.From, e.To)
			if d, ok := m.drafts[e.From]; ok { // an unsent draft follows its chat
				to := m.st.Resolve(e.To)
				if m.drafts[to] == "" {
					m.drafts[to], m.draftAtts[to] = d, m.draftAtts[e.From]
				}
				delete(m.drafts, e.From)
				delete(m.draftAtts, e.From)
			}
		case connector.ChatEvent:
			e.Chat.ID = m.st.Resolve(e.Chat.ID)
			if e.Chat.ID == openID {
				e.Chat.Unread = false
			}
			_ = m.st.UpsertChat(e.Chat)
			// Messenger says an encrypted chat moved on, but no message came
			// through the encrypted connection: fetch it from the backup.
			if c, err := m.st.Chat(e.Chat.ID); err == nil && c.Encrypted && e.Chat.LastActivity.After(m.started) &&
				m.st.Behind(e.Chat.ID, e.Chat.LastActivity) {
				refills = append(refills, m.startRefill(e.Chat.ID))
			}
		case connector.MessageEvent:
			e.Message.ChatID = m.st.Resolve(e.Message.ChatID)
			isNew, _ := m.st.AddMessage(e.Message, e.Message.ChatID == openID)
			if isNew && store.IsMissing(e.Message.Media) {
				refills = append(refills, m.startRefill(e.Message.ChatID))
			}
			if isNew && !e.Message.FromMe && e.Message.Time.After(m.started) {
				if c, err := m.st.Chat(e.Message.ChatID); err == nil && !c.Muted {
					chime = true
				}
			}
		case connector.MessageRemovedEvent:
			_ = m.st.RemoveMessage(e.ID)
		case connector.ReactionEvent:
			e.ChatID = m.st.Resolve(e.ChatID)
			_ = m.st.SetReaction(e)
		case connector.ParticipantEvent:
			e.ChatID = m.st.Resolve(e.ChatID)
			_ = m.st.AddParticipant(e)
		case connector.KeysTroubleEvent:
			who := e.UserID
			if c, err := m.st.Chat(m.st.Resolve(e.UserID)); err == nil && c.Display != "" {
				who = c.Display
			}
			m.Warn(fmt.Sprintf("Messages from %s keep failing to decrypt live (they still arrive through your backup). If this keeps up, quit and run `tuon relink`.", firstName(who)))
		case connector.StatusEvent:
			m.status, m.statusErr = e.Text, e.Err != nil
			if e.Err != nil {
				m.status += ": " + e.Err.Error()
			}
		}
	}
	_ = m.st.MergeOneToOne()
	if openID != "" {
		openID = m.st.Resolve(openID)
		_ = m.st.MarkRead(openID)
	}
	m.reloadChats()
	cmd := tea.Batch(append(refills, m.reloadMessages())...)
	if chime && m.soundOn && time.Since(m.lastChime) > 2*time.Second {
		m.lastChime = time.Now()
		name, vol := m.soundName, m.soundVol
		return tea.Batch(cmd, func() tea.Msg { playSound(name, vol); return nil })
	}
	return cmd
}

func atoiOr(s string, def int) int {
	var n int
	if _, err := fmt.Sscan(s, &n); err != nil {
		return def
	}
	return n
}

// playChime: one soft tick for new messages (macOS "Tink", half volume).
// playChime: one soft tick for new messages.

func (m *Model) reloadChats() {
	selected := m.rowKey(m.cursor)
	m.spaces = m.st.Spaces()
	known := false
	for _, sp := range m.spaces {
		known = known || sp == m.space
	}
	if !known && m.space != store.Unread { // the folder was renamed or deleted
		m.space = m.spaces[0]
	}
	m.chats, _ = m.st.Chats(m.space, m.query)
	m.counts = m.st.UnreadCounts()
	// keep the cursor on the same chat when the list reorders
	m.updatePeople()
	m.cursor = min(m.cursor, max(0, m.rowCount()-1))
	m.restoreCursor(selected)
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
	lookup := tea.Batch(m.lookupNames(), m.notSentTimer())
	if !m.drawsPhotos() {
		return lookup // photos show as "View photo": load them only when opened
	}
	var cmds []tea.Cmd
	// only the most recent photos, so opening a chat stays fast
	for i := len(m.msgs) - 1; i >= 0 && i >= len(m.msgs)-12; i-- {
		for _, md := range m.msgs[i].Media {
			if cmd := m.fetch(md); cmd != nil {
				cmds = append(cmds, cmd)
			}
		}
	}
	return tea.Batch(append(cmds, lookup)...)
}

// relink: plain-chat photo links expire after about a month. When one
// does, ask Messenger for that stretch of the chat again: the messages come
// back with fresh links. Once per photo.
func (m *Model) relink(ref string, err error) tea.Cmd {
	e := err.Error()
	if m.chat == nil || m.chat.Encrypted || m.relinked[ref] ||
		!(strings.Contains(e, "HTTP 403") || strings.Contains(e, "HTTP 410") || strings.Contains(e, "expired")) {
		return nil
	}
	for _, x := range m.msgs {
		for _, md := range x.Media {
			if md.Ref != ref {
				continue
			}
			if m.relinked == nil {
				m.relinked = map[string]bool{}
			}
			m.relinked[ref] = true
			conn, ctx, chat, msgID, at := m.conn, m.ctx, m.chat.Chat, x.ID, x.Time
			return func() tea.Msg {
				conn.KnowChat(chat)
				ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
				defer cancel()
				_ = conn.LoadOlder(ctx, chat.ID, at.Add(time.Second), "")
				return relinkedMsg{chat.ID, msgID}
			}
		}
	}
	return nil
}

// notSent: a message of yours Messenger never confirmed.
func (m Model) notSent(msg store.MessageRow) bool {
	return msg.FromMe && msg.Pending && time.Since(msg.Time) > notSentAfter
}

// retry sends a "not sent" message again: its old copy goes, a new one
// shows as "sending…". Files can't be resent (tuon doesn't keep them).
func (m Model) retry(msg store.MessageRow) (tea.Model, tea.Cmd) {
	if msg.Text == "" {
		return m, m.flash("Files can't be resent from here: delete it (d) and attach them again")
	}
	_ = m.st.RemoveMessage(msg.ID)
	var reply *connector.ReplyRef
	if msg.ReplyToID != "" {
		reply = &connector.ReplyRef{ID: msg.ReplyToID}
		for _, x := range m.msgs {
			if x.ID == msg.ReplyToID {
				reply.SenderID = x.SenderID
			}
		}
	}
	m.selecting, m.selID = false, ""
	reload := m.reloadMessages()
	send := sendAll(m.conn, m.ctx, m.chat.Chat, msg.Text, nil, nil, reply)
	return m, tea.Batch(reload, send, m.compose.Focus(), m.flash("Sending again…"))
}

// notSentTimer wakes the UI when a message still sending passes 45 s, so
// "not sent" shows even if nothing else happens on screen.
func (m *Model) notSentTimer() tea.Cmd {
	var due time.Time
	for _, msg := range m.msgs {
		if at := msg.Time.Add(notSentAfter + 100*time.Millisecond); msg.Pending && at.After(time.Now()) && (due.IsZero() || at.Before(due)) {
			due = at
		}
	}
	if due.IsZero() || (!m.checkAt.IsZero() && !due.Before(m.checkAt)) {
		return nil
	}
	m.checkAt = due
	return tea.Tick(time.Until(due), func(time.Time) tea.Msg { return pendingCheck{} })
}

// lookupNames asks Messenger, once per run, for the names of senders in
// the open chat that tuon only knows by ID.
func (m *Model) lookupNames() tea.Cmd {
	var ids []string
	for _, msg := range m.msgs {
		if msg.SenderName != "" || msg.FromMe || msg.SenderID == "" || m.askedName[msg.SenderID] {
			continue
		}
		if m.askedName == nil {
			m.askedName = map[string]bool{}
		}
		m.askedName[msg.SenderID] = true
		ids = append(ids, msg.SenderID)
	}
	if len(ids) == 0 {
		return nil
	}
	conn, ctx := m.conn, m.ctx
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		conn.LookupPeople(ctx, ids)
		return nil
	}
}

// drawsPhotos: whether this terminal shows photos in the chat at all.
func (m Model) drawsPhotos() bool { return m.imagesOn && m.kitty != nil }

// fetch loads a photo. Downloaded bytes are kept small (compressed); the
// decoded picture, which is far bigger, is made only when it will be drawn.
func (m *Model) fetch(md connector.Media) tea.Cmd {
	if md.Kind != connector.Photo || md.Ref == "" || m.pending[md.Ref] || m.failed[md.Ref] != "" {
		return nil
	}
	decode := m.drawsPhotos() || m.mode == modeImage
	if raw := m.raw[md.Ref]; raw != nil {
		if !decode || m.imgs[md.Ref] != nil {
			return nil
		}
		m.pending[md.Ref] = true
		return func() tea.Msg { // decode again from memory, no download
			img, err := decodeImage(raw)
			return mediaMsg{ref: md.Ref, raw: raw, img: img, err: err}
		}
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
		if !decode {
			return mediaMsg{ref: md.Ref, raw: b}
		}
		img, err := decodeImage(b)
		return mediaMsg{ref: md.Ref, raw: b, img: img, err: err}
	}
}

const (
	keepDecoded = 12 // decoded photos (big) kept in memory
	keepRaw     = 40 // downloaded photos (small) kept in memory
)

// remember keeps the newest photos and lets older ones go, so memory
// stays flat however many photos you scroll past.
func (m *Model) remember(ref string) {
	for i, r := range m.recent {
		if r == ref {
			m.recent = append(m.recent[:i], m.recent[i+1:]...)
			break
		}
	}
	m.recent = append(m.recent, ref)
	for i, r := range m.recent {
		age := len(m.recent) - 1 - i
		if age >= keepDecoded {
			delete(m.imgs, r)
		}
		if age >= keepRaw {
			delete(m.raw, r)
		}
	}
	if len(m.recent) > keepRaw {
		m.recent = m.recent[len(m.recent)-keepRaw:]
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
	if m.soundPick {
		return m.onSoundKey(k)
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
	m.compose.SetWidth(max(10, m.rightWidth()-4))
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
		case inputSearch: // open what's highlighted, right away
			if p := m.personAt(m.cursor); p != nil {
				return m, m.openPerson(*p)
			}
			if m.cursor < len(m.chats) {
				c := m.chats[m.cursor]
				m.query, m.remote, m.found = "", nil, nil
				m.reloadChats()
				return m, m.openChat(c)
			}
		}
		return m, m.preview()
	case "down", "up":
		if m.inputKind == inputSearch { // move through results while searching
			if k.String() == "down" {
				m.cursor = min(m.cursor+1, max(0, m.rowCount()-1))
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
		m.remote = nil
		m.reloadChats()
		m.searchSeq++
		seq := m.searchSeq
		ask := tea.Tick(350*time.Millisecond, func(time.Time) tea.Msg { return searchTick{seq} })
		return m, tea.Batch(cmd, m.preview(), ask)
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
	case "ctrl+o": // newest photo, no need to select it first
		if imgs := m.chatImages(); len(imgs) > 0 {
			return m, m.openPhoto(imgs[len(imgs)-1])
		}
		return m, m.flash("no photos in this chat")
	case "backspace": // right after a file token: remove the whole token
		v := []rune(m.compose.Value())
		pos := min(m.compose.Position(), len(v))
		if tok := tokenBefore(string(v[:pos])); tok != "" {
			start := pos - len([]rune(tok))
			m.compose.SetValue(string(v[:start]) + string(v[pos:]))
			m.compose.SetCursor(start)
			return m, nil
		}
	case "esc", "tab":
		if k.String() == "esc" && m.replyTo != nil { // esc first cancels the reply
			m.replyTo = nil
			return m, nil
		}
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
		val, files := splitDraft(m.compose.Value(), m.atts)
		if val == "" && len(files) == 0 {
			return m, nil
		}
		mentions := keptMentions(val, m.mentions)
		m.compose.SetValue("")
		delete(m.drafts, m.chat.ID)
		delete(m.draftAtts, m.chat.ID)
		m.mentions, m.sugg, m.suggOff, m.atts, m.attSeq = nil, nil, false, nil, 0
		m.selID, m.scroll = "", 0
		var reply *connector.ReplyRef
		replyLost := m.replyTo != nil && val == ""
		if m.replyTo != nil && val != "" {
			reply = &connector.ReplyRef{ID: m.replyTo.ID, SenderID: m.replyTo.SenderID}
		}
		m.replyTo = nil
		cmd := sendAll(m.conn, m.ctx, m.chat.Chat, val, mentions, files, reply)
		if replyLost { // Messenger replies carry text; files alone go as plain messages
			return m, tea.Batch(cmd, m.flash("Sent without the reply: add some text to reply with files"))
		}
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

// sendError says why a send failed in plain words; library text like
// "dgw: socket is not open" means the connection is down.
func sendError(err error) string {
	s := err.Error()
	low := strings.ToLower(s)
	for _, offline := range []string{"socket is not open", "not connected", "websocket", "connection refused", "no such host", "network is unreachable", "i/o timeout", "context deadline exceeded"} {
		if strings.Contains(low, offline) {
			return "you're offline or Messenger isn't reachable"
		}
	}
	return s
}

// restoreDraft puts an unsent message back: in the box if that chat is
// open and the box is empty, else as the chat's draft (never over one).
func (m *Model) restoreDraft(chatID, text string, files []pendingFile) {
	for _, f := range files { // keep new [Image #n] numbers clear of these
		var n int
		if _, err := fmt.Sscanf(f.token[strings.LastIndex(f.token, "#")+1:], "%d", &n); err == nil {
			m.attSeq = max(m.attSeq, n)
		}
	}
	if m.chat != nil && m.chat.ID == chatID && m.compose.Value() == "" {
		m.compose.SetValue(text)
		m.compose.CursorEnd()
		m.atts = files
		return
	}
	if m.drafts[chatID] == "" {
		m.drafts[chatID], m.draftAtts[chatID] = text, files
	}
}

// leaveChat goes back to the list, keeping the draft.
func (m *Model) leaveChat() {
	if m.chat != nil {
		if v := m.compose.Value(); v != "" {
			m.drafts[m.chat.ID], m.draftAtts[m.chat.ID] = v, m.atts
		} else {
			delete(m.drafts, m.chat.ID)
			delete(m.draftAtts, m.chat.ID)
		}
	}
	m.compose.Blur()
	m.focus, m.selecting, m.sugg = focusList, false, nil
	m.selID, m.replyTo = "", nil
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
	case "mute":
		id := m.targetChat()
		if id == "" {
			return m, nil
		}
		_ = m.st.ToggleMute(id)
		m.reloadChats()
		c, err := m.st.Chat(id)
		if err != nil {
			return m, nil
		}
		if c.Muted {
			return m, m.flash("Muted " + termSafe(c.Display) + ": no sound, not in Unread")
		}
		return m, m.flash("Unmuted " + termSafe(c.Display))
	case "sound off", "sound on":
		m.soundOn = cmd == "sound on"
		_ = m.st.SetSetting(settingSound, strings.TrimPrefix(cmd, "sound "))
		return m, m.flash("Sound " + strings.TrimPrefix(cmd, "sound "))
	case "sound", "sounds", "notification", "notifications":
		m.openSoundPicker()
	case "images off", "images on", "images", "photos off", "photos on", "photos":
		switch {
		case strings.HasSuffix(cmd, " on"):
			m.imagesOn = true
		case strings.HasSuffix(cmd, " off"):
			m.imagesOn = false
		default:
			m.imagesOn = !m.imagesOn
		}
		_ = m.st.SetSetting(settingImage, map[bool]string{true: "on", false: "off"}[m.imagesOn])
		note := "Photos off: they show as a View photo button"
		switch {
		case m.kitty == nil:
			// Terminal.app and the like can't draw photos at all
			note = "This terminal can't show photos in the chat, so photos stay as View photo buttons either way. Ghostty, kitty or WezTerm show them."
		case m.imagesOn:
			note = "Photos on"
		}
		return m, tea.Batch(m.reloadMessages(), m.flash(note))
	case "previews off", "previews on", "previews":
		switch {
		case strings.HasSuffix(cmd, " on"):
			m.previewsOn = true
		case strings.HasSuffix(cmd, " off"):
			m.previewsOn = false
		default:
			m.previewsOn = !m.previewsOn
		}
		_ = m.st.SetSetting(settingPreviews, map[bool]string{true: "on", false: "off"}[m.previewsOn])
		if m.previewsOn {
			return m, m.flash("Previews on")
		}
		return m, m.flash("Previews off: no message text until you open a chat")
	case "help":
		m.mode = modeHelp
	case "lock":
		if !m.lockOn() {
			return m, m.flash("No lock set up")
		}
		return m, m.lockNow()
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
	m.personView = nil
	if m.focus != focusList {
		return nil
	}
	if p := m.personAt(m.cursor); p != nil {
		m.personView, m.chat = p, nil
		return nil
	}
	if m.cursor >= len(m.chats) {
		return nil
	}
	c := m.chats[m.cursor]
	if m.chat != nil && m.chat.ID == c.ID {
		return nil
	}
	m.chat = &c
	m.selID, m.scroll, m.selecting, m.reacting = "", 0, false, false
	if !m.previewsOn { // don't even load them while browsing
		return nil
	}
	return tea.Batch(m.reloadMessages(), m.loadNewest())
}

func (m Model) onListKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	move := func(to int) (tea.Model, tea.Cmd) {
		m.cursor = max(0, min(to, m.rowCount()-1))
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
	case "u":
		return space(store.Unread)
	case "right", "l":
		return space(m.nextSpace(1))
	case "left", "h":
		return space(m.nextSpace(-1))
	case "f":
		m.folders, m.folderCur, m.folderAskRm, m.folderMove = true, 0, false, false
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
		if p := m.personAt(m.cursor); p != nil {
			return m, m.openPerson(*p)
		}
		if m.cursor < len(m.chats) {
			return m, m.openChat(m.chats[m.cursor])
		}
	}
	return m, nil
}

func (m Model) nextSpace(d int) store.Space {
	tabs := m.tabSpaces()
	for i, x := range tabs {
		if x == m.space {
			return tabs[(i+d+len(tabs))%len(tabs)]
		}
	}
	return tabs[0]
}

// unreadTotal: unread chats across all folders.
func (m Model) unreadTotal() int {
	n := 0
	for _, c := range m.counts {
		n += c
	}
	return n
}

// tabSpaces: the tabs shown. Unread comes first, but only while something
// is unread (or you're on it).
func (m Model) tabSpaces() []store.Space {
	if m.unreadTotal() > 0 || m.space == store.Unread {
		return append([]store.Space{store.Unread}, m.spaces...)
	}
	return m.spaces
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
	shift := func(d int) {
		if cur == store.Inbox {
			return
		}
		to := m.folderCur + d
		if to < 0 || to > n-2 { // Inbox stays last
			return
		}
		if m.st.MoveSpace(cur, d) == nil {
			m.folderCur = to
		}
		m.reloadChats()
	}
	if m.folderMove { // carrying a folder: arrows move it
		switch k.String() {
		case "up", "k", "shift+up", "K":
			shift(-1)
		case "down", "j", "shift+down", "J":
			shift(1)
		case "enter", "m", "esc", " ":
			m.folderMove = false
		}
		return m, nil
	}
	switch k.String() {
	case "esc", "f", "q":
		m.folders = false
	case "m", " ":
		if cur == store.Inbox {
			return m, m.flash("Inbox always stays last")
		}
		m.folderMove = true
	case "up", "k":
		m.folderCur = max(0, m.folderCur-1)
	case "down", "j":
		m.folderCur = min(n-1, m.folderCur+1)
	case "shift+up", "K":
		shift(-1)
	case "shift+down", "J":
		shift(1)
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
	m.atts, m.attSeq = m.draftAtts[c.ID], len(m.draftAtts[c.ID])
	m.sizeInput()
	focus := m.compose.Focus()
	_ = m.st.MarkRead(c.ID)
	cmd := m.reloadMessages()
	m.reloadChats()
	newest := m.loadNewest()
	if len(m.msgs) < 20 && newest == nil {
		return tea.Batch(focus, cmd, m.loadOlder())
	}
	return tea.Batch(focus, cmd, newest)
}

// loadNewest fills an encrypted chat from the PIN backup once per run:
// messages that couldn't be decrypted live, or arrived while tuon was
// closed, are there.
func (m *Model) loadNewest() tea.Cmd {
	if m.chat == nil || !m.chat.Encrypted || m.synced[m.chat.ID] {
		return nil
	}
	if m.synced == nil {
		m.synced = map[string]bool{}
	}
	m.synced[m.chat.ID] = true
	known := m.chat.Chat
	conn, ctx, id := m.conn, m.ctx, m.chat.ID
	return func() tea.Msg {
		conn.KnowChat(known) // its ID may be the person's, not a thread the connector has seen
		ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		return newestDoneMsg{conn.LoadOlder(ctx, id, time.Now().Add(time.Minute), "")}
	}
}

// notSentAfter: a message Messenger hasn't confirmed by then shows "not sent".
const notSentAfter = 45 * time.Second

// refillAfter: when to look in the PIN backup again for a message that
// couldn't be decrypted live. The phone has to back it up first, which
// takes seconds to minutes. After the list runs out, every 5 minutes.
var refillAfter = []time.Duration{8 * time.Second, 20 * time.Second, 40 * time.Second, time.Minute, 2 * time.Minute, 3 * time.Minute, 5 * time.Minute}

// maxRefills: about two hours of checks, then the next start or opening
// the chat tries again.
const maxRefills = 30

// startRefill begins the backup checks for a chat, once at a time.
func (m *Model) startRefill(chatID string) tea.Cmd {
	if m.refilling[chatID] {
		return nil
	}
	if m.refilling == nil {
		m.refilling = map[string]bool{}
	}
	m.refilling[chatID] = true
	return m.scheduleRefill(chatID, 0)
}

func (m *Model) scheduleRefill(chatID string, attempt int) tea.Cmd {
	wait := refillAfter[min(attempt, len(refillAfter)-1)]
	return tea.Tick(wait, func(time.Time) tea.Msg { return refillMsg{chatID, attempt} })
}

// refill fetches a chat's newest backed-up messages, which replace any
// "loading" placeholders with the same IDs.
func (m *Model) refill(chatID string, attempt int) tea.Cmd {
	c, err := m.st.Chat(chatID)
	if err != nil || !m.st.Waiting(chatID) {
		delete(m.refilling, chatID)
		return nil
	}
	conn, ctx := m.conn, m.ctx
	return func() tea.Msg {
		conn.KnowChat(c.Chat)
		ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		_ = conn.LoadOlder(ctx, chatID, time.Now().Add(time.Minute), "")
		return refillDoneMsg{chatID, attempt}
	}
}

func (m *Model) loadOlder() tea.Cmd {
	if m.chat == nil || m.loadingOlder {
		return nil
	}
	m.loadingOlder = true
	before, beforeID := time.Now(), ""
	if len(m.msgs) > 0 {
		before, beforeID = m.msgs[0].Time, m.msgs[0].ID
	}
	known := m.chat.Chat
	conn, ctx, id := m.conn, m.ctx, m.chat.ID
	return func() tea.Msg {
		conn.KnowChat(known)
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
	case "enter": // reply to the selected message, or retry one that didn't send
		if sel >= 0 && sel < len(m.msgs) && m.notSent(m.msgs[sel]) {
			return m.retry(m.msgs[sel])
		}
		if sel >= 0 && sel < len(m.msgs) && !m.msgs[sel].Pending {
			r := m.msgs[sel]
			m.replyTo = &r
		}
		return back()
	case "esc", "i":
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
		if sel >= 0 && sel < len(m.msgs) && m.msgs[sel].Pending {
			return m, m.flash("Wait until it's sent to react")
		}
		if sel >= 0 && sel < len(m.msgs) {
			m.reacting = true
		}
	case "d", "x", "backspace", "delete": // drop a message that didn't send
		if sel >= 0 && sel < len(m.msgs) && m.notSent(m.msgs[sel]) {
			_ = m.st.RemoveMessage(m.msgs[sel].ID)
			cmd := m.reloadMessages()
			m.reloadChats()
			return m, tea.Batch(cmd, m.flash("Deleted the unsent message"))
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
		if sel >= 0 && sel < len(m.msgs) {
			if md, ok := fileIn(m.msgs[sel]); ok {
				return m, m.saveFile(md)
			}
		}
		if sel >= 0 && sel < len(m.msgs) && !hasPhoto(m.msgs[sel]) {
			for _, md := range m.msgs[sel].Media {
				if md.Kind == connector.Link && md.URL != "" {
					return m, m.openLinkSafe(md.URL)
				}
			}
			return m, m.flash("Nothing to open in this message (ctrl+o opens the newest photo)")
		}
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
		return m, m.openPhoto(imgs[m.imgIdx])
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
			if from < 0 {
				return m, nil
			}
			if from != to { // dragged over several messages: copy them
				return m, copyText(m.messagesText(from, to), to-from+1)
			}
			// a click: open its photo or link, save its file, or select the message
			if md, ok := fileIn(m.msgs[from]); ok {
				return m, m.saveFile(md)
			}
			for _, md := range m.msgs[from].Media {
				if md.Kind == connector.Photo && md.Ref != "" {
					return m, m.openPhoto(md)
				}
			}
			for _, md := range m.msgs[from].Media {
				if md.Kind == connector.Link && md.URL != "" {
					return m, m.openLinkSafe(md.URL)
				}
			}
			m.focus, m.selecting = focusChat, true
			m.compose.Blur()
			m.selectIndex(from)
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
				m.cursor = max(0, min(m.cursor+d, m.rowCount()-1))
				return m, m.preview()
			}
			return m, nil
		}
		m.scroll = max(0, m.scroll-3*d)
		return m, nil
	case tea.MouseButtonLeft:
		if inList && e.Y == 2 { // the folder tabs
			_, spans := m.tabLayout()
			for _, s := range spans {
				if e.X >= s.from && e.X < s.to {
					m.leaveChat()
					m.focus = focusList
					m.switchSpace(s.sp)
					return m, tea.Batch(tea.ClearScreen, m.preview())
				}
			}
			return m, nil
		}
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
			if p := m.personAt(i); p != nil { // someone found by search
				return m, m.openPerson(*p)
			}
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
	conn, ctx, chatID, chat := m.conn, m.ctx, m.chat.ID, m.chat.Chat
	return m, func() tea.Msg {
		conn.KnowChat(chat)
		ctx, cancel := context.WithTimeout(ctx, sendTimeout)
		defer cancel()
		if err := conn.React(ctx, chatID, msg.ID, msg.SenderID, emoji); err != nil {
			return toastMsg{"Reaction not sent: " + sendError(err)}
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

// viewImages: the photos the full-screen view flips through.
func (m Model) viewImages() []connector.Media {
	if len(m.gallery) > 0 {
		return m.gallery
	}
	return m.chatImages()
}

func (m Model) onImageKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	imgs := m.viewImages()
	if len(imgs) == 0 {
		m.mode = modeMain
		return m, nil
	}
	m.imgIdx = min(m.imgIdx, len(imgs)-1)
	cur := imgs[m.imgIdx]
	switch k.String() {
	case "esc", "q", "o":
		m.mode, m.gallery = modeMain, nil
	case "h", "left", "[":
		m.imgIdx = max(0, m.imgIdx-1)
		return m, m.fetch(imgs[m.imgIdx])
	case "l", "right", "]", " ":
		m.imgIdx = min(len(imgs)-1, m.imgIdx+1)
		return m, m.fetch(imgs[m.imgIdx])
	case "O":
		return m, m.saveImage(cur.Ref, true)
	case "s":
		return m, m.saveImage(cur.Ref, false)
	}
	return m, nil
}

func (m Model) saveImage(ref string, open bool) tea.Cmd {
	b := m.raw[ref]
	if b == nil {
		return func() tea.Msg { return toastMsg{"photo still loading"} }
	}
	return func() tea.Msg {
		if open {
			path, err := tempPhoto(b)
			if err == nil {
				err = openFile(path)
			}
			if err != nil {
				return toastMsg{"open failed: " + err.Error()}
			}
			return toastMsg{"opened in viewer"}
		}
		path := filepath.Join(downloadsDir(), "tuon-"+time.Now().Format("20060102-150405")+extFor(b))
		if err := os.WriteFile(path, b, 0o600); err != nil {
			return toastMsg{"save failed: " + err.Error()}
		}
		return toastMsg{"saved " + path}
	}
}

// openPhoto shows a photo: full screen inside Tuon where the terminal can
// draw it, otherwise the real file in the system's photo viewer.
// openPhoto opens a photo together with the rest of its album.
func (m *Model) openPhoto(md connector.Media) tea.Cmd {
	for i := range m.msgs {
		for _, x := range m.msgs[i].Media {
			if x.Ref == md.Ref {
				return m.openAlbum(m.albumAt(i), md.Ref)
			}
		}
	}
	return m.openAlbum([]connector.Media{md}, md.Ref)
}

// openAlbum shows photos as a gallery: full screen inside Tuon where the
// terminal can draw them, otherwise all of them in the system's viewer.
func (m *Model) openAlbum(photos []connector.Media, startRef string) tea.Cmd {
	if len(photos) == 0 {
		return nil
	}
	for _, p := range photos {
		delete(m.failed, p.Ref) // opening also retries failed photos
	}
	if m.kitty == nil {
		return m.previewAlbum(photos)
	}
	m.gallery, m.imgIdx = photos, 0
	for i, p := range photos {
		if p.Ref == startRef {
			m.imgIdx = i
		}
	}
	m.mode = modeImage
	return m.fetch(photos[m.imgIdx])
}

// albumAt: the photos sent together with message i. Several photos in one
// message, or photo-only messages sent back to back by the same person,
// count as one album.
func (m Model) albumAt(i int) []connector.Media {
	start, end := i, i
	if photoOnly(m.msgs[i]) {
		for start > 0 && sameAlbum(m.msgs[start-1], m.msgs[start]) {
			start--
		}
		for end < len(m.msgs)-1 && sameAlbum(m.msgs[end], m.msgs[end+1]) {
			end++
		}
	}
	var out []connector.Media
	for j := start; j <= end; j++ {
		for _, md := range m.msgs[j].Media {
			if md.Kind == connector.Photo && md.Ref != "" {
				out = append(out, md)
			}
		}
	}
	return out
}

func photoOnly(msg store.MessageRow) bool {
	if msg.Text != "" || len(msg.Media) == 0 {
		return false
	}
	for _, md := range msg.Media {
		if md.Kind != connector.Photo {
			return false
		}
	}
	return true
}

// sameAlbum: b continues a's album (same sender, both just photos, within a minute).
func sameAlbum(a, b store.MessageRow) bool {
	return photoOnly(a) && photoOnly(b) && a.SenderID == b.SenderID && b.Time.Sub(a.Time) < time.Minute
}

// previewAlbum opens photos in the system's viewer once all are loaded.
func (m *Model) previewAlbum(photos []connector.Media) tea.Cmd {
	var missing []tea.Cmd
	var data [][]byte
	for _, p := range photos {
		if b := m.raw[p.Ref]; b != nil {
			data = append(data, b)
			continue
		}
		if m.failed[p.Ref] != "" {
			continue // skip ones that can't load
		}
		if cmd := m.fetch(p); cmd != nil {
			missing = append(missing, cmd)
		}
		if m.pending[p.Ref] {
			missing = append(missing, nil)
		}
	}
	if len(missing) > 0 { // wait; mediaMsg calls back here as each arrives
		first := len(m.albumWait) == 0
		m.albumWait = photos
		if first {
			missing = append(missing, m.flash(fmt.Sprintf("Opening %d photo(s)…", len(photos))))
		}
		return tea.Batch(missing...)
	}
	m.albumWait = nil
	if len(data) == 0 {
		return m.flash("couldn't load the photos")
	}
	return func() tea.Msg {
		paths, err := tempAlbum(data)
		if err == nil {
			err = previewFiles(paths)
		}
		if err != nil {
			return toastMsg{"couldn't open photos: " + err.Error()}
		}
		return nil
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

// ---- sound picker ----

func (m *Model) openSoundPicker() {
	m.soundNames = soundNames()
	m.soundPick, m.soundCur = true, 0
	for i, n := range m.soundNames {
		if n == m.soundName {
			m.soundCur = i
		}
	}
	m.soundUndo = [3]string{m.soundName, fmt.Sprint(m.soundVol), fmt.Sprint(m.soundOn)}
}

func (m Model) onSoundKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	play := func() tea.Cmd {
		name, vol := m.soundName, m.soundVol
		return func() tea.Msg { playSound(name, vol); return nil }
	}
	switch k.String() {
	case "up", "k":
		if m.soundCur > 0 {
			m.soundCur--
			m.soundName = m.soundNames[m.soundCur]
			return m, play()
		}
	case "down", "j":
		if m.soundCur < len(m.soundNames)-1 {
			m.soundCur++
			m.soundName = m.soundNames[m.soundCur]
			return m, play()
		}
	case "left", "h", "-":
		m.soundVol = max(10, m.soundVol-10)
		return m, play()
	case "right", "l", "+", "=":
		m.soundVol = min(200, m.soundVol+10)
		return m, play()
	case " ", "p":
		return m, play()
	case "x":
		m.soundOn = !m.soundOn
	case "enter":
		m.soundPick = false
		_ = m.st.SetSetting("sound_name", m.soundName)
		_ = m.st.SetSetting("sound_volume", fmt.Sprint(m.soundVol))
		_ = m.st.SetSetting(settingSound, map[bool]string{true: "on", false: "off"}[m.soundOn])
		if !m.soundOn {
			return m, m.flash("Saved. Sound is off.")
		}
		return m, m.flash(fmt.Sprintf("Saved: %s at %d%%", m.soundName, m.soundVol))
	case "esc", "q":
		m.soundPick = false
		m.soundName = m.soundUndo[0]
		m.soundVol = atoiOr(m.soundUndo[1], 100)
		m.soundOn = m.soundUndo[2] == "true"
	}
	return m, nil
}

// ---- people search ----

// rowCount: chats, then people (while searching).
func (m Model) rowCount() int { return len(m.chats) + len(m.found) }

func (m Model) personAt(i int) *store.Person {
	if i >= len(m.chats) && i < m.rowCount() {
		p := m.found[i-len(m.chats)]
		return &p
	}
	return nil
}

// updatePeople lists people matching the search who aren't already shown
// as a chat: your contacts first, then Messenger's results.
func (m *Model) updatePeople() {
	m.found = nil
	q := strings.TrimSpace(m.query)
	if q == "" {
		m.remote = nil
		return
	}
	shown := map[string]bool{m.conn.SelfID(): true}
	for _, c := range m.chats {
		shown[c.ID] = true
	}
	add := func(p store.Person) {
		if !shown[p.ID] && p.Name != "" {
			shown[p.ID] = true
			m.found = append(m.found, p)
		}
	}
	for _, p := range m.st.SearchContacts(q, 15) {
		add(p)
	}
	for _, p := range m.remote {
		add(p)
	}
}

// openPerson opens your 1:1 chat with someone found in search.
func (m *Model) openPerson(p store.Person) tea.Cmd {
	chatID := p.ID
	if p.Group { // a group not in the list yet: add it, then load its messages
		if err := m.st.UpsertChat(connector.Chat{ID: chatID, Name: p.Name, Group: true, Encrypted: p.Encrypted}); err != nil {
			return m.flash(err.Error())
		}
	} else {
		var err error
		if chatID, err = m.conn.OpenDirect(m.ctx, p.ID); err != nil {
			return m.flash("couldn't open chat: " + err.Error())
		}
		if err := m.st.EnsureChat(chatID, true); err != nil {
			return m.flash(err.Error())
		}
	}
	c, err := m.st.Chat(chatID)
	if err != nil {
		return m.flash(err.Error())
	}
	m.stopInput()
	m.query, m.remote, m.found, m.personView = "", nil, nil, nil
	return m.openChat(c)
}

// fileIn: the received file in a message, if any.
func fileIn(msg store.MessageRow) (connector.Media, bool) {
	for _, md := range msg.Media {
		if md.Kind == connector.File {
			return md, true
		}
	}
	return connector.Media{}, false
}

// saveFile downloads a received file into Downloads and shows it in the
// file manager. It is never opened or run: it came from someone else.
func (m *Model) saveFile(md connector.Media) tea.Cmd {
	if md.Ref == "" {
		return m.flash("This file came in before tuon could fetch files. Open it on your phone.")
	}
	conn, ctx := m.conn, m.ctx
	return tea.Batch(m.flash("Saving "+termSafe(safeFileName(md.Name))+"…"), func() tea.Msg {
		ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		defer cancel()
		b, err := conn.FetchMedia(ctx, md)
		if err != nil {
			return toastMsg{"couldn't save the file: " + err.Error()}
		}
		path := uniquePath(filepath.Join(downloadsDir(), safeFileName(md.Name)))
		if err := os.WriteFile(path, b, 0o600); err != nil {
			return toastMsg{"couldn't save the file: " + err.Error()}
		}
		_ = markDownloaded(path) // the OS warns before running it, like a browser download
		_ = revealFile(path)
		return toastMsg{"Saved to " + shortHome(path)}
	})
}

func hasPhoto(msg store.MessageRow) bool {
	for _, md := range msg.Media {
		if md.Kind == connector.Photo && md.Ref != "" {
			return true
		}
	}
	return false
}

// openLinkSafe opens a link, but a risky one (plain http, an IP address,
// look-alike punycode, a user@ prefix, an odd port) needs a second press,
// with the reason shown first.
func (m *Model) openLinkSafe(url string) tea.Cmd {
	host, risk := linkRisk(url)
	if risk != "" && m.confirmURL != url {
		m.confirmURL = url
		return m.flash("Careful: " + termSafe(host) + " " + risk + ". Press again to open it anyway.")
	}
	m.confirmURL = ""
	return openLink(url)
}

// openLink opens a shared link in the browser.
func openLink(url string) tea.Cmd {
	return func() tea.Msg {
		if err := openURL(url); err != nil {
			return toastMsg{"couldn't open link: " + err.Error()}
		}
		return toastMsg{"Opened in your browser"}
	}
}

// rowKey names the row at i ("c:<chat>" or "p:<person>"), so the highlight
// can follow it when the list changes under it.
func (m Model) rowKey(i int) string {
	if p := m.personAt(i); p != nil {
		return "p:" + p.ID
	}
	if i >= 0 && i < len(m.chats) {
		return "c:" + m.chats[i].ID
	}
	return ""
}

func (m *Model) restoreCursor(key string) {
	if key == "" {
		return
	}
	for i := 0; i < m.rowCount(); i++ {
		if m.rowKey(i) == key {
			m.cursor = i
			return
		}
	}
}

// firstFolder is where tuon opens: your first folder (Unread aside).
func firstFolder(st *store.Store) store.Space {
	for _, sp := range st.Spaces() {
		if sp != store.Unread {
			return sp
		}
	}
	return store.Inbox
}
