package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
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
)

const unreadDot = "●"

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

// termSafe makes emoji take the width Tuon expects, so lines never wrap
// and the screen never drifts.
//   - "❄️" (narrow symbol + U+FE0F "emoji style") is drawn 2 cells wide but
//     advances 1 in Terminal.app: the marker becomes a real space.
//   - Joiners (U+200D), skin tones, keycap marks and U+FE0E are dropped, so
//     each emoji stands alone: a family shows as its people, 👍🏽 as 👍.
func termSafe(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	var prev rune
	for _, r := range s {
		switch {
		case r == '\uFE0F':
			if prev != 0 && ansi.StringWidth(string(prev)) == 1 {
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
