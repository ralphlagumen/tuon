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
	n = copy(p, t.tail)
	t.tail = t.tail[:0]
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
	return n, err
}

// appInput is what Bubble Tea reads keys and mouse events from.
func appInput() io.Reader { return &ttyIn{File: os.Stdin} }
