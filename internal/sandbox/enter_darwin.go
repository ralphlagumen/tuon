package sandbox

import (
	"os"
	"syscall"
)

const sandboxExec = "/usr/bin/sandbox-exec"

// Enter re-launches tuon inside the sandbox. It only returns if that
// isn't possible (already sandboxed, turned off, or no sandbox-exec), with
// the reason; the caller decides whether to carry on.
func Enter() string {
	if Active() || Disabled() {
		return ""
	}
	if _, err := os.Stat(sandboxExec); err != nil {
		return "sandbox unavailable: " + err.Error()
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "sandbox unavailable: " + err.Error()
	}
	exe, err := os.Executable()
	if err != nil {
		return "sandbox unavailable: " + err.Error()
	}
	tmp := os.TempDir()
	profile := Profile(realDir(home), realDir(tmp))
	args := append([]string{"sandbox-exec", "-p", profile, exe}, os.Args[1:]...)
	env := append(os.Environ(), envMark+"=1")
	err = syscall.Exec(sandboxExec, args, env) // replaces this process
	return "sandbox unavailable: " + err.Error()
}
