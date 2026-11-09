// Package seq knows where terminal input can be cut without splitting an
// escape sequence or a character. No dependencies (the launcher uses it).
package seq

import "unicode/utf8"

// CompleteLen is how much of b can go to the app now without cutting an
// escape sequence (a mouse wheel report, an arrow key, a terminal reply)
// or a UTF-8 character in two. The app's input reader treats a short read
// as complete, so half a mouse report would arrive as typed text
// ("[<65;95;46M"). The rest waits for the next read, or a short timeout
// (a lone Esc is a real key press).
func CompleteLen(b []byte) int {
	esc := -1
	for i := len(b) - 1; i >= 0 && i >= len(b)-64; i-- {
		if b[i] == 0x1b {
			esc = i
			break
		}
	}
	if esc >= 0 && !seqComplete(b[esc:]) {
		return esc
	}
	// a character cut at the end
	for k := 1; k <= 3 && k <= len(b); k++ {
		if c := b[len(b)-k]; c >= 0xC0 { // a start byte
			if !utf8.FullRune(b[len(b)-k:]) {
				return len(b) - k
			}
			break
		} else if c < 0x80 {
			break
		}
	}
	return len(b)
}

// seqComplete: s starts with ESC; is the sequence finished?
func seqComplete(s []byte) bool {
	if len(s) < 2 {
		return false
	}
	switch s[1] {
	case '[': // CSI: parameters and intermediates (0x20-0x3F), then a final byte
		for _, c := range s[2:] {
			if c >= 0x40 && c <= 0x7E {
				return true
			}
			if c < 0x20 || c > 0x3F {
				return true // not a CSI after all: don't hold it
			}
		}
		return false
	case ']': // OSC (a terminal's reply): ends with BEL or ESC \
		for i := 2; i < len(s); i++ {
			if s[i] == 0x07 {
				return true
			}
		}
		return false
	case 'O': // SS3: one more byte
		return len(s) >= 3
	}
	return true // alt+key, or ESC \ ending an earlier OSC
}
