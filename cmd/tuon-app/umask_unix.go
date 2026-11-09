//go:build !windows

package main

import "syscall"

// setUmask: files tuon creates (database, logs, photos) start out
// readable only by you.
func setUmask() { syscall.Umask(0o077) }
