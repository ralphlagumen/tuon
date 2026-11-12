package vault

import (
	"time"

	"golang.org/x/term"
)

// QuietWait waits after a wrong passphrase with the terminal's echo off,
// then throws away whatever was typed meanwhile. Otherwise a passphrase
// typed early shows on screen, and its keys end up in front of the next try.
func QuietWait(fd int, d time.Duration) {
	old, err := term.MakeRaw(fd) // no echo
	if err != nil {
		time.Sleep(d)
		return
	}
	time.Sleep(d)
	flushInput(fd)
	_ = term.Restore(fd, old)
}
