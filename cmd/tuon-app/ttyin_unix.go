//go:build !windows

package main

import (
	"io"
	"os"

	"golang.org/x/sys/unix"

	"github.com/ralph/tuon/internal/seq"
)

// ttyIn is the terminal input Bubble Tea reads. Bubble Tea (v1.3) reads
// 256 bytes at a time and, when a read ends inside a mouse report, types
// the rest of it as text ("[<65;81;48M" while scrolling fast: a burst is
// many reports, more than 256 bytes). So a read never ends inside an
// escape sequence or a character:
//   - a full read that ends mid-sequence returns only the complete part
//     and keeps the tail for the next read (the rest of the sequence is
//     already waiting, so that read comes at once);
//   - a short one waits up to 25 ms for the rest (a lone Esc is a key).
//
// It stays a file (Fd) so Bubble Tea still puts the terminal in raw mode.
type ttyIn struct {
	*os.File
	tail []byte
}

func (t *ttyIn) Read(p []byte) (n int, err error) {
	n = copy(p, t.tail) // usually the start of a sequence whose rest is waiting
	t.tail = t.tail[:0]
	if n > 0 && !t.ready(tailWait) {
		// nothing followed: it was a real lone Esc at the end of a full read.
		// Hand it over now; blocking here held it until the next key, so Esc
		// pressed as a fast scroll stopped seemed to freeze tuon (audit A13-11)
		return n, nil
	}
	m, err := t.File.Read(p[n:])
	n += m
	for err == nil && n > 0 && seq.CompleteLen(p[:n]) < n {
		if n == len(p) { // full: keep the cut-off tail for next time
			k := seq.CompleteLen(p[:n])
			if k == 0 {
				break // one huge sequence: hand it over as it is
			}
			t.tail = append(t.tail, p[k:n]...)
			return k, nil
		}
		fds := []unix.PollFd{{Fd: int32(t.Fd()), Events: unix.POLLIN}}
		if k, perr := unix.Poll(fds, 25); perr != nil || k == 0 {
			break // nothing more: a lone Esc, say
		}
		m, rerr := t.File.Read(p[n:])
		n += m
		if rerr != nil {
			break
		}
	}
	if err == nil && n < len(p) {
		escLast(p[:n])
	}
	return n, err
}

// tailWait (ms): how long the start of a sequence kept from a full read
// waits for its rest. Long: under load (a fast scroll redrawing) the rest
// can take well over 25 ms, and handing the Esc over alone typed the rest
// of a mouse report into the box ("[<65;133;59M", 2026-11-12). A real lone
// Esc only lands here at the very end of a full read, and is then 0.2 s late.
const tailWait = 200

// ready: input is waiting (or comes within ms milliseconds).
func (t *ttyIn) ready(ms int) bool {
	fds := []unix.PollFd{{Fd: int32(t.Fd()), Events: unix.POLLIN}}
	k, err := unix.Poll(fds, ms)
	return err == nil && k > 0
}

// escLast moves a lone Esc that's directly followed by another escape
// sequence to the end of the read. Esc pressed while a scroll still coasts
// arrives glued to the next mouse report ("\x1b\x1b[<65;…M"), which Bubble
// Tea reads as alt+esc plus typed text; "\x1b[<65;…M\x1b" reads as the
// scroll, then Esc. Only on short reads, where the end is a real boundary.
func escLast(b []byte) {
	for i := 0; i+2 < len(b); i++ {
		if b[i] == 0x1b && b[i+1] == 0x1b && (b[i+2] == '[' || b[i+2] == 'O') {
			copy(b[i:], b[i+1:])
			b[len(b)-1] = 0x1b
			return
		}
	}
}

// appInput is what Bubble Tea reads keys and mouse events from.
func appInput() io.Reader { return &ttyIn{File: os.Stdin} }
