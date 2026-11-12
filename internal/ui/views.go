package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/ralph/tuon/internal/connector"
	"github.com/ralph/tuon/internal/store"
)

// Layout (two panes, Messenger-style):
//
//	header                                   (1)
//	────────────┬──────────────────────────  (1)
//	tabs        │ chat title                 (1)
//	────────────┼──────────────────────────  (1)
//	chat list   │ messages                   (bodyH)
//	            │ ─────
//	            │ > input
//	────────────┴──────────────────────────  (1)
//	footer                                   (1)
//
// Below narrowWidth only the focused pane shows.

const narrowWidth = 72

func (m Model) narrow() bool { return m.w < narrowWidth }

func (m Model) leftWidth() int {
	if m.narrow() {
		if m.focus == focusChat && m.chat != nil {
			return 0
		}
		return m.w
	}
	return max(28, min(52, m.w*3/10))
}

func (m Model) rightWidth() int {
	lw := m.leftWidth()
	if lw == 0 {
		return m.w
	}
	if lw >= m.w {
		return 0
	}
	return m.w - lw - 1
}

func (m Model) bodyHeight() int { return max(3, m.h-6) }
func (m Model) chatHeight() int {
	rows := 1
	if m.composing() {
		rows = m.compose.Height()
	}
	return max(1, m.bodyHeight()-1-rows)
}
func (m Model) searching() bool { return m.inputKind == inputSearch || m.query != "" }

func (m Model) View() string {
	if m.w == 0 {
		return ""
	}
	var lines []string
	switch {
	case m.locked:
		lines = m.lockView() // nothing of the chats shows while locked
	case m.mode == modeHelp:
		lines = m.helpView()
	case m.mode == modeImage:
		lines = m.imageView()
	default:
		lines = m.mainView()
		m.sel.highlight(lines)
	}
	// always exactly h lines, each exactly w wide, on the ink background
	for len(lines) < m.h {
		lines = append(lines, "")
	}
	lines = lines[:m.h]
	for i, l := range lines {
		// a stray line break (from a name, snippet or error text) would add
		// a row and scroll the whole screen: flatten it
		if strings.ContainsAny(l, "\n\r\t") {
			l = strings.NewReplacer("\r\n", " ", "\n", " ", "\r", " ", "\t", " ").Replace(l)
		}
		if ansi.StringWidth(l) > m.w {
			l = ansi.Truncate(l, m.w, "")
		}
		lines[i] = paint(l, m.w)
	}
	// Make every line differ from the last frame (an invisible reset that
	// alternates), so Bubble Tea redraws all of them each time. If the
	// terminal ever scrolls behind our back, the next frame wipes it out
	// instead of leaving shifted copies of unchanged lines.
	frameTick = !frameTick
	tag := "\x1b[0m"
	if frameTick {
		tag = "\x1b[m"
	}
	for i := range lines {
		lines[i] += tag
	}
	// keep the terminal's line wrap off (some terminals reset it)
	lines[0] = "\x1b[?7l" + lines[0]
	return strings.Join(lines, "\n")
}

var frameTick bool // only touched from Bubble Tea's view goroutine

func (m Model) mainView() []string {
	lw, rw := m.leftWidth(), m.rightWidth()
	bh := m.bodyHeight()

	joinRule := func(mid string) string {
		switch {
		case lw == 0:
			return rule(rw)
		case rw == 0:
			return rule(lw)
		}
		return sFaint.Render(strings.Repeat("─", lw) + mid + strings.Repeat("─", rw))
	}
	join := func(l, r string) string {
		switch {
		case lw == 0:
			return fit(r, rw)
		case rw == 0:
			return fit(l, lw)
		}
		return fit(l, lw) + sFaint.Render("│") + fit(r, rw)
	}

	status := sMuted.Render(termSafe(m.status) + " ")
	if m.statusErr {
		status = sErr.Render(termSafe(m.status) + " ")
	}
	if m.warning != "" {
		status = sErr.Render("⚠ "+termSafe(m.warning)) + "  " + status
	}
	out := []string{
		spread(" "+sAccentB.Render(brand)+"  "+sMuted.Render(m.platform), status, m.w),
		joinRule("┬"),
		join(m.tabs(), m.chatTitle(rw)),
		joinRule("┼"),
	}

	left := m.listBody(lw, bh)
	right := m.chatBody(rw, bh)
	for i := 0; i < bh; i++ {
		out = append(out, join(left[i], right[i]))
	}
	out = append(out, joinRule("┴"), m.footer())

	if m.tagFor != "" {
		out = m.overlayTag(out, lw)
	}
	if m.folders {
		out = m.overlayFolders(out)
	}
	if m.soundPick {
		out = m.overlaySound(out)
	}
	return out
}

func (m Model) footer() string {
	switch {
	case m.inputKind == inputCommand:
		return " " + sAccent.Render(":") + m.input.View()
	case m.toast != "":
		return " " + sText.Render(termSafe(m.toast))
	case m.inputKind == inputFolder:
		return " " + sAccent.Render("Folder name: ") + m.input.View()
	case m.folderAskRm:
		return " " + sText.Render("Delete this folder? Its chats go to Inbox. ") + keyHints("y", "delete", "any key", "cancel")
	case m.soundPick:
		return keyHints("↑↓", "sound", "←→", "volume", "space", "play", "x", "sound on/off", "enter", "save", "esc", "cancel")
	case m.folders && m.folderMove:
		return keyHints("↑↓", "move folder", "enter", "done")
	case m.folders && len(m.spaces) > 0 && m.spaces[min(m.folderCur, len(m.spaces)-1)] == store.Inbox:
		return keyHints("↑↓", "pick", "a", "add", "esc", "close") + "   " + sMuted.Render("Inbox is built in: it can't be moved, renamed or deleted")
	case m.folders:
		return keyHints("↑↓", "pick", "m", "move", "a", "add", "r", "rename", "d", "delete", "esc", "close")
	case m.inputKind == inputSearch:
		return keyHints("enter", "done", "esc", "clear search")
	case m.composing() && len(m.sugg) > 0:
		return keyHints("↑↓", "choose", "tab", "tag person", "esc", "close")
	case m.composing():
		return keyHints("enter", "send", "alt+enter", "new line", "@", "tag", "@~/", "attach", "ctrl+v", "paste image", "ctrl+o", "newest photo", "↑", "pick message", "esc esc", "clear", "tab", "chats")
	case m.reacting:
		return keyHints("1-6", "react", "x", "remove mine", "esc", "cancel")
	case m.focus == focusChat && m.selecting && m.selIndex() >= 0 && m.selIndex() < len(m.msgs) && isPlaceholder(m.msgs[m.selIndex()]):
		return keyHints("d", "remove this line", "↑↓", "pick", "esc", "back to typing")
	case m.focus == focusChat && m.selecting && m.selIndex() >= 0 && m.selIndex() < len(m.msgs) && m.notSent(m.msgs[m.selIndex()]):
		return keyHints("enter", "send again", "d", "delete", "↑↓", "pick", "esc", "back to typing")
	case m.focus == focusChat && m.selecting:
		return keyHints("↑↓", "pick", "enter", "reply", "r", "react", "y", "copy", "o", "open", "esc", "back to typing", "tab", "chat list")
	default:
		return keyHints("↑↓", "browse", "←→ 1-9", "folders", "u", "unread", "enter", "open", "/", "search", "t", "move chat", "f", "edit folders", "?", "help")
	}
}

// ---- left: chat list ----

// tabs: "Unread ⟨5⟩   Favorite   Work ⟨2⟩   Inbox". The current tab is
// amber; unread counts are small amber pills. Keys live in the footer
// (1-9, u, ←→), so the tabs stay clean.
func (m Model) tabs() string { s, _ := m.tabLayout(); return s }

// tabSpan: where a tab sits on the tab row (cells, end exclusive).
type tabSpan struct {
	sp       store.Space
	from, to int
}

// tabLayout renders the tab row and says where each tab is, for clicks.
func (m Model) tabLayout() (string, []tabSpan) {
	type tab struct {
		sp      store.Space
		name    string
		n       int
		current bool
	}
	var tabs []tab
	for _, sp := range m.tabSpaces() {
		t := tab{sp: sp, name: store.Label(sp), n: m.counts[sp], current: sp == m.space}
		if sp == store.Unread {
			t.n = m.unreadTotal()
		}
		tabs = append(tabs, t)
	}
	render := func(t tab, nameW int) string {
		name := t.name
		if nameW > 0 {
			name = ansi.Truncate(name, nameW, "…")
		}
		out := sMuted.Render(name)
		if t.current {
			out = sAccentB.Render(name)
		}
		if t.n > 0 {
			out += " " + sBadge.Render(fmt.Sprintf(" %d ", t.n))
		}
		return out
	}
	build := func(sep string, nameW int) (string, []tabSpan) {
		var parts []string
		var spans []tabSpan
		x := 1
		for _, t := range tabs {
			r := render(t, nameW)
			w := ansi.StringWidth(r)
			spans = append(spans, tabSpan{t.sp, x, x + w})
			x += w + len(sep)
			parts = append(parts, r)
		}
		return " " + strings.Join(parts, sep), spans
	}
	for _, try := range []struct {
		sep string
		w   int
	}{{"    ", 0}, {"   ", 0}, {"  ", 0}, {"  ", 6}, {" ", 4}} {
		if s, spans := build(try.sep, try.w); ansi.StringWidth(s) <= m.leftWidth() {
			return s, spans
		}
	}
	return build(" ", 3)
}

const rowLines = 3 // name, snippet, gap

func (m Model) listRows() int {
	h := m.bodyHeight() - 1
	if m.searching() {
		h--
	}
	return max(1, h/rowLines)
}

func (m Model) listBody(lw, bh int) []string {
	if lw == 0 {
		return make([]string, bh)
	}
	out := []string{""}
	if m.inputKind == inputSearch {
		out = append(out, " "+sAccent.Render("/ ")+m.input.View())
	} else if m.query != "" {
		out = append(out, " "+sAccent.Render("/ ")+sText.Render(m.query))
	}
	if m.rowCount() == 0 {
		msg := " Nothing matches."
		if m.space == store.Unread && m.query == "" {
			msg = " All caught up."
		} else if m.space == store.Inbox && m.query == "" {
			msg = " All chats are sorted."
		} else if m.query == "" {
			msg = " Empty. Press 3, pick a chat, press t."
		}
		out = append(out, sMuted.Render(msg))
	}
	rows := m.listRows()
	off := 0
	if m.cursor >= rows {
		off = m.cursor - rows + 1
	}
	for i := off; i < off+rows && i < m.rowCount(); i++ {
		if p := m.personAt(i); p != nil {
			out = append(out, m.personRow(*p, i == m.cursor, lw)...)
			continue
		}
		out = append(out, m.chatRow(m.chats[i], i == m.cursor, lw)...)
	}
	for len(out) < bh {
		out = append(out, "")
	}
	return out[:bh]
}

// personRow: someone from people search (no chat with them in the list).
func (m Model) personRow(p store.Person, selected bool, lw int) []string {
	edge := " "
	if selected {
		edge = sAccent.Render("▎")
	}
	l1 := edge + sAccent.Render("@") + " " + sText.Render(fit(termSafe(p.Name), lw-4)) + " "
	what := "Person · enter to message"
	if p.Group {
		what = "Group chat · enter to open"
	}
	l2 := edge + "  " + sMuted.Render(fit(what, lw-4)) + " "
	if selected && m.focus == focusList {
		band := func(s string) string {
			return sSelBand.Render(strings.ReplaceAll(fit(s, lw), "\x1b[0m", "\x1b[0m"+bgSeq(cSelBand)))
		}
		return []string{band(l1), band(l2), ""}
	}
	return []string{l1, l2, ""}
}

// chatRow: name + time, snippet, then a gap line.
func (m Model) chatRow(c store.ChatRow, selected bool, lw int) []string {
	t := ago(c.LastActivity)
	nameW := max(4, lw-5-ansi.StringWidth(t))
	name := termSafe(c.Display)
	if c.Muted {
		name += " (muted)"
	}
	// a group chat carries a small mark right after its name; 1:1s stay plain
	mark := ""
	if c.Group {
		mark += " " + sGroupMark.Render(groupMark)
	}
	if c.Encrypted { // end-to-end encrypted: Meta can't read it
		mark += " " + lockMark
	}
	markW := ansi.StringWidth(mark)
	styled := func(st lipgloss.Style) string {
		if mark == "" {
			return st.Render(fit(name, nameW))
		}
		n := ansi.Truncate(name, max(1, nameW-markW), "…")
		pad := max(0, nameW-ansi.StringWidth(n)-markW)
		return st.Render(n) + mark + strings.Repeat(" ", pad)
	}

	edge, dot := " ", " "
	if c.Unread {
		dot = sAccent.Render(unreadDot)
	}
	nameS := styled(sText)
	if c.Unread {
		nameS = styled(sBold)
	}
	snippet := c.Snippet
	if !m.previewsOn { // shoulder-surfing: no message text in the list
		snippet = ""
		if c.Unread {
			snippet = "new message"
		}
	}
	snip := sMuted.Render(fit(termSafe(snippet), lw-4))
	if c.Muted {
		nameS = styled(sMuted)
	}
	if selected {
		edge = sAccent.Render("▎")
	}
	l1 := edge + dot + " " + nameS + " " + sMuted.Render(t) + " "
	l2 := edge + "  " + snip + " "
	if selected && m.focus == focusList {
		band := func(s string) string {
			return sSelBand.Render(strings.ReplaceAll(fit(s, lw), "\x1b[0m", "\x1b[0m"+bgSeq(cSelBand)))
		}
		return []string{band(l1), band(l2), ""}
	}
	return []string{l1, l2, ""}
}

// ---- right: open chat ----

func (m Model) chatTitle(rw int) string {
	if m.chat == nil || rw == 0 {
		return ""
	}
	c := m.chat
	kind := "1:1"
	if c.Group {
		kind = fmt.Sprintf("group, %d people", len(m.people)+1)
	}
	if c.Encrypted {
		kind += ", encrypted"
	}
	left := " " + sBold.Render(termSafe(c.Display))
	if c.Encrypted {
		left += " " + lockMark
	}
	if m.narrow() {
		left = " " + sAccent.Render("‹") + left
	}
	return spread(left, sMuted.Render("in "+store.Label(c.Space)+"   "+kind+" "), rw)
}

func (m Model) chatBody(rw, bh int) []string {
	if rw == 0 {
		return make([]string, bh)
	}
	out := make([]string, 0, bh)
	if m.chat != nil && !m.previewsOn && m.focus == focusList { // previews off: nothing until opened
		for i := 0; i < bh; i++ {
			out = append(out, "")
		}
		hint := "Previews are off. Press enter to open " + termSafe(m.chat.Display) + "."
		out[bh/2] = strings.Repeat(" ", max(0, (rw-ansi.StringWidth(hint))/2)) + sMuted.Render(hint)
		return out
	}
	if m.chat == nil {
		for i := 0; i < bh; i++ {
			out = append(out, "")
		}
		hint := "Pick a chat on the left and press enter."
		if m.personView != nil {
			hint = "Press enter to message " + termSafe(m.personView.Name) + "."
			if m.personView.Group {
				hint = "Press enter to open " + termSafe(m.personView.Name) + "."
			}
		}
		out[bh/2] = strings.Repeat(" ", max(0, (rw-len(hint))/2)) + sMuted.Render(hint)
		return out
	}

	ch := m.chatHeight()
	lines, _ := m.chatLines()
	scroll := min(m.scroll, max(0, len(lines)-ch))
	end := len(lines) - scroll
	start := max(0, end-ch)
	view := append([]string(nil), lines[start:end]...)
	if m.loadingOlder && start == 0 {
		view = append([]string{"  " + sMuted.Render("Loading older messages…")}, view...)
	}
	for len(view) < ch {
		view = append([]string{""}, view...)
	}
	if len(view) > ch {
		view = view[len(view)-ch:]
	}

	// pop-ups sit just above the composer
	var pop []string
	switch {
	case m.reacting:
		pop = []string{"", m.reactPicker()}
	case len(m.sugg) > 0:
		for i, p := range m.sugg {
			row := " " + termSafe(p.Label) + " "
			if i == m.suggIdx {
				row = sAccent.Render("▎") + sSelMsg.Render(row)
			} else {
				row = " " + sTheirs.Render(row)
			}
			pop = append(pop, "  "+row)
		}
	}
	if m.replyTo != nil && m.composing() {
		who := senderLabel(*m.replyTo)
		if m.replyTo.FromMe {
			who = "yourself"
		}
		what := m.replyTo.Text
		if what == "" && len(m.replyTo.Media) > 0 {
			what = mediaLabel(m.replyTo.Media[0].Kind)
		}
		line := sAccent.Render("↩ Replying to "+who+": ") + sMuted.Render(termSafe(strings.Join(strings.Fields(what), " ")))
		pop = append(pop, "  "+ansi.Truncate(line, max(10, rw-22), "…")+sFaint.Render("   esc cancels"))
	}
	if len(m.atts) > 0 && m.chat != nil { // what the [Image #n] tokens are
		var parts []string
		for _, a := range m.atts {
			if strings.Contains(m.compose.Value(), a.token) {
				parts = append(parts, sAccent.Render(a.token)+" "+sMuted.Render(termSafe(a.file.Name)+" "+humanSize(len(a.file.Data))))
			}
		}
		if len(parts) > 0 {
			pop = append(pop, "  "+strings.Join(parts, sMuted.Render("   ")))
		}
	}
	for i, p := range pop {
		if j := len(view) - len(pop) + i; j >= 0 {
			view[j] = p
		}
	}

	out = append(out, view...)
	out = append(out, " "+sFaint.Render(strings.Repeat("─", max(0, rw-2))))
	switch {
	case m.composing():
		for i, l := range strings.Split(m.compose.View(), "\n") {
			mark := "  "
			if i == 0 {
				mark = sAccent.Render("›") + " "
			}
			out = append(out, " "+mark+l)
		}
	case m.compose.Value() != "":
		out = append(out, " "+sMuted.Render("› "+ansi.Truncate(m.compose.Value(), rw-4, "…")))
	case m.focus == focusList:
		out = append(out, " "+sMuted.Render("› press enter to open and write"))
	default:
		out = append(out, " "+sMuted.Render("› Message "+termSafe(firstName(m.chat.Display))))
	}
	return out
}

func (m Model) reactPicker() string {
	mine := ""
	if i := m.selIndex(); i >= 0 && i < len(m.msgs) {
		mine = m.msgs[i].MyReaction
	}
	var parts []string
	for i, e := range reactions {
		item := sMuted.Render(fmt.Sprint(i+1)) + " " + termSafe(e)
		if e == mine {
			item = sAccentB.Render(fmt.Sprint(i+1)) + " " + termSafe(e)
		}
		parts = append(parts, item)
	}
	tail := ""
	if mine != "" {
		tail = sMuted.Render("    x remove yours")
	}
	return "   " + sTheirs.Render(" "+strings.Join(parts, "   ")+" ") + tail
}

type span struct{ start, end, media int } // media: first photo/card/file line

const (
	sepGap  = 20 * time.Minute
	groupGp = 5 * time.Minute
)

// chatLines renders the conversation as rows, like Slack: a name column
// (yours in amber), text across the pane, time and reactions at the right
// end. Returns, for each message, which lines it covers.
func (m Model) chatLines() ([]string, []span) {
	rw := m.rightWidth()
	const nameW = 12
	gutter := 1 + nameW + 2 // " name  "
	textW := max(10, rw-gutter-14)
	selected := -1
	if m.selecting {
		selected = m.selIndex()
	}
	dragLo, dragHi := -1, -1
	if m.dragFrom >= 0 {
		dragLo, dragHi = min(m.dragFrom, m.dragTo), max(m.dragFrom, m.dragTo)
	}

	var out []string
	if len(m.msgs) == 0 && m.chat != nil {
		note := "No messages yet."
		if m.chat.Encrypted {
			note = "No messages here yet. Older encrypted messages are in Messenger's backup: quit tuon and run `tuon history` to restore them with your PIN."
		}
		for _, l := range strings.Split(ansi.Wordwrap(note, max(10, rw-4), ""), "\n") {
			out = append(out, " "+sMuted.Render(l))
		}
		return out, nil
	}
	spans := make([]span, len(m.msgs))
	var prev *store.MessageRow
	albumStart := -1
	for i := range m.msgs {
		msg := &m.msgs[i]
		if msg.Text == "" && len(msg.Media) == 0 {
			continue
		}
		// later photos of an album are shown by its first message
		if albumStart >= 0 && i > 0 && sameAlbum(m.msgs[i-1], *msg) {
			spans[i] = spans[albumStart]
			continue
		}
		albumStart = -1
		if photoOnly(*msg) {
			albumStart = i
		}
		if prev == nil || !sameDay(msg.Time, prev.Time) {
			label := " " + dayLabel(msg.Time) + " "
			out = append(out, "", sFaint.Render(" ──")+sMuted.Render(label)+sFaint.Render(strings.Repeat("─", max(0, rw-4-ansi.StringWidth(label)))), "")
		}
		newRun := prev == nil || prev.SenderID != msg.SenderID || msg.Time.Sub(prev.Time) >= groupGp || !sameDay(msg.Time, prev.Time)
		if prev != nil && sameDay(msg.Time, prev.Time) {
			out = append(out, "") // a line between messages, even from the same person
		}
		prev = msg

		var body []string
		if q := msg.ReplyTo; q != nil { // what this replies to
			quote := "↩ " + termSafe(q.Name)
			if q.Name != "" {
				quote += ": "
			}
			quote += termSafe(strings.Join(strings.Fields(q.Text), " "))
			body = append(body, sMuted.Render(ansi.Truncate(quote, textW, "…")))
		}
		if msg.Text != "" {
			wrapped := ansi.Hardwrap(ansi.Wordwrap(termSafe(msg.Text), textW, ""), textW, true)
			for _, l := range strings.Split(wrapped, "\n") {
				body = append(body, sText.Render(l))
			}
		}
		mediaRow := len(body)
		var others []connector.Media
		for _, md := range msg.Media {
			if md.Kind != connector.Photo {
				others = append(others, md)
			}
		}
		if album := m.albumAt(i); len(album) == 1 {
			body = append(body, m.mediaLines(album[0], textW)...)
		} else if len(album) > 1 {
			body = append(body, m.albumLines(album, textW)...)
		}
		for _, md := range others {
			body = append(body, m.mediaLines(md, textW)...)
		}

		isSel := i == selected || (i >= dragLo && i <= dragHi)
		name := ""
		if newRun {
			if msg.FromMe {
				name = sAccentB.Render(fit("You", nameW))
			} else {
				name = nameStyle(msg.SenderID).Render(fit(senderLabel(*msg), nameW))
			}
		}
		chip := reactionChip(msg.Reactions, isSel)
		start := len(out)
		for j, l := range body {
			left := strings.Repeat(" ", nameW)
			if j == 0 && name != "" {
				left = name
			}
			edge := " "
			if isSel {
				edge = sAccent.Render("▎")
			}
			var rightCol []string
			switch {
			case j == 0 && msg.Pending && time.Since(msg.Time) > notSentAfter:
				rightCol = append(rightCol, sErr.Render("not sent"))
			case j == 0 && msg.Pending:
				rightCol = append(rightCol, sMuted.Render("sending…"))
			case j == 0 && (newRun || isSel):
				rightCol = append(rightCol, sMuted.Render(msg.Time.Format("3:04 PM")))
			}
			line := edge + left + "  " + l
			if len(rightCol) > 0 {
				line = spread(line, strings.Join(rightCol, "   ")+" ", rw)
			}
			if isSel {
				line = band(line, rw)
			}
			out = append(out, line)
		}
		if chip != "" { // reactions: a small line under the message
			edge := " "
			if isSel {
				edge = sAccent.Render("▎")
			}
			line := ansi.Truncate(edge+strings.Repeat(" ", nameW)+"  "+chip, rw-1, "…")
			if isSel {
				line = band(line, rw)
			}
			out = append(out, line)
		}
		spans[i] = span{start, len(out), start + mediaRow}
		if albumStart == i { // the album's later messages share these lines
			for j := i + 1; j < len(m.msgs) && sameAlbum(m.msgs[j-1], m.msgs[j]); j++ {
				spans[j] = spans[i]
			}
		}
	}
	return out, spans
}

// albumLines: several photos sent together, as one line (plus the first
// photo where the terminal can draw it).
func (m Model) albumLines(album []connector.Media, maxW int) []string {
	button := sTheirs.Render(" ") + sAccent.Background(lipgloss.Color(cTheirs)).Render(fmt.Sprintf("▸ View %d photos", len(album))) + sTheirs.Render(" ")
	if m.kitty != nil && m.imagesOn {
		if lines := m.mediaLines(album[0], maxW); len(lines) > 1 {
			return append(lines, button)
		}
	}
	return []string{button}
}

// band gives a line the soft selection background, edge to edge.
func band(s string, w int) string {
	return sSelBand.Render(strings.ReplaceAll(fit(s, w), "\x1b[0m", "\x1b[0m"+bgSeq(cSelBand)))
}

func dayLabel(t time.Time) string {
	now := time.Now()
	switch {
	case sameDay(t, now):
		return "Today"
	case sameDay(t, now.AddDate(0, 0, -1)):
		return "Yesterday"
	case t.Year() == now.Year():
		return t.Format("Mon, Jan 2")
	default:
		return t.Format("Mon, Jan 2 2006")
	}
}

// reactionChip is a soft pill under the message: " 😆 2  ❤ ". On the
// picked message it says who reacted: " 😆 Ana, Ben  ❤ You ".
func reactionChip(rs []store.ReactionCount, who bool) string {
	if len(rs) == 0 {
		return ""
	}
	var parts []string
	for _, r := range rs {
		p := termSafe(r.Emoji)
		switch {
		case who:
			p += " " + sText.Render(reactors(r))
		case r.Count > 1:
			p += " " + sMuted.Render(fmt.Sprint(r.Count))
		}
		parts = append(parts, p)
	}
	in := " " + strings.Join(parts, "  ") + " "
	return sPill.Render(strings.ReplaceAll(in, "\x1b[0m", "\x1b[0m"+bgSeq(cTheirs)))
}

// reactors: first names of who reacted; people we have no name for count
// as "N others".
func reactors(r store.ReactionCount) string {
	var names []string
	unknown := r.Count - len(r.Names)
	for _, n := range r.Names {
		if n == "" {
			unknown++
			continue
		}
		names = append(names, termSafe(firstName(n)))
	}
	switch {
	case unknown == 1:
		names = append(names, "1 other")
	case unknown > 1:
		names = append(names, fmt.Sprintf("%d others", unknown))
	}
	return strings.Join(names, ", ")
}

func senderLabel(msg store.MessageRow) string {
	switch {
	case msg.SenderName != "":
		return termSafe(firstName(msg.SenderName))
	case len(msg.SenderID) > 4:
		return "…" + msg.SenderID[len(msg.SenderID)-4:]
	}
	return msg.SenderID
}

func sameDay(a, b time.Time) bool { return a.YearDay() == b.YearDay() && a.Year() == b.Year() }

// stamp is the time label between message blocks, like Messenger's.
func stamp(t time.Time) string {
	now := time.Now()
	switch {
	case sameDay(t, now):
		return t.Format("3:04 PM")
	case now.Sub(t) < 6*24*time.Hour:
		return t.Format("Mon 3:04 PM")
	case t.Year() == now.Year():
		return t.Format("Jan 2, 3:04 PM")
	default:
		return t.Format("Jan 2 2006, 3:04 PM")
	}
}

func (m Model) mediaLines(md connector.Media, maxW int) []string {
	if md.Kind == connector.Link {
		return m.linkCard(md, maxW)
	}
	tag := sTheirs.Render(" " + mediaLabel(md.Kind) + " ")
	if md.Kind == connector.File {
		name := md.Name
		if name == "" {
			name = "File"
		}
		hint := " · o to save"
		if md.Ref == "" {
			hint = " · on your phone"
		}
		return []string{sTheirs.Render(" ") + sAccent.Background(lipgloss.Color(cTheirs)).Render(ansi.Truncate("▸ "+termSafe(name), max(8, min(maxW-16, 48)), "…")) +
			sTheirs.Render(" ") + sMuted.Render(hint)}
	}
	if md.Kind != connector.Photo || md.Ref == "" {
		return []string{tag}
	}
	if img := m.imgs[md.Ref]; img != nil && m.kitty != nil && m.imagesOn {
		cols, rows := kittyBox(img, max(10, min(maxW, 60)), 12)
		if lines, err := m.kitty.place(md.Ref, img, cols, rows); err == nil && len(lines) > 0 {
			return lines
		}
	}
	switch {
	case m.failed[md.Ref] != "":
		return []string{tag + sMuted.Render("  couldn't load, click to retry")}
	}
	button := sTheirs.Render(" ") + sAccent.Background(lipgloss.Color(cTheirs)).Render("▸ View photo") + sTheirs.Render(" ")
	if m.pending[md.Ref] {
		button += sMuted.Render("  loading…")
	}
	return []string{button}
}

// ---- image ----

func (m Model) imageView() []string {
	imgs := m.viewImages()
	idx := min(m.imgIdx, len(imgs)-1)
	left := " " + sAccentB.Render(brand) + "  " + sAccent.Render("<") + " " + sBold.Render(termSafe(m.chat.Display))
	areaH := m.h - 4
	var body []string
	res := ""
	if idx >= 0 {
		ref := imgs[idx].Ref
		switch img := m.imgs[ref]; {
		case img != nil && m.kitty != nil:
			cols, rows := kittyBox(img, m.w-2, areaH)
			if lines, err := m.kitty.place(ref, img, cols, rows); err == nil {
				body = lines
			} else {
				body = []string{sErr.Render("  couldn't show photo: " + err.Error())}
			}
		case m.failed[ref] != "":
			body = []string{sErr.Render("  couldn't load: " + m.failed[ref])}
		default:
			body = []string{sDim.Render("  loading…")}
		}
		if m.imgs[ref] != nil && m.kitty != nil {
			b := m.imgs[ref].Bounds()
			res = fmt.Sprintf("%d×%d original · ", b.Dx(), b.Dy())
		}
	}
	right := sDim.Render(fmt.Sprintf("%s%d / %d ", res, idx+1, len(imgs)))
	out := []string{spread(left, right, m.w), rule(m.w)}
	padTop := max(0, (areaH-len(body))/2)
	for i := 0; i < padTop; i++ {
		out = append(out, "")
	}
	for _, l := range body {
		pad := max(0, (m.w-ansi.StringWidth(l))/2)
		out = append(out, strings.Repeat(" ", pad)+l)
	}
	for len(out) < m.h-2 {
		out = append(out, "")
	}
	out = append(out, rule(m.w))
	hints := keyHints("esc", "back", "←→", "prev/next", "O", "open in viewer", "s", "save")
	if m.kitty == nil {
		hints += sMuted.Render("     Blocky? This terminal can't show real photos. Run tuon in Ghostty.")
	}
	if m.toast != "" {
		hints = " " + termSafe(m.toast)
	}
	out = append(out, hints)
	return out
}

// ---- help ----

// overlayFolders draws the folder manager in the middle of the screen.
func (m Model) overlayFolders(lines []string) []string {
	counts := m.st.SpaceCounts()
	const inner = 34
	box := []string{sAccent.Render("┌─ Folders " + strings.Repeat("─", inner-10) + "┐")}
	for i, sp := range m.spaces {
		n := counts[sp]
		label := store.Label(sp)
		if sp == store.Inbox {
			label += sMuted.Render("  (built in)")
		}
		row := fit(" "+label, inner-9) + sMuted.Render(fmt.Sprintf("%4d chats", n))
		if i == m.folderCur && m.folderMove {
			row = sAccent.Render("↕") + sSelMsg.Render(fit(row, inner-1))
		} else if i == m.folderCur {
			row = sAccent.Render("▎") + sSelMsg.Render(fit(row, inner-1))
		} else {
			row = " " + fit(row, inner-1)
		}
		box = append(box, "│"+fit(row, inner)+"│")
	}
	box = append(box, sAccent.Render("└"+strings.Repeat("─", inner)+"┘"))
	row := max(4, (m.h-len(box))/2)
	col := max(0, (m.w-inner-2)/2)
	for i, b := range box {
		r := row + i
		if r >= len(lines) {
			break
		}
		lines[r] = ansi.Cut(lines[r], 0, col) + b + ansi.Cut(lines[r], col+inner+2, m.w)
	}
	return lines
}

// overlaySound draws the notification sound picker.
func (m Model) overlaySound(lines []string) []string {
	const inner = 34
	box := []string{sAccent.Render("┌─ New message sound " + strings.Repeat("─", inner-20) + "┐")}
	state := sAccent.Render("on")
	if !m.soundOn {
		state = sMuted.Render("off")
	}
	bars := m.soundVol / 20
	vol := sAccent.Render(strings.Repeat("▮", bars)) + sFaint.Render(strings.Repeat("▮", 10-min(10, bars)))
	box = append(box,
		"│"+fit(" Sound "+state, inner)+"│",
		"│"+fit(" Volume "+vol+sMuted.Render(fmt.Sprintf(" %d%%", m.soundVol)), inner)+"│",
		"│"+strings.Repeat(" ", inner)+"│")
	rows := max(3, min(len(m.soundNames), m.h-14))
	off := max(0, min(m.soundCur-rows/2, len(m.soundNames)-rows))
	for i := off; i < off+rows && i < len(m.soundNames); i++ {
		row := " " + m.soundNames[i]
		if i == m.soundCur {
			row = sAccent.Render("▎") + sSelMsg.Render(fit(row, inner-1))
		} else {
			row = " " + fit(row, inner-1)
		}
		box = append(box, "│"+fit(row, inner)+"│")
	}
	box = append(box, sAccent.Render("└"+strings.Repeat("─", inner)+"┘"))
	row := max(2, (m.h-len(box))/2)
	col := max(0, (m.w-inner-2)/2)
	for i, b := range box {
		r := row + i
		if r >= len(lines) {
			break
		}
		lines[r] = ansi.Cut(lines[r], 0, col) + b + ansi.Cut(lines[r], col+inner+2, m.w)
	}
	return lines
}

func (m Model) helpView() []string {
	const descW = 15 // the longest description ("chats (empty)") plus a gap
	row := func(k1, d1, k2, d2 string) string {
		return " " + fit(sDim.Render(d1), descW) + fit(sAccent.Render(k1), 13) + fit(sDim.Render(d2), descW) + sAccent.Render(k2)
	}
	cmd := func(c, d string) string { return "  " + fit(sAccent.Render(c), 24) + sDim.Render(d) }
	head := func(t string) string { return " " + sDim.Render(t) }
	out := []string{
		" " + sAccentB.Render(brand) + sDim.Render(" · keys"),
		rule(m.w),
		head("chat list"),
		row("↑ ↓", "browse", "enter", "open"),
		row("← → 1-9", "folders", "u", "unread"),
		row("/", "search", "t", "move chat"),
		row("f", "edit folder", ":", "command"),
		row("?", "this help", "q", "quit"),
		"",
		head("in a chat"),
		row("enter", "send", "alt+enter", "new line"),
		row("@", "tag", "@~/", "attach"),
		row("ctrl+v", "paste", "ctrl+o", "photo"),
		row("↑", "pick msg", "esc esc", "clear"),
		row("tab", "chats", "esc", "chats (empty)"),
		"",
		head("picked message"),
		row("enter", "reply", "r 1-6", "react"),
		row("o", "open", "y", "copy"),
		row("enter", "send again", "d", "delete") + sDim.Render("   (not sent)"),
		"",
		head("mouse"),
		row("drag", "copy text", "2×click", "copy word"),
		row("3×click", "copy line", "click", "pick / open"),
		"",
		head("folders (f)"),
		row("a", "add", "r", "rename"),
		row("d", "delete", "m", "move"),
		"",
		head("commands"),
		cmd(":m  :mute", "mute or unmute the chat"),
		cmd(":s  :sound", "new-message sound and volume"),
		cmd(":l  :lock", "lock tuon now"),
		cmd(":i  :images on|off", "photos inside the chat"),
		cmd(":p  :previews on|off", "message text in the chat list"),
		cmd(":r  :seen on|off", "mark chats read on your phone (people see Seen)"),
		cmd(":q  :quit", "quit"),
		"",
		head("new chats land in Inbox. Move them once with t; they stay put."),
	}
	if len(out) > m.h-2 { // a short window: cut, never push the footer off
		out = out[:max(0, m.h-2)]
	}
	for len(out) < m.h-2 {
		out = append(out, "")
	}
	out = append(out, rule(m.w), keyHints("esc", "close"))
	return out
}

// ---- tag popup ----

func (m Model) overlayTag(lines []string, lw int) []string {
	name := m.tagFor
	cur := store.Space("")
	if c, err := m.st.Chat(m.tagFor); err == nil {
		name, cur = c.Display, c.Space
	}
	title := fmt.Sprintf(" Move %q to ", ansi.Truncate(termSafe(name), 24, "…"))
	inner := max(26, ansi.StringWidth(title)+2)
	top := "┌─" + title + strings.Repeat("─", max(0, inner-ansi.StringWidth(title)-1)) + "┐"
	item := func(k, label string, here bool) string {
		mark := "  "
		if here {
			mark = sAccent.Render("• ")
		}
		return "│" + fit("  "+sAccent.Render(k)+"  "+mark+label, inner) + "│"
	}
	box := []string{sAccent.Render(top)}
	for i, sp := range m.spaces {
		k := fmt.Sprint(i + 1)
		if sp == store.Inbox {
			k = "i"
		}
		box = append(box, item(k, store.Label(sp), sp == cur))
	}
	box = append(box, sAccent.Render("└"+strings.Repeat("─", inner)+"┘"))

	row, col := 4, 1
	if m.focus == focusChat && m.chat != nil {
		col = lw + 2 // top of the chat pane
		if lw == 0 {
			col = 1
		}
	} else {
		rows := m.listRows()
		pos := m.cursor
		if pos >= rows {
			pos = rows - 1
		}
		row = 4 + 1 + pos*rowLines + 2
		if m.searching() {
			row++
		}
		if row+len(box) > m.h-2 {
			row = max(0, row-len(box)-2)
		}
	}
	boxW := ansi.StringWidth(box[0])
	for i, b := range box {
		r := row + i
		if r >= len(lines) {
			break
		}
		lines[r] = ansi.Cut(lines[r], 0, col) + b + ansi.Cut(lines[r], col+boxW, m.w)
	}
	return lines
}

func mediaLabel(k connector.MediaKind) string {
	switch k {
	case connector.Photo:
		return "Photo"
	case connector.Video:
		return "Video, open on your phone"
	case connector.Voice:
		return "Voice message"
	case connector.Sticker:
		return "Sticker"
	case connector.Link:
		return "Link"
	case connector.Missing:
		return "New message, loading from your backup…"
	case connector.Unavailable:
		return "Couldn't load this message. See it on your phone (d removes this line)."
	case connector.KeyChanged:
		return "⚠ Security key changed: a new phone or reinstall, or someone listening in. If unsure, check with them another way."
	default:
		return "File"
	}
}

// linkCard: a shared post or link, as a small card. Click or o opens it.
func (m Model) linkCard(md connector.Media, maxW int) []string {
	w := max(12, min(maxW, 60))
	title := md.Title
	if title == "" {
		title = md.URL
	}
	lines := []string{sTheirs.Render(" ") + sAccent.Background(lipgloss.Color(cTheirs)).Render(fit("▸ "+termSafe(title), w)) + sTheirs.Render(" ")}
	// where it really goes: the domain, not the sender-chosen title
	where := md.Source
	if where == "" {
		where = "Link"
	}
	hint := ""
	if md.URL != "" {
		host, risk := linkRisk(md.URL)
		where, hint = host, " · click to open"
		if risk != "" {
			hint = " · ⚠ " + risk
		}
	}
	lines = append(lines, sTheirs.Render(" "+fit(termSafe(where)+hint, w)+" "))
	return lines
}
