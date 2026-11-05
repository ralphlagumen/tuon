package connector

import "strings"

// SafeText removes characters that could control the terminal or disguise
// text. Everything shown in tuon comes from other people (names, messages,
// link titles), so it must never carry:
//   - control codes (ESC starts terminal commands: retitle the window,
//     write the clipboard, move the cursor), DEL and the C1 range
//   - bidirectional overrides, which can make text read backwards
//     ("gpj.exe" shown as "exe.jpg")
//
// Newlines are kept when keepNewlines is true (message bodies); tabs become
// spaces.
func SafeText(s string, keepNewlines bool) string {
	if s == "" {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r == '\n' && keepNewlines:
			b.WriteRune(r)
		case r == '\t':
			b.WriteRune(' ')
		case r == 0x2028 || r == 0x2029: // line / paragraph separators: a plain break
			if keepNewlines {
				b.WriteRune('\n')
			} else {
				b.WriteRune(' ')
			}
		case r == 0x200b, r == 0x2060, r == 0xfeff, r >= 0x2061 && r <= 0x2064:
			// invisible spaces and joiners: dropped, so "Ra​ine" can't pose as "Raine"
		case r < 0x20, r == 0x7f, r >= 0x80 && r <= 0x9f:
			// control codes: dropped
		case r >= 0x202a && r <= 0x202e, r >= 0x2066 && r <= 0x2069, r == 0x200e, r == 0x200f, r == 0x061c:
			// bidi overrides and marks: dropped
		case r == 0xfffd:
			// replacement char from broken input: dropped
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
