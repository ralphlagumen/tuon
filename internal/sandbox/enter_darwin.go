package sandbox

import (
	"os"
	"os/exec"
)

const sandboxExec = "/usr/bin/sandbox-exec"

// Command builds the sandboxed chat app (app is tuon-app's path) for the
// launcher to run as a child. It returns nil and the reason when there's
// no sandbox to use (the reason is empty when you turned it off).
func Command(app string, args []string) (*exec.Cmd, string) {
	if Active() || Disabled() {
		return nil, ""
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
