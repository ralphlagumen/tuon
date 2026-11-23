package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/ralph/tuon/internal/connector"
)

// The keys panel (:keys, MESSENGER-MITM M4.1): a 1:1 encrypted chat's
// device keys, in the same hex Messenger's app shows under "Verify
// end-to-end encryption", so you can compare them on your phone and mark
// the person verified. A verified person's keys are checked again when you
// open their chat (once a run) and when Messenger says their key changed;
// a difference is a lasting header warning until you look.

type keysPanel struct {
	chatID, name string
	set          *connector.KeySet
	err          string
	top          int
}

type keysMsg struct {
	chatID string
	set    connector.KeySet
	err    error
	check  bool // a background check, not the panel
}

func (m Model) fetchKeys(chatID string, check bool) tea.Cmd {
	kl, ok := m.conn.(connector.KeyLister)
	if !ok {
		return nil
	}
	ctx := m.ctx
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		set, err := kl.ContactKeys(ctx, chatID)
		return keysMsg{chatID: chatID, set: set, err: err, check: check}
	}
}

// openKeys is :keys on the open or highlighted chat.
func (m Model) openKeys() (tea.Model, tea.Cmd) {
	id := m.targetChat()
	if id == "" {
		return m, nil
	}
	c, err := m.st.Chat(id)
	switch {
	case err != nil:
		return m, nil
	case c.Group:
		return m, m.flash("Keys are per person: open your 1:1 chat with them")
	case !c.Encrypted:
		return m, m.flash("This chat isn't end-to-end encrypted: no keys to verify")
	}
	if _, ok := m.conn.(connector.KeyLister); !ok {
		return m, m.flash("Keys aren't available here")
	}
	m.keys = &keysPanel{chatID: c.ID, name: termSafe(c.Display)}
	return m, m.fetchKeys(c.ID, false)
}

// checkVerified looks again at a verified person's keys, once a run.
func (m *Model) checkVerified(chatID string) tea.Cmd {
	if _, _, ok := m.st.Verified(chatID); !ok || m.keysChecked[chatID] {
		return nil
	}
	if m.keysChecked == nil {
		m.keysChecked = map[string]bool{}
	}
	m.keysChecked[chatID] = true
	return m.fetchKeys(chatID, true)
}

func (m *Model) onKeys(msg keysMsg) {
	if msg.err == nil {
		m.compareVerified(msg.chatID, msg.set.Theirs)
	}
	if msg.check || m.keys == nil || m.keys.chatID != msg.chatID {
		return
	}
	if msg.err != nil {
		m.keys.err = msg.err.Error()
		return
	}
	m.keys.set = &msg.set
}

// compareVerified raises (or takes down) the "keys changed" warning.
func (m *Model) compareVerified(chatID string, theirs []connector.DeviceKey) {
	print, _, ok := m.st.Verified(chatID)
	if !ok {
		return
	}
	changed, added := connector.KeyChanges(print, theirs)
	if m.keyWarns == nil {
		m.keyWarns = map[string]string{}
	}
	m.unwarn(m.keyWarns[chatID])
	delete(m.keyWarns, chatID)
	if len(changed)+len(added) == 0 {
		return
	}
	name := chatID
	if c, err := m.st.Chat(chatID); err == nil && c.Display != "" {
		name = c.Display
	}
	w := firstName(termSafe(name)) + "'s keys changed since you verified them: :keys to compare"
	m.keyWarns[chatID] = w
	m.Warn(w)
}

func (m Model) onKeysKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	p := m.keys
	switch k.String() {
	case "esc", "q":
		m.keys = nil
	case "j", "down":
		p.top++
	case "k", "up":
		p.top = max(0, p.top-1)
	case "v":
		if p.set == nil {
			return m, nil
		}
		print := connector.KeyPrint(p.set.Theirs)
		if print == "" {
			return m, m.flash("No keys yet: send them a message first")
		}
		_ = m.st.SetVerified(p.chatID, print, time.Now())
		m.compareVerified(p.chatID, p.set.Theirs)
		return m, m.flash("Marked " + p.name + " verified")
	case "u":
		_ = m.st.SetVerified(p.chatID, "", time.Time{})
		m.unwarn(m.keyWarns[p.chatID])
		delete(m.keyWarns, p.chatID)
		return m, m.flash(p.name + " is no longer marked verified")
	}
	return m, nil
}

func missingKeys(ks []connector.DeviceKey) int {
	n := 0
	for _, k := range ks {
		if k.Key == nil {
			n++
		}
	}
	return n
}

// keyText: the key as Messenger's app shows it: the 05 type byte (a
// Curve25519 key in Signal's encoding), then the 32 key bytes, uppercase
// pairs, 11 to a line (three even lines; the phone wraps by width).
func keyText(key []byte, w int) []string {
	var pairs []string
	for _, b := range append([]byte{0x05}, key...) {
		pairs = append(pairs, fmt.Sprintf("%02X", b))
	}
	per := 11
	for per > 3 && per*3-1 > w {
		per--
	}
	var lines []string
	for i := 0; i < len(pairs); i += per {
		lines = append(lines, strings.Join(pairs[i:min(i+per, len(pairs))], " "))
	}
	return lines
}

func (m Model) overlayKeys(lines []string) []string {
	p := m.keys
	inner := min(m.w-4, 96)
	text := inner - 4
	body := []string{
		sMuted.Render("Compare with Messenger on your phone: open this chat, tap their name,"),
		sMuted.Render("Privacy & safety › Verify end-to-end encryption. Same bytes, one key per device."),
		"",
	}
	print, at, verified := m.st.Verified(p.chatID)
	devices := func(ks []connector.DeviceKey) {
		for _, k := range ks {
			if k.Key == nil {
				continue // counted below: no key to compare (Messenger's app doesn't list these either)
			}
			label := fmt.Sprintf("device %-4d ", k.Device)
			kl := keyText(k.Key, text-len(label)-8)
			for i, l := range kl {
				pre := strings.Repeat(" ", len(label))
				if i == 0 {
					pre = label
				}
				if i == len(kl)-1 && k.This {
					l += sMuted.Render("  ← tuon")
				}
				body = append(body, pre+sBold.Render(l))
			}
		}
		if n := missingKeys(ks); n > 0 {
			body = append(body, sMuted.Render(fmt.Sprintf("+ %d more %s Messenger lists, with no key in tuon yet (none sent or received there)", n, plural(n, "device", "devices"))))
		}
	}
	count := func(ks []connector.DeviceKey) string {
		n := len(ks) - missingKeys(ks)
		return fmt.Sprintf("  %d %s", n, plural(n, "key", "keys"))
	}
	switch {
	case p.err != "":
		body = append(body, sErr.Render("Couldn't get the keys: "+termSafe(p.err)))
	case p.set == nil:
		body = append(body, sMuted.Render("Asking Messenger for their devices…"))
	default:
		changed, added := connector.KeyChanges(print, p.set.Theirs)
		switch {
		case verified && len(changed)+len(added) > 0:
			var what []string
			for _, d := range changed {
				what = append(what, fmt.Sprintf("device %d changed", d))
			}
			for _, d := range added {
				what = append(what, fmt.Sprintf("device %d is new", d))
			}
			body = append(body, sErr.Render("⚠ Changed since you verified them on "+at.Format("Jan 2")+": "+strings.Join(what, ", ")),
				sMuted.Render("Compare again on your phone; press v once they match."))
		case verified:
			body = append(body, sAccentB.Render("✓ Verified")+sMuted.Render(" on "+at.Format("Jan 2, 2006")+": keys match what you checked"))
		default:
			body = append(body, sMuted.Render("Not verified yet."))
		}
		body = append(body, "", sBold.Render(fmt.Sprintf("%s's keys", firstName(p.name)))+sMuted.Render(count(p.set.Theirs)))
		devices(p.set.Theirs)
		body = append(body, "", sBold.Render("Your keys")+sMuted.Render(count(p.set.Mine)))
		devices(p.set.Mine)
	}
	room := max(3, m.h-6)
	p.top = min(p.top, max(0, len(body)-room))
	view := body[p.top:min(len(body), p.top+room)]
	foot := "v  mark verified  ·  u  unmark  ·  j/k  scroll  ·  esc  close"

	title := ansi.Truncate("─ Keys · "+p.name+" ", inner-1, "… ")
	box := []string{sAccent.Render("┌" + title + strings.Repeat("─", max(0, inner-ansi.StringWidth(title))) + "┐")}
	for _, l := range view {
		box = append(box, sAccent.Render("│")+fit("  "+l, inner)+sAccent.Render("│"))
	}
	box = append(box, sAccent.Render("│")+fit("", inner)+sAccent.Render("│"),
		sAccent.Render("│")+fit("  "+sMuted.Render(foot), inner)+sAccent.Render("│"),
		sAccent.Render("└"+strings.Repeat("─", inner)+"┘"))
	row := max(1, (m.h-len(box))/2)
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

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
