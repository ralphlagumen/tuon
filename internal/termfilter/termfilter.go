// Package termfilter cleans what the sandboxed chat app writes before the
// launcher hands it to your real terminal. The terminal runs outside the
// sandbox, so some escape codes let a bad library reach past it: a link
// (OSC 8) your click opens with any URL scheme, a kitty photo read from a
// file path on disk, iTerm's file download (OSC 1337), a fake window title
// or desktop notification, DCS strings that tunnel other codes. Ordinary
// codes (cursor, color, modes) pass untouched; of the string codes, only
// what tuon uses passes:
//
//   - OSC 10/11/12 "?" (color queries Bubble Tea sends)
//   - OSC 52 writes (tuon's clipboard fallback; reads are dropped. The app
//     can run pbcopy/pbpaste anyway, so this keeps the surface small)
//   - APC "G" kitty graphics with the image sent inline (t=d), or no t
//
// Everything else (other OSC, DCS, PM, SOS, other APC, C1 controls written
// as UTF-8) is dropped. It is a stream filter: codes split across reads are
// handled.
package termfilter

import "bytes"

const (
	esc = 0x1b
	bel = 0x07
)

type state int

const (
	ground   state = iota
	sawEsc         // ESC, waiting for the next byte
	sawC2          // 0xC2, maybe the start of a UTF-8 C1 control
	header         // collecting the start of an OSC/APC string to decide on it
	passing        // inside an allowed string
	dropping       // inside a refused string
	strEsc         // ESC inside a string: ST (ESC \) or an abort
)

// maxHeader: how much of a string's start is read before deciding; a
// longer start than any allowed code needs is refused.
const maxHeader = 512

// Filter is one stream's state. Not safe for concurrent use.
type Filter struct {
	st      state
	kind    byte // ']' OSC, '_' APC, 'P' DCS, '^' PM, 'X' SOS
	hdr     []byte
	pass    bool // in strEsc: whether the string was being passed
	out     []byte
	Dropped int // codes dropped so far
}

// Filter returns p with refused codes removed. The result is valid until
// the next call.
func (f *Filter) Filter(p []byte) []byte {
	f.out = f.out[:0]
	for i := 0; i < len(p); i++ {
		f.step(p[i])
	}
	return f.out
}

func (f *Filter) step(b byte) {
	switch f.st {
	case ground:
		switch b {
		case esc:
			f.st = sawEsc
		case 0xC2:
			f.st = sawC2
		default:
			f.out = append(f.out, b)
		}
	case sawC2:
		if b >= 0x80 && b <= 0x9F { // U+0080–U+009F: C1 controls (0x9B CSI, 0x9D OSC…)
			f.Dropped++
			f.st = ground
			return
		}
		f.out = append(f.out, 0xC2)
		f.st = ground
		f.step(b)
	case sawEsc:
		switch b {
		case ']', '_':
			f.kind, f.hdr, f.st = b, f.hdr[:0], header
		case 'P', '^', 'X':
			f.kind, f.st = b, dropping
			f.Dropped++
		case esc: // a lone ESC, then another code
			f.out = append(f.out, esc)
		default:
			f.out = append(f.out, esc, b)
			f.st = ground
		}
	case header:
		if b == esc || (b == bel && f.kind == ']') {
			allowed := f.decide(true)
			if allowed {
				f.open()
			} else {
				f.Dropped++
			}
			if b == bel {
				if allowed {
					f.out = append(f.out, bel)
				}
				f.st = ground
				return
			}
			f.pass, f.st = allowed, strEsc
			return
		}
		f.hdr = append(f.hdr, b)
		if len(f.hdr) > maxHeader {
			f.Dropped++
			f.st = dropping
			return
		}
		switch d := f.decision(); d {
		case yes:
			f.open()
			f.st = passing
		case no:
			f.Dropped++
			f.st = dropping
		}
	case passing, dropping:
		pass := f.st == passing
		switch {
		case b == esc:
			f.pass, f.st = pass, strEsc
		case b == bel && f.kind == ']':
			if pass {
				f.out = append(f.out, bel)
			}
			f.st = ground
		default:
			if pass {
				f.out = append(f.out, b)
			}
		}
	case strEsc:
		switch {
		case b == '\\': // ST: the string ends
			if f.pass {
				f.out = append(f.out, esc, '\\')
			}
			f.st = ground
		case !f.pass:
			// a refused string ends only at ST (or BEL for OSC): an ESC
			// inside it (tmux passthrough doubles them) mustn't let the
			// rest out as codes
			f.st = dropping
			if b == esc {
				f.st = strEsc
			}
		default:
			// ESC + something else ends an allowed string: close it
			// properly, then that ESC starts a new code
			f.out = append(f.out, esc, '\\')
			f.st = sawEsc
			f.step(b)
		}
	}
}

// open writes the start of an allowed string.
func (f *Filter) open() {
	f.out = append(f.out, esc, f.kind)
	f.out = append(f.out, f.hdr...)
}

type verdict int

const (
	undecided verdict = iota
	yes
	no
)

// decision looks at the header collected so far.
func (f *Filter) decision() verdict {
	h := f.hdr
	if f.kind == '_' { // APC: kitty graphics only, image data inline
		if h[0] != 'G' {
			return no
		}
		i := bytes.IndexByte(h, ';')
		if i < 0 {
			return undecided // control keys still coming
		}
		return kittyOK(h[1:i])
	}
	// OSC: the number, then the rest
	num, rest, ok := bytes.Cut(h, []byte(";"))
	if !ok {
		if len(h) > 4 || !digits(h) {
			return no
		}
		return undecided
	}
	switch string(num) {
	case "10", "11", "12":
		if len(rest) == 0 {
			return undecided
		}
		if string(rest) == "?" {
			return undecided // allowed only if the string ends here (decide)
		}
		return no
	case "52":
		sel, data, ok := bytes.Cut(rest, []byte(";"))
		if !ok {
			if len(sel) > 8 {
				return no
			}
			return undecided
		}
		if len(data) == 0 {
			return undecided
		}
		if data[0] == '?' {
			return no // a clipboard read
		}
		return yes
	}
	return no
}

// decide: the string ended inside the header.
func (f *Filter) decide(ended bool) bool {
	h := f.hdr
	if f.kind == '_' {
		if len(h) == 0 || h[0] != 'G' {
			return false
		}
		ctl := h[1:]
		if i := bytes.IndexByte(h, ';'); i >= 0 {
			ctl = h[1:i]
		}
		return kittyOK(ctl) == yes
	}
	num, rest, _ := bytes.Cut(h, []byte(";"))
	switch string(num) {
	case "10", "11", "12":
		return string(rest) == "?"
	case "52":
		_, data, ok := bytes.Cut(rest, []byte(";"))
		return ok && len(data) > 0 && data[0] != '?'
	}
	return false
}

// kittyOK: kitty graphics control keys ("a=t,f=100,t=d,i=1"). The image
// must come inline: t=f (a file), t=t (a temp file) and t=s (shared
// memory) make the terminal read from disk outside the sandbox.
func kittyOK(ctl []byte) verdict {
	for _, kv := range bytes.Split(ctl, []byte(",")) {
		k, v, _ := bytes.Cut(kv, []byte("="))
		if string(k) == "t" && string(v) != "d" {
			return no
		}
	}
	return yes
}

func digits(b []byte) bool {
	for _, c := range b {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
