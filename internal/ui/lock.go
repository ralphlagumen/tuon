package ui

import (
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// The lock screen: after a stretch with no input (or :lock), tuon hides
// everything and asks for Touch ID / the Mac password, or the passphrase.
// Messages keep arriving underneath; nothing is shown until unlocked.

type (
	idleTick    struct{}
	unlockedMsg struct{ err error }
)

// lockConfig is set by main once the vault is open.
type lockConfig struct {
	touch func() error      // Touch ID / Mac password (macOS); nil when using a passphrase
	pass  func(string) bool // checks the passphrase
	after time.Duration     // lock after this long without input; 0 = never
}

// SetLock turns on the lock screen. touch is used when set, else pass.
func (m *Model) SetLock(touch func() error, pass func(string) bool, after time.Duration) {
	m.lock = lockConfig{touch: touch, pass: pass, after: after}
	m.lastInput = time.Now()
	in := textinput.New()
	in.EchoMode = textinput.EchoPassword
	in.EchoCharacter = '•'
	in.Prompt = ""
	in.CharLimit = 256
	m.lockIn = in
}

func (m Model) lockOn() bool { return m.lock.touch != nil || m.lock.pass != nil }

func idleLater() tea.Cmd {
	return tea.Tick(20*time.Second, func(time.Time) tea.Msg { return idleTick{} })
}

// lockNow hides the screen and forgets any half-typed draft view state.
func (m *Model) lockNow() tea.Cmd {
	if !m.lockOn() || m.locked {
		return nil
	}
	m.locked, m.unlocking, m.lockErr = true, false, ""
	m.lockIn.SetValue("")
	if m.kitty != nil {
		m.kitty.clearAll() // photos are drawn outside the text grid
	}
	return tea.Batch(tea.ClearScreen, m.lockIn.Focus())
}

// onLockedKey: the only keys that work while locked.
func (m Model) onLockedKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	if k.String() == "ctrl+c" {
		return m, tea.Quit
	}
	if m.unlocking {
		return m, nil
	}
	if m.lock.touch != nil {
		if k.String() == "enter" || k.String() == " " {
			m.unlocking, m.lockErr = true, ""
			touch := m.lock.touch
			return m, func() tea.Msg { return unlockedMsg{touch()} }
		}
		return m, nil
	}
	if k.String() == "enter" {
		ok := m.lock.pass(m.lockIn.Value())
		m.lockIn.SetValue("")
		if ok {
			return m, func() tea.Msg { return unlockedMsg{} }
		}
		m.lockFails++
		wait := time.Duration(1<<min(m.lockFails-1, 5)) * time.Second
		m.unlocking, m.lockErr = true, "Wrong passphrase."
		return m, tea.Tick(wait, func(time.Time) tea.Msg { return unlockedMsg{err: errWrong} })
	}
	var cmd tea.Cmd
	m.lockIn, cmd = m.lockIn.Update(k)
	return m, cmd
}

type wrongPass struct{}

func (wrongPass) Error() string { return "wrong passphrase" }

var errWrong error = wrongPass{}

func (m Model) onUnlocked(msg unlockedMsg) (tea.Model, tea.Cmd) {
	m.unlocking = false
	if msg.err == errWrong { // the wait after a wrong passphrase is over
		return m, nil
	}
	if msg.err != nil {
		m.lockErr = "Not unlocked. Press enter to try again."
		return m, nil
	}
	m.locked, m.lockFails, m.lockErr = false, 0, ""
	m.lastInput = time.Now()
	return m, tea.Batch(tea.ClearScreen, m.reloadMessages())
}

func (m Model) lockView() []string {
	how := "Press enter to unlock with Touch ID or your Mac password."
	if m.lock.touch == nil {
		how = "Type your passphrase and press enter:  " + m.lockIn.View()
	}
	if m.unlocking && m.lock.touch != nil {
		how = "Waiting for Touch ID…"
	}
	lines := make([]string, max(1, m.h/2-2))
	lines = append(lines,
		center(sAccentB.Render("tuon is locked"), m.w),
		"",
		center(sMuted.Render(how), m.w),
	)
	if m.lockErr != "" {
		lines = append(lines, "", center(sErr.Render(m.lockErr), m.w))
	}
	return lines
}

func center(s string, w int) string {
	pad := (w - ansi.StringWidth(s)) / 2
	if pad < 0 {
		pad = 0
	}
	return strings.Repeat(" ", pad) + s
}
