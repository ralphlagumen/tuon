package ui

import (
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// Text selection, like Claude Code: drag over messages or the draft to
// highlight text; letting go copies it. Double-click takes a word,
// triple-click a line. In the messages only the text column is taken (no
// names, no times), so a copy is just what was said.

// region is a box of screen cells: columns [x0, x1), rows [y0, y1).
type region struct{ x0, x1, y0, y1 int }

func (r region) has(x, y int) bool { return x >= r.x0 && x < r.x1 && y >= r.y0 && y < r.y1 }

func (r region) clamp(x, y int) (int, int) {
	return max(r.x0, min(x, r.x1-1)), max(r.y0, min(y, r.y1-1))
}

type textSel struct {
	reg      region
	ax, ay   int  // where the drag started
	cx, cy   int  // where it is now
	dragging bool // the button is down
	moved    bool // it went past the first cell
	shown    bool // highlight on screen
}

// clicks: the last press, to spot double and triple clicks.
type clicks struct {
	x, y, n int
	at      time.Time
}

const multiClick = 400 * time.Millisecond

// selRegions: the message text column and the draft box, on screen.
func (m Model) selRegions() (chat, draft region, ok bool) {
	if m.mode != modeMain || m.chat == nil || m.locked {
		return
	}
	lw, rw := m.leftWidth(), m.rightWidth()
	if rw == 0 {
		return
	}
	paneX := 0
	if lw > 0 {
		paneX = lw + 1 // after the │
	}
	const gutter = 1 + 12 + 2 // edge, name, gap (chatLines)
	textW := max(10, rw-gutter-14)
	ch := m.chatHeight()
	top := 4 // header, rule, title, rule
	chat = region{paneX + gutter, min(paneX+gutter+textW, m.w), top, top + ch}
	if m.composing() {
		y0 := top + ch + 1 // below the rule
		draft = region{paneX + 3, paneX + rw, y0, y0 + m.compose.Height()}
	}
	return chat, draft, true
}

// span of the selection on row y, in columns [from, to).
func (s textSel) span(y int) (from, to int, ok bool) {
	y1, x1, y2, x2 := s.ay, s.ax, s.cy, s.cx
	if y2 < y1 || y2 == y1 && x2 < x1 {
		y1, x1, y2, x2 = y2, x2, y1, x1
	}
	if y < y1 || y > y2 {
		return 0, 0, false
	}
	from, to = s.reg.x0, s.reg.x1
	if y == y1 {
		from = x1
	}
	if y == y2 {
		to = x2 + 1
	}
	return from, to, from < to
}

// highlight paints the selection over finished screen lines.
func (s textSel) highlight(lines []string) {
	if !s.shown {
		return
	}
	bg := bgSeq(cSelected)
	for y := s.reg.y0; y < s.reg.y1 && y < len(lines); y++ {
		from, to, ok := s.span(y)
		if !ok {
			continue
		}
		l := lines[y]
		if w := ansi.StringWidth(l); w < to {
			l += strings.Repeat(" ", to-w)
		}
		mid := ansi.Cut(l, from, to)
		mid = strings.NewReplacer("\x1b[0m", "\x1b[0m"+bg, "\x1b[m", "\x1b[m"+bg).Replace(mid)
		lines[y] = ansi.Cut(l, 0, from) + bg + mid + "\x1b[0m" + ansi.Cut(l, to, ansi.StringWidth(l))
	}
}

// text: what the selection covers, as plain text, one line per row.
func (s textSel) text(lines []string) string {
	var out []string
	for y := s.reg.y0; y < s.reg.y1 && y < len(lines); y++ {
		from, to, ok := s.span(y)
		if !ok {
			continue
		}
		out = append(out, strings.TrimRight(ansi.Strip(ansi.Cut(lines[y], from, to)), " "))
	}
	for len(out) > 0 && out[0] == "" {
		out = out[1:]
	}
	for len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	return strings.Join(out, "\n")
}

// wordAt: the columns of the word under x on a screen line, within reg.
func wordAt(line string, x int, reg region) (from, to int) {
	plain := ansi.Strip(line)
	var cells []rune // one entry per screen cell; wide runes fill two
	for _, r := range plain {
		cells = append(cells, r)
		for w := ansi.StringWidth(string(r)); w > 1; w-- {
			cells = append(cells, r)
		}
	}
	blank := func(i int) bool { return i < 0 || i >= len(cells) || cells[i] == ' ' }
	if blank(x) {
		return x, x + 1
	}
	from, to = x, x+1
	for from > reg.x0 && !blank(from-1) {
		from--
	}
	for to < reg.x1 && !blank(to) {
		to++
	}
	return from, to
}

// onSelectMouse handles text selection; done means the event is used up.
func (m Model) onSelectMouse(e tea.MouseMsg) (Model, tea.Cmd, bool) {
	chat, draft, ok := m.selRegions()
	if !ok {
		return m, nil, false
	}
	switch {
	case e.Action == tea.MouseActionPress && e.Button == tea.MouseButtonLeft:
		reg := chat
		if !chat.has(e.X, e.Y) {
			if !draft.has(e.X, e.Y) {
				m.sel = textSel{}
				return m, nil, false
			}
			reg = draft
		}
		n := 1
		if c := m.lastClick; c.x == e.X && c.y == e.Y && time.Since(c.at) < multiClick {
			n = c.n%3 + 1
		}
		m.lastClick = clicks{e.X, e.Y, n, time.Now()}
		m.sel = textSel{reg: reg, ax: e.X, ay: e.Y, cx: e.X, cy: e.Y, dragging: true}
		if n == 1 {
			return m, nil, false // maybe a plain click: let it through too
		}
		lines := m.mainView()
		if n == 2 {
			from, to := wordAt(lines[e.Y], e.X, reg)
			m.sel.ax, m.sel.cx = from, to-1
		} else {
			m.sel.ax, m.sel.cx = reg.x0, reg.x1-1
		}
		m.sel.dragging, m.sel.shown = false, true
		m.dragFrom, m.dragTo = -1, -1
		return m, copyText(m.sel.text(lines), 1), true
	case e.Action == tea.MouseActionMotion && m.sel.dragging:
		m.sel.cx, m.sel.cy = m.sel.reg.clamp(e.X, e.Y)
		if m.sel.cx != m.sel.ax || m.sel.cy != m.sel.ay {
			m.sel.moved, m.sel.shown = true, true
		}
		return m, nil, true
	case e.Action == tea.MouseActionRelease && m.sel.dragging:
		m.sel.dragging = false
		if !m.sel.moved {
			m.sel.shown = false
			return m, nil, false // a click: opens, selects, focuses as before
		}
		m.dragFrom, m.dragTo = -1, -1
		if t := m.sel.text(m.mainView()); t != "" {
			return m, copyText(t, 1), true
		}
		return m, nil, true
	case e.Action == tea.MouseActionPress: // wheel and other buttons
		m.sel = textSel{}
	}
	return m, nil, false
}
