package ui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/ralph/tuon/internal/connector"
)

// The loading screen: at startup tuon shows what it's syncing and takes
// no input (but q) until Messenger is connected, encrypted chats are up
// (or known unavailable) and the chat list is complete. Reading and
// replying to a half-synced list showed stale chats and unread marks.
// Only at startup: a later reconnect keeps tuon usable, with the status in
// the header.

// loadSkipAfter: after this long, enter lets you in anyway (a step that
// never finishes mustn't lock you out of your saved chats).
const loadSkipAfter = 45 * time.Second

var syncSteps = []string{connector.SyncMessenger, connector.SyncEncrypted, connector.SyncChats}

type loadTick struct{}

func loadLater() tea.Cmd {
	return tea.Tick(time.Second, func(time.Time) tea.Msg { return loadTick{} })
}

// WaitForSync turns the loading screen on (tuon-app calls it before the
// program starts; tests and other front ends don't).
func (m *Model) WaitForSync() {
	m.loading = true
	m.loadStart = time.Now()
	m.syncDone = map[string]bool{}
	m.syncErr = map[string]string{}
}

// onSync records a startup step; the screen goes once all are done.
func (m *Model) onSync(e connector.SyncEvent) {
	if e.Chats > m.syncChats {
		m.syncChats = e.Chats
	}
	if !m.loading || !e.Done {
		return
	}
	m.syncDone[e.Part] = true
	if e.Err != nil {
		m.syncErr[e.Part] = e.Err.Error()
	}
	for _, s := range syncSteps {
		if !m.syncDone[s] {
			return
		}
	}
	m.loading = false
}

func (m Model) canSkipLoading() bool {
	return m.loadFailed != "" || time.Since(m.loadStart) >= loadSkipAfter
}

func (m Model) onLoadingKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "enter":
		if m.canSkipLoading() {
			m.loading = false
			if m.loadFailed != "" {
				return m, m.flash("Offline: showing your saved chats")
			}
			return m, m.flash("Still syncing: chats may be out of date for a moment")
		}
	}
	return m, nil
}

// loadingView: the chat screen, blurred, with a floating card showing
// what's syncing. The blur keeps the layout's shape but no text: names
// and snippets of a half-synced list aren't readable or clickable.
func (m Model) loadingView() []string {
	lines := m.blurred()
	card := m.loadingCard()
	inner := ansi.StringWidth(card[0]) - 2
	row := max(0, (m.h-len(card))/2)
	col := max(0, (m.w-inner-2)/2)
	for i, c := range card {
		r := row + i
		if r >= len(lines) {
			break
		}
		lines[r] = ansi.Cut(lines[r], 0, col) + c + ansi.Cut(lines[r], col+inner+2, m.w)
	}
	return lines
}

// blurred: the main view with every letter, digit and emoji turned into
// a faint shade; spaces and box lines keep the shape.
func (m Model) blurred() []string {
	src := m.mainView()
	out := make([]string, len(src))
	for i, l := range src {
		var b strings.Builder
		for _, r := range ansi.Strip(l) {
			switch w := ansi.StringWidth(string(r)); {
			case r == ' ' || w == 0:
				b.WriteRune(' ')
			case r >= 0x2500 && r <= 0x257F: // box drawing
				b.WriteRune(r)
			default:
				b.WriteString(strings.Repeat("░", w))
			}
		}
		out[i] = sBlur.Render(b.String())
	}
	return out
}

var sBlur = lipgloss.NewStyle().Foreground(lipgloss.Color(cFaint))

// loadingCard: the floating box: brand, a progress bar, the steps, keys.
func (m Model) loadingCard() []string {
	inner := min(48, max(30, m.w-6))
	spin := []string{"·  ", "·· ", "···", " ··", "  ·", "   "}[int(time.Since(m.loadStart)/(300*time.Millisecond))%6]
	step := func(part, doing, done string) string {
		switch {
		case m.syncErr[part] != "":
			return sErr.Render("✗ ") + sMuted.Render(done+" unavailable: "+termSafe(m.syncErr[part]))
		case m.syncDone[part]:
			return sAccentB.Render("✓ ") + done
		}
		return sMuted.Render(spin + " " + doing)
	}
	chats := "Chat list"
	if m.syncChats > 0 {
		chats = fmt.Sprintf("Chat list: %d chats", m.syncChats)
	}
	fetching := "Fetching your chats"
	if m.syncChats > 0 {
		fetching = fmt.Sprintf("Fetching your chats (%d so far)", m.syncChats)
	}
	done := 0
	for _, s := range syncSteps {
		if m.syncDone[s] {
			done++
		}
	}
	barW := inner - 12
	fill := barW * done / len(syncSteps)
	if !m.syncDone[connector.SyncChats] && m.syncChats > 0 {
		fill = min(barW-1, fill+barW/len(syncSteps)/2) // chats coming in
	}
	bar := sAccent.Render(strings.Repeat("━", fill)) + sFaint.Render(strings.Repeat("━", barW-fill)) +
		sMuted.Render(fmt.Sprintf("  %d of %d", done, len(syncSteps)))

	body := []string{
		"",
		sAccentB.Render(brand) + sMuted.Render("  ·  syncing"),
		"",
		bar,
		"",
		step(connector.SyncMessenger, "Connecting to Messenger", "Connected to Messenger"),
		step(connector.SyncEncrypted, "Connecting encrypted chats", "Encrypted chats"),
		step(connector.SyncChats, fetching, chats),
		"",
	}
	switch {
	case m.loadFailed != "":
		body = append(body, sErr.Render("Couldn't connect: "+termSafe(m.loadFailed)), "",
			sMuted.Render("enter  read saved chats offline  ·  q  quit"))
	case m.canSkipLoading():
		body = append(body, sMuted.Render("Taking longer than usual."), "",
			sMuted.Render("enter  use tuon anyway  ·  q  quit"))
	default:
		body = append(body, sMuted.Render("q  quit"))
	}
	body = append(body, "")

	bg := bgSeq(cTheirs)
	onCard := func(s string) string {
		s = strings.ReplaceAll(s, "\x1b[0m", "\x1b[0m"+bg)
		s = strings.ReplaceAll(s, "\x1b[m", "\x1b[m"+bg)
		return bg + s + "\x1b[0m"
	}
	edge := sFaint.Render
	card := []string{edge("╭" + strings.Repeat("─", inner) + "╮")}
	for _, l := range body {
		card = append(card, edge("│")+onCard(fit("  "+l, inner))+edge("│"))
	}
	return append(card, edge("╰"+strings.Repeat("─", inner)+"╯"))
}
