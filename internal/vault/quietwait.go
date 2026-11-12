//go:build darwin || linux

package vault

import (
	"os"
	"os/signal"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// QuietWait waits after a wrong passphrase with the terminal's echo off,
// then throws away whatever was typed meanwhile. Otherwise a passphrase
// typed early shows on screen, and its keys end up in front of the next try.
//
// Only echo goes off (not signals): ctrl+c still quits. If tuon is stopped
// during the wait, the terminal gets its echo back first (audit A13-5).
func QuietWait(fd int, d time.Duration) {
	old, err := unix.IoctlGetTermios(fd, ioctlGetTermios)
	if err != nil {
		time.Sleep(d)
		return
	}
	quiet := *old
	quiet.Lflag &^= unix.ECHO | unix.ECHONL
	if unix.IoctlSetTermios(fd, ioctlSetTermios, &quiet) != nil {
		time.Sleep(d)
		return
	}
	restore := func() { _ = unix.IoctlSetTermios(fd, ioctlSetTermios, old) }

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	defer signal.Stop(sigs)
	select {
	case <-time.After(d):
	case <-sigs:
		flushInput(fd)
		restore()
		os.Exit(130) // what a shell expects after ctrl+c
	}
	flushInput(fd)
	restore()
}
