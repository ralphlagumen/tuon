package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// mouse sends one mouse event and returns the model and what was copied.
func mouse(t *testing.T, m Model, a tea.MouseAction, b tea.MouseButton, x, y int) (Model, string) {
	t.Helper()
	next, cmd := m.Update(tea.MouseMsg{X: x, Y: y, Action: a, Button: b})
	m = next.(Model)
	if cmd == nil {
		return m, ""
	}
	if c, ok := cmd().(copiedMsg); ok {
		return m, c.text
	}
	return m, ""
}

// find: the screen cell where sub starts in the right pane.
func find(t *testing.T, m Model, sub string) (x, y int) {
	t.Helper()
	for y, l := range strings.Split(screen(m), "\n") {
		if i := strings.LastIndex(l, sub); i >= 0 && ansi.StringWidth(l[:i]) > m.leftWidth() {
			return ansi.StringWidth(l[:i]), y
		}
	}
	t.Fatalf("%q not on screen:\n%s", sub, screen(m))
	return
}

func TestSelectText(t *testing.T) {
	m, _, _ := newTestModel(t, 100, 26)
	m = seed(m)
	m = key(m, "3")
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = run(next.(Model), cmd)

	// drag over part of a message: just that text is copied and highlighted
	x, y := find(t, m, "pushed the fix")
	m, _ = mouse(t, m, tea.MouseActionPress, tea.MouseButtonLeft, x+7, y)
	m, _ = mouse(t, m, tea.MouseActionMotion, tea.MouseButtonLeft, x+13, y)
	if !strings.Contains(m.View(), bgSeq(cSelected)) {
		t.Fatal("dragging should highlight")
	}
	m, got := mouse(t, m, tea.MouseActionRelease, tea.MouseButtonNone, x+13, y)
	if got != "the fix" {
		t.Fatalf("copied %q, want %q", got, "the fix")
	}
	if !m.sel.shown || m.selecting {
		t.Fatalf("highlight should stay and the drag should not pick the message: %+v", m.sel)
	}
	// drag down across messages: only message text, no names or times
	x2, y2 := find(t, m, "thanks, ping me")
	m, _ = mouse(t, m, tea.MouseActionPress, tea.MouseButtonLeft, x, y)
	m, _ = mouse(t, m, tea.MouseActionMotion, tea.MouseButtonLeft, x2+5, y2)
	m, got = mouse(t, m, tea.MouseActionRelease, tea.MouseButtonNone, x2+5, y2)
	if !strings.HasPrefix(got, "pushed the fix") || !strings.HasSuffix(got, "thanks") ||
		strings.Contains(got, "Mark") || strings.Contains(got, "PM") {
		t.Fatalf("multi-line copy = %q", got)
	}
	// a key drops the highlight
	m = key(m, "esc")
	if m.sel.shown || strings.Contains(m.View(), bgSeq(cSelected)) {
		t.Fatal("a key should drop the highlight")
	}

	// double-click a word
	x, y = find(t, m, "deploy done")
	m, _ = mouse(t, m, tea.MouseActionPress, tea.MouseButtonLeft, x+2, y)
	m, _ = mouse(t, m, tea.MouseActionRelease, tea.MouseButtonNone, x+2, y)
	m, got = mouse(t, m, tea.MouseActionPress, tea.MouseButtonLeft, x+2, y)
	if got != "deploy" {
		t.Fatalf("double-click copied %q", got)
	}
	m, _ = mouse(t, m, tea.MouseActionRelease, tea.MouseButtonNone, x+2, y)

	// the draft: drag over what you're typing
	m = key(m, "esc")
	m = typeText(m, "hello brave world")
	x, y = find(t, m, "hello brave")
	m, _ = mouse(t, m, tea.MouseActionPress, tea.MouseButtonLeft, x+6, y)
	m, _ = mouse(t, m, tea.MouseActionMotion, tea.MouseButtonLeft, x+10, y)
	m, got = mouse(t, m, tea.MouseActionRelease, tea.MouseButtonNone, x+10, y)
	if got != "brave" {
		t.Fatalf("draft copy = %q", got)
	}
	if m.compose.Value() != "hello brave world" {
		t.Fatalf("selecting must not change the draft: %q", m.compose.Value())
	}
}
