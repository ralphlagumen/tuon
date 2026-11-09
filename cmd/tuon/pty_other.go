//go:build !darwin

package main

import "os/exec"

// runOnPty: only macOS sandboxes the app so far; elsewhere it shares the
// terminal (ok is false: run it the usual way).
func runOnPty(*exec.Cmd, func()) (int, bool, error) { return 0, false, nil }
