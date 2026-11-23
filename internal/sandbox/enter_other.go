//go:build !darwin && !linux

package sandbox

import (
	"fmt"
	"os"
	"os/exec"
)

// Command: no sandbox on this system yet (Windows: backlog).
func Command(string, []string, int) (*exec.Cmd, string) { return nil, "" }

// TempBase is the chat app's temp folder: the system's here.
func TempBase() (string, error) { return os.TempDir(), nil }

// Enter is Linux's enter step; elsewhere there's none.
func Enter(string, []string) {
	fmt.Fprintln(os.Stderr, "tuon: no sandbox enter step on this system")
	os.Exit(1)
}
