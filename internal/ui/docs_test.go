package ui

import (
	"fmt"
	"image"
	"image/color"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"

	"github.com/ralph/tuon/internal/connector"
	"github.com/ralph/tuon/internal/store"
)

// TestDocsScreenshots renders tuon's real screens (with made-up chats) to
// SVG for the README.
//
//	TUON_DOCS=docs go test ./internal/ui -run TestDocsScreenshots
func TestDocsScreenshots(t *testing.T) {
	dir := os.Getenv("TUON_DOCS")
	if dir == "" {
		t.Skip("set TUON_DOCS=<dir>")
	}
	dir, _ = filepath.Abs(filepath.Join("..", "..", dir))
	_ = os.MkdirAll(dir, 0o755)
	lipgloss.SetColorProfile(termenv.TrueColor)
	trueColor = true

	m, st, _ := newTestModel(t, 150, 30)
	now := time.Now()
	at := func(d time.Duration) time.Time { return now.Add(-d) }
	evts := []any{
		connector.StatusEvent{Text: "connected"},
		connector.ContactEvent{ID: "101", Name: "Maya Santos"},
		connector.ContactEvent{ID: "102", Name: "Leo Cruz"},
		connector.ContactEvent{ID: "103", Name: "Ana Reyes"},
		connector.ContactEvent{ID: "104", Name: "Mom"},
		connector.ChatEvent{Chat: connector.Chat{ID: "1", Name: "Launch Team", Group: true, LastActivity: at(2 * time.Minute)}},
		connector.ChatEvent{Chat: connector.Chat{ID: "2", Name: "Design Review", Group: true, LastActivity: at(40 * time.Minute), Unread: true}},
		connector.ChatEvent{Chat: connector.Chat{ID: "104", LastActivity: at(3 * time.Hour), Encrypted: true}},
		connector.ChatEvent{Chat: connector.Chat{ID: "3", Name: "Weekend Hike", Group: true, LastActivity: at(26 * time.Hour)}},
		connector.ChatEvent{Chat: connector.Chat{ID: "102", LastActivity: at(50 * time.Hour), Encrypted: true}},
		connector.MessageEvent{Message: connector.Message{ID: "d1", ChatID: "2", SenderID: "103", Text: "New mockups are up", Time: at(40 * time.Minute)}},
		connector.MessageEvent{Message: connector.Message{ID: "h1", ChatID: "104", SenderID: "104", Text: "Dinner on Sunday?", Time: at(3 * time.Hour)}},
		connector.MessageEvent{Message: connector.Message{ID: "w1", ChatID: "3", SenderID: "102", Text: "Trail opens at 6", Time: at(26 * time.Hour)}},
		connector.MessageEvent{Message: connector.Message{ID: "l0", ChatID: "102", SenderID: "102", Text: "See you there", Time: at(50 * time.Hour)}},
		connector.MessageEvent{Message: connector.Message{ID: "m1", ChatID: "1", SenderID: "101", Text: "Build 1.4 passed all checks", Time: at(32 * time.Minute)}},
		connector.MessageEvent{Message: connector.Message{ID: "m2", ChatID: "1", SenderID: "102", Text: "Release notes are in the doc", Time: at(31 * time.Minute)}},
		connector.MessageEvent{Message: connector.Message{ID: "m3", ChatID: "1", SenderID: "1", FromMe: true, Text: "Great. Shipping at 3pm", Time: at(30 * time.Minute)}},
		connector.MessageEvent{Message: connector.Message{ID: "m4", ChatID: "1", SenderID: "101", Text: "Screenshot of the new onboarding", Time: at(5 * time.Minute),
			Media: []connector.Media{{Kind: connector.Photo, Ref: "p1"}}}},
		connector.MessageEvent{Message: connector.Message{ID: "m5", ChatID: "1", SenderID: "103", Text: "Looks clean", Time: at(3 * time.Minute), ReplyToID: "m4"}},
		connector.MessageEvent{Message: connector.Message{ID: "m6", ChatID: "1", SenderID: "1", FromMe: true, Text: "Thanks all, see you at the demo", Time: at(2 * time.Minute)}},
		connector.ReactionEvent{ChatID: "1", MessageID: "m1", ActorID: "102", Emoji: "👍"},
		connector.ReactionEvent{ChatID: "1", MessageID: "m1", ActorID: "103", Emoji: "👍"},
		connector.ReactionEvent{ChatID: "1", MessageID: "m3", ActorID: "101", Emoji: "❤"},
	}
	next, _ := m.Update(EventsMsg(evts))
	m = next.(Model)
	_ = st.SetSpace("1", store.Work)
	_ = st.SetSpace("2", store.Work)
	_ = st.SetSpace("104", store.Personal)
	_ = st.SetSpace("3", store.Personal)
	m.reloadChats()

	// 1. a chat open, typing a reply
	m = key(m, "1")
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = run(next.(Model), cmd)
	m = key(m, "up")
	m = key(m, "up")
	m = key(m, "enter")
	m = typeText(m, "Shipping it now")
	writeSVG(t, filepath.Join(dir, "screen-chat.svg"), m.View())

	// 2. browsing folders, with the Unread tab
	m = key(m, "esc")
	m.compose.SetValue("")
	m.replyTo = nil
	m = key(m, "esc")
	m = key(m, "u")
	writeSVG(t, filepath.Join(dir, "screen-unread.svg"), m.View())
	_ = image.Rect
	_ = color.RGBA{}
}

var sgrRe = regexp.MustCompile(`^\x1b\[([0-9;]*)m`)
var otherEsc = regexp.MustCompile(`^\x1b(\[[0-9;?]*[A-Za-z]|\][^\x07]*\x07)`)

// writeSVG draws ANSI screen text as an SVG terminal window.
func writeSVG(t *testing.T, path, screen string) {
	t.Helper()
	const cw, ch, pad, bar = 8.6, 19.0, 18.0, 30.0
	lines := strings.Split(screen, "\n")
	cols := 0
	for _, l := range lines {
		cols = max(cols, ansi.StringWidth(l))
	}
	w := float64(cols)*cw + 2*pad
	h := float64(len(lines))*ch + 2*pad + bar
	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" width="%.0f" height="%.0f" viewBox="0 0 %.0f %.0f">`, w, h, w, h)
	fmt.Fprintf(&b, `<rect width="%.0f" height="%.0f" rx="12" fill="#1f2329"/>`, w, h)
	fmt.Fprintf(&b, `<rect width="%.0f" height="%.0f" rx="12" fill="#2a2f37"/><rect y="%.0f" width="%.0f" height="12" fill="#2a2f37"/>`, w, bar, bar-12, w)
	for i, c := range []string{"#ff5f57", "#febc2e", "#28c840"} {
		fmt.Fprintf(&b, `<circle cx="%.0f" cy="15" r="6" fill="%s"/>`, 22+float64(i)*20, c)
	}
	fmt.Fprintf(&b, `<g font-family="SF Mono, Menlo, Consolas, monospace" font-size="14.5" xml:space="preserve">`)
	for row, l := range lines {
		y := bar + pad + float64(row)*ch
		fg, bg, bold := "#e6e9ef", "", false
		col := 0
		var run strings.Builder
		runStart, runFg, runBold := 0, fg, false
		flush := func() {
			if run.Len() == 0 {
				return
			}
			weight := ""
			if runBold {
				weight = ` font-weight="700"`
			}
			// place every character on its own grid column, so a font with a
			// slightly different advance can't push later columns out of line
			var xs []string
			c := runStart
			for _, r := range run.String() {
				xs = append(xs, fmt.Sprintf("%.1f", pad+float64(c)*cw))
				c += max(1, ansi.StringWidth(string(r)))
			}
			fmt.Fprintf(&b, `<text x="%s" y="%.1f" fill="%s"%s>%s</text>`,
				strings.Join(xs, " "), y+14, runFg, weight, xmlEscape(run.String()))
			run.Reset()
		}
		for len(l) > 0 {
			if mm := sgrRe.FindStringSubmatch(l); mm != nil {
				l = l[len(mm[0]):]
				parts := strings.Split(mm[1], ";")
				for i := 0; i < len(parts); i++ {
					switch parts[i] {
					case "", "0":
						fg, bg, bold = "#e6e9ef", "", false
					case "1":
						bold = true
					case "22":
						bold = false
					case "39":
						fg = "#e6e9ef"
					case "49":
						bg = ""
					case "38", "48":
						if i+4 < len(parts) && parts[i+1] == "2" {
							r, _ := strconv.Atoi(parts[i+2])
							g, _ := strconv.Atoi(parts[i+3])
							bb, _ := strconv.Atoi(parts[i+4])
							hex := fmt.Sprintf("#%02x%02x%02x", r, g, bb)
							if parts[i] == "38" {
								fg = hex
							} else {
								bg = hex
							}
							i += 4
						}
					}
				}
				continue
			}
			if mm := otherEsc.FindString(l); mm != "" {
				l = l[len(mm):]
				continue
			}
			r := []rune(l)[0]
			l = l[len(string(r)):]
			cw2 := ansi.StringWidth(string(r))
			if bg != "" && bg != "#1f2329" {
				fmt.Fprintf(&b, `<rect x="%.1f" y="%.1f" width="%.1f" height="%.1f" fill="%s"/>`, pad+float64(col)*cw, y, float64(cw2)*cw+0.5, ch, bg)
			}
			if fg != runFg || bold != runBold || r == ' ' && run.Len() == 0 || cw2 != 1 {
				flush()
				runStart, runFg, runBold = col, fg, bold
			}
			if r != ' ' || run.Len() > 0 {
				run.WriteRune(r)
			} else {
				runStart = col + 1
			}
			if cw2 != 1 {
				flush()
				runStart = col + cw2
			}
			col += cw2
		}
		flush()
	}
	b.WriteString(`</g></svg>`)
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}

func xmlEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}
