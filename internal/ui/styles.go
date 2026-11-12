package ui

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/ralph/tuon/internal/connector"
)

// Palette: "lamplight at night". Tuon paints its own background so contrast
// doesn't depend on the terminal theme. One accent (amber) marks what needs
// your eye: unread, selection, your reactions, the active space.
const (
	cInk       = "#1F2329" // background
	cText      = "#E6E9EF" // messages, names
	cMuted     = "#A3ACBA" // snippets, times, hints (≈6.5:1 on ink)
	cFaint     = "#3A414C" // rules
	cTheirs    = "#30353D" // their bubble: neutral grey
	cMine      = "#28476B" // my bubble: clearly blue
	cTheirsSel = "#424954" // their bubble, selected
	cMineSel   = "#355D8C" // my bubble, selected
	cSelBand   = "#2A3039" // selected chat row
	cSelected  = "#46516A" // highlighted pick in pop-ups
	cAccent    = "#E5B25D" // amber
	cErr       = "#E07A7A"
)

// brand is the name in the header: the "o" is the logo, a ring with a dot.
const brand = "tuʘn"

var (
	sText    = lipgloss.NewStyle().Foreground(lipgloss.Color(cText))
	sBold    = lipgloss.NewStyle().Foreground(lipgloss.Color(cText)).Bold(true)
	sMuted   = lipgloss.NewStyle().Foreground(lipgloss.Color(cMuted))
	sFaint   = lipgloss.NewStyle().Foreground(lipgloss.Color(cFaint))
	sAccent  = lipgloss.NewStyle().Foreground(lipgloss.Color(cAccent))
	sAccentB = lipgloss.NewStyle().Foreground(lipgloss.Color(cAccent)).Bold(true)
	sErr     = lipgloss.NewStyle().Foreground(lipgloss.Color(cErr))

	sTheirs    = lipgloss.NewStyle().Foreground(lipgloss.Color(cText)).Background(lipgloss.Color(cTheirs))
	sMine      = lipgloss.NewStyle().Foreground(lipgloss.Color(cText)).Background(lipgloss.Color(cMine))
	sSelMsg    = lipgloss.NewStyle().Foreground(lipgloss.Color(cText)).Background(lipgloss.Color(cSelected))
	sTheirsSel = lipgloss.NewStyle().Foreground(lipgloss.Color(cText)).Background(lipgloss.Color(cTheirsSel))
	sMineSel   = lipgloss.NewStyle().Foreground(lipgloss.Color(cText)).Background(lipgloss.Color(cMineSel))
	sSelBand   = lipgloss.NewStyle().Background(lipgloss.Color(cSelBand))
	sChip      = lipgloss.NewStyle().Foreground(lipgloss.Color(cMuted))
	sPill      = lipgloss.NewStyle().Foreground(lipgloss.Color(cText)).Background(lipgloss.Color(cTheirs))
)

// sBadge: an unread count, dark on amber
var sBadge = lipgloss.NewStyle().Foreground(lipgloss.Color(cInk)).Background(lipgloss.Color(cAccent)).Bold(true)

const unreadDot = "●"

// groupMark follows a group chat's name in the list: one cell wide, a bit
// brighter than muted so it can be spotted, quieter than the name.
const groupMark = "⁂"

var sGroupMark = lipgloss.NewStyle().Foreground(lipgloss.Color("#C9D1DC"))

// lockMark follows an end-to-end encrypted chat's name (list and header).
// U+1F512 is a 2-cell emoji with no U+FE0F, so Terminal.app keeps its width.
const lockMark = "🔒"

// Each person in a group gets a steady name color, so you can tell who is
// talking at a glance. All read well on the ink background.
var nameColors = []string{"#7FB4E3", "#8FCB8F", "#D6A2E8", "#E3A27F", "#7FD1C7", "#E3C97F", "#A9A6F2", "#E38FA8"}

func nameStyle(id string) lipgloss.Style {
	h := uint32(2166136261)
	for i := 0; i < len(id); i++ {
		h = (h ^ uint32(id[i])) * 16777619
	}
	return lipgloss.NewStyle().Foreground(lipgloss.Color(nameColors[h%uint32(len(nameColors))])).Bold(true)
}

// Kept for older call sites: dim == muted.
var sDim = sMuted

func rule(w int) string { return sFaint.Render(strings.Repeat("─", max(0, w))) }

// paint gives a finished line the ink background, edge to edge. Inner
// styles end with a reset, so the background is put back after each one.
func paint(line string, w int) string {
	bg := bgSeq(cInk)
	line = strings.ReplaceAll(line, "\x1b[0m", "\x1b[0m"+bg)
	line = strings.ReplaceAll(line, "\x1b[m", "\x1b[m"+bg)
	if pad := w - ansi.StringWidth(line); pad > 0 {
		line += strings.Repeat(" ", pad)
	}
	return bg + line + "\x1b[0m"
}

func bgSeq(hex string) string {
	var r, g, b uint8
	fmt.Sscanf(hex, "#%02x%02x%02x", &r, &g, &b)
	if trueColor {
		return fmt.Sprintf("\x1b[48;2;%d;%d;%dm", r, g, b)
	}
	return fmt.Sprintf("\x1b[48;5;%dm", to256rgb(int(r), int(g), int(b)))
}

// fit truncates or pads s to exactly w cells.
func fit(s string, w int) string {
	if w <= 0 {
		return ""
	}
	s = ansi.Truncate(s, w, "…")
	if pad := w - ansi.StringWidth(s); pad > 0 {
		s += strings.Repeat(" ", pad)
	}
	return s
}

// spread puts left and right on one line of width w.
func spread(left, right string, w int) string {
	gap := w - ansi.StringWidth(left) - ansi.StringWidth(right)
	if gap < 1 {
		return fit(left, w-ansi.StringWidth(right)-1) + " " + right
	}
	return left + strings.Repeat(" ", gap) + right
}

// keyHints renders "enter open  / search": keys bright, words muted.
func keyHints(pairs ...string) string {
	var parts []string
	for i := 0; i+1 < len(pairs); i += 2 {
		parts = append(parts, sBold.Render(pairs[i])+" "+sMuted.Render(pairs[i+1]))
	}
	return " " + strings.Join(parts, "   ")
}

func ago(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	d := time.Since(t)
	now := time.Now()
	switch {
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour && t.Day() == now.Day():
		return fmt.Sprintf("%dh", int(d.Hours()))
	case t.YearDay() == now.AddDate(0, 0, -1).YearDay() && t.Year() == now.AddDate(0, 0, -1).Year():
		return "yday"
	case d < 6*24*time.Hour:
		return t.Format("Mon")
	case t.Year() == now.Year():
		return t.Format("Jan 2")
	default:
		return t.Format("1/2/06")
	}
}

func firstName(s string) string {
	if f := strings.Fields(s); len(f) > 0 {
		return f[0]
	}
	return s
}

// appleTerminal: macOS Terminal.app, which draws "❤️" 2 cells wide but moves
// the cursor only 1.
var appleTerminal = os.Getenv("TERM_PROGRAM") == "Apple_Terminal"

// emojiCursor keeps "❤️" (narrow symbol + U+FE0F) a color emoji in
// Terminal.app: clear its 2 cells (ECH, cursor stays), draw it, then step
// over the second cell (CUF). Escapes have no width, so Tuon counts 2
// cells and the terminal moves 2: nothing drifts.
//
// The codes go in last, on each finished screen line (View), never inside
// text that is still laid out: truncating or cutting text keeps every
// escape code past the cut, so a dropped emoji's cursor step would stay
// and push the line out of place (audit A13-0).
const emojiPre, emojiPost = "\x1b[2X", "\x1b[C"

func emojiCursor(line string) string {
	if !appleTerminal || !strings.ContainsRune(line, '\uFE0F') {
		return line
	}
	rs := []rune(line)
	var b strings.Builder
	b.Grow(len(line) + 16)
	for i := 0; i < len(rs); i++ {
		r := rs[i]
		if i+1 < len(rs) && rs[i+1] == '\uFE0F' && r > '~' && ansi.StringWidth(string(r)) == 1 {
			b.WriteString(emojiPre)
			b.WriteRune(r)
			b.WriteString("\uFE0F" + emojiPost)
			i++ // the marker
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// colorBare: symbols people mean as emoji even without U+FE0F (❤ is
// Messenger's heart reaction). Terminal.app shows them as color emoji.
var colorBare = map[rune]bool{'❤': true, '❣': true, '☺': true, '☹': true, '✌': true, '☝': true, '❄': true, '☀': true}

// termSafe makes emoji take the width Tuon expects, so lines never wrap
// and the screen never drifts.
//   - "❄️" (narrow symbol + U+FE0F "emoji style") is drawn 2 cells wide but
//     advances 1 in Terminal.app: there it stays "❄️" (counted 2 cells) and
//     View adds the cursor codes last (emojiCursor); elsewhere the marker
//     becomes a real space (text-style symbol).
//
// It is safe to run twice: its output has no escape codes.
//   - Joiners (U+200D), skin tones, keycap marks and U+FE0E are dropped, so
//     each emoji stands alone: a family shows as its people, 👍🏽 as 👍.
func termSafe(s string) string {
	s = connector.SafeText(s, true)
	var b strings.Builder
	b.Grow(len(s))
	var prev rune
	rs := []rune(s)
	for i, r := range rs {
		marked := i+1 < len(rs) && rs[i+1] == '\uFE0F'
		narrowEmoji := marked && r > '~' && ansi.StringWidth(string(r)) == 1 ||
			!marked && colorBare[r] // Messenger's heart reaction is a bare ❤
		switch {
		case narrowEmoji && appleTerminal:
			b.WriteRune(r)
			b.WriteRune('\uFE0F') // emojiCursor adds the cursor codes at the end
			prev = '\uFE0F'
			continue
		case r == '\uFE0F':
			if prev != '\uFE0F' && prev != 0 && ansi.StringWidth(string(prev)) == 1 {
				b.WriteRune(' ')
			}
			continue
		case r == '\u200D', r == '\uFE0E', r == '\u20E3', r >= 0x1F3FB && r <= 0x1F3FF:
			continue
		case r < ' ' && r != '\n':
			continue // stray control characters
		}
		b.WriteRune(r)
		prev = r
	}
	return b.String()
}
