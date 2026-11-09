//go:build !windows

package main

import (
	"io"
	"os"

	"golang.org/x/sys/unix"

	"github.com/ralph/tuon/internal/seq"
)

// ttyIn is the terminal input Bubble Tea reads. Bubble Tea treats a read
// that doesn't fill its buffer as complete, so a mouse-wheel report cut in
// two by a burst of scrolling turned into typed text ("[<65;81;48M"). A
// read that ends inside an escape sequence or a character waits up to
// 25 ms for the rest. It never keeps bytes between reads, so Bubble Tea's
// own wait for input (on the file descriptor) stays right. It stays a
// file (Fd) so Bubble Tea still puts the terminal in raw mode.
type ttyIn struct{ *os.File }

func (t ttyIn) Read(p []byte) (int, error) {
	n, err := t.File.Read(p)
	for err == nil && n > 0 && n < len(p) && seq.CompleteLen(p[:n]) < n {
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
func appInput() io.Reader { return ttyIn{os.Stdin} }
