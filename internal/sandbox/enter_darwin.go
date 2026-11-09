package sandbox

import (
	"os"
	"os/exec"
)

const sandboxExec = "/usr/bin/sandbox-exec"

// Command builds the sandboxed chat app (app is tuon-app's path) for the
// launcher to run as a child. It returns nil and the reason when there's
// no sandbox to use. The launcher is never sandboxed itself, so an
// inherited TUON_SANDBOXED (see ClearMark) can't switch this off.
func Command(app string, args []string) (*exec.Cmd, string) {
	if Disabled() {
		return nil, "sandbox off (TUON_NO_SANDBOX)"
	}
	if _, err := os.Stat(sandboxExec); err != nil {
		return nil, "sandbox unavailable: " + err.Error()
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, "sandbox unavailable: " + err.Error()
	}
	profile := Profile(realDir(home), realDir(os.TempDir()), app)
	cmd := exec.Command(sandboxExec, append([]string{"-p", profile, app}, args...)...)
	cmd.Env = append(os.Environ(), envMark+"=1")
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd, ""
}
