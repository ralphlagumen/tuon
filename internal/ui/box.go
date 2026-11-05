package ui

import (
	"regexp"
	"strings"

	bkey "github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/ralph/tuon/internal/connector"
)

// box is the message box: several lines, growing up to maxBoxLines.
// enter sends; alt+enter (option+enter), shift+enter or ctrl+j add a line.
type box struct{ ta textarea.Model }

const maxBoxLines = 6

func newBox() box {
	ta := textarea.New()
	ta.Prompt = ""
	ta.ShowLineNumbers = false
	ta.CharLimit = 4000
	ta.MaxHeight = 0
	ta.SetHeight(1)
	ta.EndOfBufferCharacter = ' '
	ta.KeyMap.InsertNewline = bkey.NewBinding(bkey.WithKeys("alt+enter", "shift+enter", "ctrl+j"))
	plain := lipgloss.NewStyle()
	for _, st := range []*textarea.Style{&ta.FocusedStyle, &ta.BlurredStyle} {
		st.Base, st.CursorLine, st.Text, st.Placeholder = plain, plain, sText, sMuted
		st.EndOfBuffer, st.Prompt, st.LineNumber, st.CursorLineNumber = plain, plain, plain, plain
	}
	ta.Cursor.Style = sAccent
	return box{ta: ta}
}

func (b box) Value() string          { return b.ta.Value() }
func (b *box) SetValue(s string)     { b.ta.SetHeight(maxBoxLines); b.ta.SetValue(s); b.fit() }
func (b *box) Focus() tea.Cmd        { return b.ta.Focus() }
func (b *box) Blur()                 { b.ta.Blur() }
func (b box) View() string           { return b.ta.View() }
func (b *box) SetWidth(w int)        { b.ta.SetWidth(w); b.fit() }
func (b box) Height() int            { return b.ta.Height() }
func (b *box) CursorEnd()            { b.SetCursor(len([]rune(b.Value()))) }
func (b box) Lines() []string        { return strings.Split(b.Value(), "\n") }
func (b *box) InsertString(s string) { b.ta.SetHeight(maxBoxLines); b.ta.InsertString(s); b.fit() }

func (b box) Update(msg tea.Msg) (box, tea.Cmd) {
	var cmd tea.Cmd
	// full height while editing, so the text area never scrolls a short
	// draft out of view; fit() shrinks it back afterwards
	b.ta.SetHeight(maxBoxLines)
	b.ta, cmd = b.ta.Update(msg)
	b.fit()
	return b, cmd
}

// fit grows the box with its text, up to maxBoxLines rows.
func (b *box) fit() {
	w := max(1, b.ta.Width())
	rows := 0
	for _, l := range b.Lines() {
		rows += max(1, (ansi.StringWidth(l)+w-1)/w)
	}
	b.ta.SetHeight(max(1, min(maxBoxLines, rows)))
}

// Position is the cursor's place in the whole text, in characters.
func (b box) Position() int {
	lines := b.Lines()
	row := min(b.ta.Line(), len(lines)-1)
	pos := 0
	for i := 0; i < row; i++ {
		pos += len([]rune(lines[i])) + 1
	}
	li := b.ta.LineInfo()
	return pos + min(li.StartColumn+li.CharOffset, len([]rune(lines[row])))
}

// SetCursor puts the cursor at a place in the whole text.
func (b *box) SetCursor(pos int) {
	lines := b.Lines()
	row, col := 0, pos
	for row < len(lines)-1 && col > len([]rune(lines[row])) {
		col -= len([]rune(lines[row])) + 1
		row++
	}
	for i := 0; i < 4*len(lines)+8 && b.ta.Line() > row; i++ {
		b.ta.CursorUp()
	}
	for i := 0; i < 4*len(lines)+8 && b.ta.Line() < row; i++ {
		b.ta.CursorDown()
	}
	b.ta.SetCursor(max(0, col))
}

// ---- files in the text: [Image #1], [File #2] ----

// pendingFile is a file waiting in the draft, shown in the text as its token.
type pendingFile struct {
	token string
	file  connector.Attachment
}

var tokenRe = regexp.MustCompile(`\[(Image|File) #\d+\]`)

// splitDraft: the files whose tokens are still in the text (in text order),
// and the text without the tokens. Deleting a token drops its file.
func splitDraft(text string, files []pendingFile) (string, []connector.Attachment) {
	var out []connector.Attachment
	for _, tok := range tokenRe.FindAllString(text, -1) {
		for _, f := range files {
			if f.token == tok {
				out = append(out, f.file)
			}
		}
	}
	clean := tokenRe.ReplaceAllString(text, "")
	var lines []string
	for _, l := range strings.Split(clean, "\n") {
		lines = append(lines, strings.Join(strings.Fields(l), " "))
	}
	return strings.TrimSpace(strings.Join(lines, "\n")), out
}

// tokenBefore: the token right before the cursor, if any (backspace removes it whole).
func tokenBefore(before string) string {
	locs := tokenRe.FindAllStringIndex(before, -1)
	if len(locs) == 0 {
		return ""
	}
	last := locs[len(locs)-1]
	if last[1] == len(before) {
		return before[last[0]:last[1]]
	}
	return ""
}
