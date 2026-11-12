//go:build !darwin && !linux

package vault

import (
	"time"

	"golang.org/x/term"
)

// QuietWait: see quietwait.go. Here, raw mode keeps typing off the screen.
func QuietWait(fd int, d time.Duration) {
	old, err := term.MakeRaw(fd)
	if err != nil {
		time.Sleep(d)
		return
	}
	time.Sleep(d)
	flushInput(fd)
	_ = term.Restore(fd, old)
}
