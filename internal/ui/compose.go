package ui

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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

// suggestion is one row in the pop-up above the message box: a person to
// tag, or a file or folder to attach.
type suggestion struct {
	Label  string
	Person *store.Person
	Path   string
	IsDir  bool
}

type (
	attachedMsg struct {
		files []connector.Attachment
		err   error
	}
	pasteTextMsg string
	copiedMsg    struct {
		n   int
		err error
	}
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
// (from Finder) is attached; plain text is typed into the box.
func pasteClipboard() tea.Cmd {
	return func() tea.Msg {
		if runtime.GOOS != "darwin" {
			return attachedMsg{err: errors.New("pasting images works on macOS only")}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if out, err := exec.CommandContext(ctx, "osascript", "-e", "the clipboard as «class PNGf»").Output(); err == nil {
			s := string(out)
			if i := strings.Index(s, "PNGf"); i >= 0 {
				h := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(s[i+4:]), "»"))
				if data, err := hex.DecodeString(h); err == nil && len(data) > 0 {
					name := "pasted-" + time.Now().Format("150405") + ".png"
					return attachedMsg{files: []connector.Attachment{{Name: name, Mime: "image/png", Data: data}}}
				}
			}
		}
		if out, err := exec.CommandContext(ctx, "osascript", "-e", "POSIX path of (the clipboard as «class furl»)").Output(); err == nil {
			if p := strings.TrimSpace(string(out)); p != "" {
				a, err := readAttachment(p)
				if err != nil {
					return attachedMsg{err: err}
				}
				return attachedMsg{files: []connector.Attachment{a}}
			}
		}
		if out, err := exec.CommandContext(ctx, "pbpaste").Output(); err == nil && len(out) > 0 {
			return pasteTextMsg(out)
		}
		return attachedMsg{err: errors.New("nothing to paste")}
	}
}

// sendAll sends the files, then the text (if any).
func sendAll(conn connector.Connector, ctx context.Context, chatID, text string, mentions []connector.Mention, files []connector.Attachment) tea.Cmd {
	return func() tea.Msg {
		for _, f := range files {
			ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
			err := conn.SendFile(ctx, chatID, f)
			cancel()
			if err != nil {
				return sentMsg{fmt.Errorf("%s: %w", f.Name, err)}
			}
		}
		if text == "" {
			return sentMsg{}
		}
		return sentMsg{conn.Send(ctx, chatID, text, mentions)}
	}
}

func copyText(text string, n int) tea.Cmd {
	return func() tea.Msg {
		if runtime.GOOS == "darwin" {
			cmd := exec.Command("pbcopy")
			cmd.Stdin = strings.NewReader(text)
			return copiedMsg{n: n, err: cmd.Run()}
		}
		return copiedMsg{err: errors.New("copy works on macOS only")}
	}
}

// messagesText is what gets copied: "Name: text" per message.
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
