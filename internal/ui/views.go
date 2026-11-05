package ui

import (
	"fmt"
	"strings"
	"time"

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
	return max(28, min(46, m.w*3/10))
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
func (m Model) chatHeight() int { return max(1, m.bodyHeight()-2) }
func (m Model) searching() bool { return m.inputKind == inputSearch || m.query != "" }

func (m Model) View() string {
	if m.w == 0 {
		return ""
	}
	var lines []string
	switch m.mode {
	case modeHelp:
		lines = m.helpView()
	case modeImage:
		lines = m.imageView()
	default:
		lines = m.mainView()
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
	return strings.Join(lines, "\n")
}

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

	status := sMuted.Render(m.status + " ")
	if m.statusErr {
		status = sErr.Render(m.status + " ")
	}
	out := []string{
		spread(" "+sAccentB.Render("tuon")+"  "+sMuted.Render(m.platform), status, m.w),
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
	return out
}

func (m Model) footer() string {
	switch {
	case m.inputKind == inputCommand:
		return " " + sAccent.Render(":") + m.input.View()
	case m.toast != "":
		return " " + sText.Render(m.toast)
	case m.inputKind == inputFolder:
		return " " + sAccent.Render("Folder name: ") + m.input.View()
	case m.folderAskRm:
		return " " + sText.Render("Delete this folder? Its chats go to Inbox. ") + keyHints("y", "delete", "any key", "cancel")
	case m.folders:
		return keyHints("↑↓", "pick", "a", "add", "r", "rename", "d", "delete", "shift+↑↓", "reorder", "esc", "close")
	case m.inputKind == inputSearch:
		return keyHints("enter", "done", "esc", "clear search")
	case m.composing() && len(m.sugg) > 0:
		return keyHints("↑↓", "choose", "tab", "tag person", "esc", "close")
	case m.composing():
		return keyHints("enter", "send", "@", "tag", "@~/", "attach file", "ctrl+v", "paste image", "↑", "pick a message", "esc", "chats")
	case m.reacting:
		return keyHints("1-6", "react", "x", "remove mine", "esc", "cancel")
	case m.focus == focusChat && m.selecting:
		return keyHints("↑↓", "pick", "r", "react", "y", "copy", "o", "view photo", "esc", "back to typing", "tab", "chat list")
	default:
		return keyHints("↑↓", "browse", "←→", "folders", "enter", "open", "/", "search all", "t", "move chat", "f", "edit folders", "?", "help")
	}
}

// ---- left: chat list ----

func (m Model) tabs() string {
	build := func(sep string) string {
		var parts []string
		for i, sp := range m.spaces {
			label := fmt.Sprintf("%d %s", i+1, store.Label(sp))
			if n := m.counts[sp]; n > 0 {
				label += fmt.Sprintf(" %d", n)
			}
			if sp == m.space {
				parts = append(parts, sAccentB.Render(label))
			} else {
				parts = append(parts, sMuted.Render(label))
			}
		}
		return " " + strings.Join(parts, sep)
	}
	if full := build("   "); ansi.StringWidth(full) <= m.leftWidth() {
		return full
	}
	return build(" ")
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
	if len(m.chats) == 0 {
		msg := " Nothing matches."
		if m.space == store.Inbox && m.query == "" {
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
	for i := off; i < off+rows && i < len(m.chats); i++ {
		out = append(out, m.chatRow(m.chats[i], i == m.cursor, lw)...)
	}
	for len(out) < bh {
		out = append(out, "")
	}
	return out[:bh]
}

// chatRow: name + time, snippet, then a gap line.
func (m Model) chatRow(c store.ChatRow, selected bool, lw int) []string {
	t := ago(c.LastActivity)
	nameW := max(4, lw-5-ansi.StringWidth(t))
	name := termSafe(c.Display)
	if c.Muted {
		name += " (muted)"
	}

	edge, dot := " ", " "
	if c.Unread {
		dot = sAccent.Render(unreadDot)
	}
	nameS := sText.Render(fit(name, nameW))
	if c.Unread {
		nameS = sBold.Render(fit(name, nameW))
	}
	snip := sMuted.Render(fit(termSafe(c.Snippet), lw-4))
	if c.Muted {
		nameS = sMuted.Render(fit(name, nameW))
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
	if m.chat == nil {
		for i := 0; i < bh; i++ {
			out = append(out, "")
		}
		hint := "Pick a chat on the left and press enter."
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
	if len(m.atts) > 0 && m.chat != nil { // files waiting to be sent
		var chips []string
		for _, a := range m.atts {
			kind := "File"
			if a.IsImage() {
				kind = "Photo"
			}
			chips = append(chips, sMine.Render(" "+kind+": "+termSafe(a.Name)+"  "+humanSize(len(a.Data))+" "))
		}
		pop = append(pop, "  "+strings.Join(chips, " ")+sMuted.Render("   enter sends · backspace removes"))
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
		out = append(out, " "+sAccent.Render("›")+" "+m.compose.View())
	case m.compose.Value() != "":
		out = append(out, " "+sMuted.Render("› "+ansi.Truncate(m.compose.Value(), rw-4, "…")))
	case m.focus == focusList:
		out = append(out, " "+sMuted.Render("› press enter to open and write"))
	default:
		out = append(out, " "+sMuted.Render("› Message "+firstName(m.chat.Display)))
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
		item := sMuted.Render(fmt.Sprint(i+1)) + " " + e
		if e == mine {
			item = sAccentB.Render(fmt.Sprint(i+1)) + " " + e
		}
		parts = append(parts, item)
	}
	tail := ""
	if mine != "" {
		tail = sMuted.Render("    x remove yours")
	}
	return "   " + sTheirs.Render(" "+strings.Join(parts, "   ")+" ") + tail
}

type span struct{ start, end int }

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
	spans := make([]span, len(m.msgs))
	var prev *store.MessageRow
	for i := range m.msgs {
		msg := &m.msgs[i]
		if msg.Text == "" && len(msg.Media) == 0 {
			continue
		}
		if prev == nil || !sameDay(msg.Time, prev.Time) {
			label := " " + dayLabel(msg.Time) + " "
			out = append(out, "", sFaint.Render(" ──")+sMuted.Render(label)+sFaint.Render(strings.Repeat("─", max(0, rw-4-ansi.StringWidth(label)))), "")
		}
		newRun := prev == nil || prev.SenderID != msg.SenderID || msg.Time.Sub(prev.Time) >= groupGp || !sameDay(msg.Time, prev.Time)
		if newRun && prev != nil && sameDay(msg.Time, prev.Time) {
			out = append(out, "")
		}
		prev = msg

		var body []string
		if msg.Text != "" {
			wrapped := ansi.Hardwrap(ansi.Wordwrap(termSafe(msg.Text), textW, ""), textW, true)
			for _, l := range strings.Split(wrapped, "\n") {
				body = append(body, sText.Render(l))
			}
		}
		for _, md := range msg.Media {
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
		chip := reactionChip(msg.Reactions)
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
			if j == len(body)-1 && chip != "" {
				rightCol = append(rightCol, chip)
			}
			switch {
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
		spans[i] = span{start, len(out)}
	}
	return out, spans
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

// reactionChip is "😆 2 ❤" for one message; yours is marked in amber.
func reactionChip(rs []store.ReactionCount) string {
	var parts []string
	for _, r := range rs {
		p := r.Emoji
		if r.Count > 1 {
			p += " " + fmt.Sprint(r.Count)
		}
		if r.Mine {
			p += sAccentB.Render("•")
		}
		parts = append(parts, p)
	}
	return strings.Join(parts, "  ")
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
	tag := sTheirs.Render(" " + mediaLabel(md.Kind) + " ")
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
		return []string{tag + sMuted.Render("  couldn't load, press o to retry")}
	case m.pending[md.Ref]:
		return []string{tag + sMuted.Render("  loading…")}
	}
	return []string{tag + sMuted.Render("  press o to view")}
}

// ---- image ----

func (m Model) imageView() []string {
	imgs := m.chatImages()
	idx := min(m.imgIdx, len(imgs)-1)
	left := " " + sAccentB.Render("tuon") + "  " + sAccent.Render("<") + " " + sBold.Render(m.chat.Display)
	areaH := m.h - 4
	var body []string
	res := ""
	if idx >= 0 {
		ref := imgs[idx].Ref
		key := fmt.Sprintf("%s|%d|%d", ref, m.w-2, areaH)
		switch img := m.imgs[ref]; {
		case img != nil && m.kitty != nil:
			cols, rows := kittyBox(img, m.w-2, areaH)
			if lines, err := m.kitty.place(ref, img, cols, rows); err == nil {
				body = lines
			} else {
				body = []string{sErr.Render("  couldn't show photo: " + err.Error())}
			}
		case thumbCache[key] != nil:
			body = thumbCache[key]
		case img != nil:
			body = renderImage(img, m.w-2, areaH)
			thumbCache[key] = body
		case m.failed[ref] != "":
			body = []string{sErr.Render("  couldn't load: " + m.failed[ref])}
		default:
			body = []string{sDim.Render("  loading…")}
		}
		if m.imgs[ref] != nil && m.kitty != nil {
			b := m.imgs[ref].Bounds()
			res = fmt.Sprintf("%d×%d original · ", b.Dx(), b.Dy())
		} else if m.imgs[ref] != nil && len(body) > 0 {
			// 1x2 square pixels per cell
			res = fmt.Sprintf("%d×%d px · ", ansi.StringWidth(body[0]), len(body)*2)
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
		hints = " " + m.toast
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
		if i == m.folderCur {
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

func (m Model) helpView() []string {
	row := func(k1, d1, k2, d2 string) string {
		return " " + fit(sDim.Render(d1), 11) + fit(sAccent.Render(k1), 13) + fit(sDim.Render(d2), 11) + sAccent.Render(k2)
	}
	out := []string{
		" " + sAccentB.Render("tuon") + sDim.Render(" · keys"),
		rule(m.w),
		row("j/k ↑/↓", "move", "1 2 3", "spaces"),
		row("enter", "open chat", "tab / esc", "switch pane"),
		row("/", "search", "t", "move chat"),
		row("f", "folders", "1-9 / ←→", "switch folder"),
		row("i", "type", "enter", "send"),
		row("@", "mention", "tab", "pick name"),
		row("r", "react", "1-6 / x", "pick / remove"),
		row("o", "photo", "O / s", "view / save"),
		row("g / G", "top / end", "ctrl+u/d", "page"),
		"",
		" " + sDim.Render("commands"),
		"  " + sAccent.Render(":work :personal :inbox") + sDim.Render("   switch space"),
		"  " + sAccent.Render(":mute") + sDim.Render("                    mute / unmute chat"),
		"  " + sAccent.Render(":images on|off") + sDim.Render("           photo previews"),
		"  " + sAccent.Render(":quit") + sDim.Render("                    quit"),
		"",
		" " + sDim.Render("new chats land in inbox. tag them once with t, they stay put."),
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
	default:
		return "File"
	}
}
