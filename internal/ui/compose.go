package ui

import (
	"context"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ralph/tuon/internal/connector"
	"github.com/ralph/tuon/internal/store"
)

// Attaching files: ctrl+v pastes an image (or a copied file) from the
// clipboard, dragging a file into the window attaches it, and "@~/…"
// completes a path. Attachments wait as chips above the message box and go
// out with enter.

const maxAttach = 25 << 20 // 25 MB, Messenger's limit

// sendTimeout: a send or reaction that hasn't gone through by then fails
// with a message, instead of hanging on a stalled connection.
var sendTimeout = 45 * time.Second // tests shorten it

// suggestion is one row in the pop-up above the message box: a person to
// tag, or a file or folder to attach.
type suggestion struct {
	Label    string
	Person   *store.Person
	Path     string
	IsDir    bool
	Everyone bool // "@everyone" in a group chat
}

type (
	attachedMsg struct {
		files []connector.Attachment
		err   error
	}
	pasteTextMsg string
	copiedMsg    struct {
		n    int
		err  error
		text string
	}
	// clipClear: time to clear the clipboard if it still holds text
	clipClear struct{ text string }
)

// isPathQuery: "@~/…", "@/…", "@./…" complete files; anything else is a person.
func isPathQuery(q string) bool {
	return strings.HasPrefix(q, "~") || strings.HasPrefix(q, "/") || strings.HasPrefix(q, ".")
}

func expandHome(p string) string {
	if strings.HasPrefix(p, "~") {
		home, _ := os.UserHomeDir()
		return home + p[1:]
	}
	return p
}

func shortHome(p string) string {
	home, _ := os.UserHomeDir()
	if home != "" && strings.HasPrefix(p, home) {
		return "~" + p[len(home):]
	}
	return p
}

// pathSuggestions lists files and folders matching a typed path.
func pathSuggestions(q string) []suggestion {
	full := expandHome(q)
	dir, prefix := filepath.Dir(full), filepath.Base(full)
	if strings.HasSuffix(q, "/") {
		dir, prefix = full, ""
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []suggestion
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") && !strings.HasPrefix(prefix, ".") {
			continue
		}
		if !strings.HasPrefix(strings.ToLower(name), strings.ToLower(prefix)) {
			continue
		}
		p := filepath.Join(dir, name)
		isDir := e.IsDir()
		label := shortHome(p)
		if isDir {
			label += "/"
		}
		out = append(out, suggestion{Label: label, Path: p, IsDir: isDir})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].IsDir && !out[j].IsDir })
	if len(out) > 8 {
		out = out[:8]
	}
	return out
}

func readAttachment(path string) (connector.Attachment, error) {
	st, err := os.Stat(path)
	if err != nil {
		return connector.Attachment{}, err
	}
	if st.IsDir() {
		return connector.Attachment{}, fmt.Errorf("%s is a folder", filepath.Base(path))
	}
	if st.Size() > maxAttach {
		return connector.Attachment{}, fmt.Errorf("%s is over 25 MB", filepath.Base(path))
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return connector.Attachment{}, err
	}
	return connector.Attachment{Name: filepath.Base(path), Mime: mimeOf(path, data), Data: data}, nil
}

func mimeOf(path string, data []byte) string {
	if t := mime.TypeByExtension(strings.ToLower(filepath.Ext(path))); t != "" {
		return strings.Split(t, ";")[0]
	}
	return strings.Split(http.DetectContentType(data), ";")[0]
}

// droppedPaths reads a drag-and-drop paste: one or more paths, with spaces
// escaped ("my\ file.jpg") or quoted. ok is false unless every one exists.
func droppedPaths(s string) (paths []string, ok bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, false
	}
	var cur strings.Builder
	quote := rune(0)
	flush := func() {
		if cur.Len() > 0 {
			paths = append(paths, cur.String())
			cur.Reset()
		}
	}
	esc := false
	for _, r := range s {
		switch {
		case esc:
			cur.WriteRune(r)
			esc = false
		case r == '\\':
			esc = true
		case quote != 0 && r == quote:
			quote = 0
		case quote == 0 && (r == '\'' || r == '"'):
			quote = r
		case quote == 0 && (r == ' ' || r == '\n'):
			flush()
		default:
			cur.WriteRune(r)
		}
	}
	flush()
	for _, p := range paths {
		if st, err := os.Stat(expandHome(p)); err != nil || st.IsDir() || !filepath.IsAbs(expandHome(p)) {
			return nil, false
		}
	}
	return paths, len(paths) > 0
}

func attachPaths(paths []string) tea.Cmd {
	return func() tea.Msg {
		var files []connector.Attachment
		for _, p := range paths {
			a, err := readAttachment(expandHome(p))
			if err != nil {
				return attachedMsg{err: err}
			}
			files = append(files, a)
		}
		return attachedMsg{files: files}
	}
}

// pasteClipboard: an image on the clipboard is attached; a copied file
// (from the file manager) is attached; plain text is typed into the box.
func pasteClipboard() tea.Cmd {
	return func() tea.Msg {
		if data, err := clipboardImage(); err == nil && len(data) > 0 {
			name := "pasted-" + time.Now().Format("150405") + ".png"
			return attachedMsg{files: []connector.Attachment{{Name: name, Mime: "image/png", Data: data}}}
		}
		if p := clipboardFile(); p != "" {
			a, err := readAttachment(p)
			if err != nil {
				return attachedMsg{err: err}
			}
			return attachedMsg{files: []connector.Attachment{a}}
		}
		if t := clipboardText(); t != "" {
			return pasteTextMsg(t)
		}
		return attachedMsg{err: errors.New("nothing to paste")}
	}
}

// sendAll sends the files, then the text (if any). If a file fails, the
// text and the files not sent yet come back in sentMsg, so the draft can be
// put back instead of lost.
func sendAll(conn connector.Connector, ctx context.Context, chat connector.Chat, text string, mentions []connector.Mention, files []pendingFile, reply *connector.ReplyRef) tea.Cmd {
	// files first, then the text (Messenger shows them as separate messages)
	chatID := chat.ID
	return func() tea.Msg {
		conn.KnowChat(chat)
		for i, f := range files {
			ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
			err := conn.SendFile(ctx, chatID, f.file)
			cancel()
			if err != nil {
				back := make([]string, 0, len(files)-i+1)
				for _, rest := range files[i:] {
					back = append(back, rest.token)
				}
				if text != "" {
					back = append(back, text)
				}
				return sentMsg{err: fmt.Errorf("%s: %w", f.file.Name, err), chatID: chatID,
					restore: strings.Join(back, " "), restoreAtts: files[i:]}
			}
		}
		if text == "" {
			return sentMsg{}
		}
		// a failed send takes its "sending…" copy away, so put the text back
		ctx, cancel := context.WithTimeout(ctx, sendTimeout)
		defer cancel()
		if err := conn.Send(ctx, chatID, text, mentions, reply); err != nil {
			return sentMsg{err: err, chatID: chatID, restore: text}
		}
		return sentMsg{}
	}
}

func copyText(text string, n int) tea.Cmd {
	return func() tea.Msg { return copiedMsg{n: n, err: writeClipboard(text), text: text} }
}

// clipboardKeep: copied messages leave the clipboard after this, so they
// don't sit in clipboard history or get pasted somewhere by mistake.
var clipboardKeep = 60 * time.Second

// readClipboard and writeClipboard are swappable in tests.
var readClipboard, writeClipboard = clipboardText, copyToClipboard

func (m Model) messagesText(from, to int) string {
	var b strings.Builder
	for i := max(0, from); i <= min(to, len(m.msgs)-1); i++ {
		msg := m.msgs[i]
		who := senderLabel(msg)
		if msg.FromMe {
			who = "You"
		}
		text := msg.Text
		if text == "" && len(msg.Media) > 0 {
			text = "[" + mediaLabel(msg.Media[0].Kind) + "]"
		}
		if from == to {
			return text // one message: just its text
		}
		fmt.Fprintf(&b, "%s: %s\n", who, text)
	}
	return strings.TrimRight(b.String(), "\n")
}

func humanSize(n int) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%d KB", n>>10)
	}
	return fmt.Sprintf("%d B", n)
}
