//go:build !windows

package main

import (
	"errors"
	"fmt"
	"os"
	"syscall"

	"golang.org/x/term"

	"github.com/ralph/tuon/internal/sandbox"
)

// sharesTerminal: nil (allowed) when the app's terminal is the launcher's.
func sharesTerminal() error {
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return errors.New("no terminal to type into")
	}
	parent := os.Getenv(sandbox.ParentTTYEnv)
	if parent == "" {
		return nil // no terminal of its own was set up
	}
	st, err := os.Stdin.Stat()
	if err != nil {
		return nil
	}
	if sys, ok := st.Sys().(*syscall.Stat_t); ok && fmt.Sprint(sys.Rdev) != parent {
		return errors.New("own terminal")
	}
	return nil
}
