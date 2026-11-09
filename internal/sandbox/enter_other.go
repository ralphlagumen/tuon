//go:build !darwin

package sandbox

import "os/exec"

// Command: no sandbox on this system yet (Linux Landlock is planned).
func Command(string, []string) (*exec.Cmd, string) { return nil, "" }
