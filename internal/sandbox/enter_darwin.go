package sandbox

import (
	"os"
	"os/exec"
	"syscall"
)

const sandboxExec = "/usr/bin/sandbox-exec"

// Command builds the sandboxed tuon for the chat app: the launcher (this
// process, unsandboxed) runs it as a child, so logging in — which opens a
// browser that needs the keychain — can happen outside the sandbox. It
// returns nil and the reason when there's no sandbox to use.
func Command(args []string) (*exec.Cmd, string) {
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
	exe, err := os.Executable()
	if err != nil {
		return nil, "sandbox unavailable: " + err.Error()
	}
	profile := Profile(realDir(home), realDir(os.TempDir()), exe)
	cmd := exec.Command(sandboxExec, append([]string{"-p", profile, exe}, args...)...)
	cmd.Env = append(os.Environ(), envMark+"=1")
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd, ""
}

// Enter re-launches this process inside the sandbox in place (for
// `tuon sandbox-test`). It only returns if that isn't possible.
func Enter() string {
	cmd, why := Command(os.Args[1:])
	if cmd == nil {
		return why
	}
	err := syscall.Exec(cmd.Path, cmd.Args, cmd.Env)
	return "sandbox unavailable: " + err.Error()
}
