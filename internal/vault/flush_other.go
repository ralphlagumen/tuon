//go:build !darwin && !linux

package vault

// flushInput: no portable way here; echo is still off during the wait.
func flushInput(fd int) {}
