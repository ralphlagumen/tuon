package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"

	"github.com/ralph/tuon/internal/sandbox"
	"github.com/ralph/tuon/internal/store"
	"github.com/ralph/tuon/internal/vault"
)

// Exit codes the sandboxed chat app uses to hand work back to the launcher.
const (
	exitNeedLogin = 41 // Messenger logged us out: log in again
	exitNeedSetup = 42 // no saved login yet: first-run setup
)

// launchSandboxed runs the chat app in the sandbox and does the logins it
// asks for outside it. ok is false when there's no sandbox (other systems,
// TUON_NO_SANDBOX, already inside): then the caller runs tuon directly.
func launchSandboxed(args []string) (code int, ok bool) {
	if c, why := sandbox.Command(args); c == nil {
		sandboxNote = why
		return 0, false
	}
	// unlock once here; the chat app gets the key over a pipe
	if vault.Exists(store.Dir()) {
		if _, err := openVault(); err != nil {
			fmt.Fprintln(os.Stderr, "tuon:", err)
			return 1, true
		}
	}
	for {
		cmd, _ := sandbox.Command(args)
		if vlt != nil {
			r, w, err := os.Pipe()
			if err != nil {
				fmt.Fprintln(os.Stderr, "tuon:", err)
				return 1, true
			}
			_, _ = w.Write(vlt.Handoff())
			w.Close()
			cmd.ExtraFiles = []*os.File{r} // fd 3 in the child
			cmd.Env = append(cmd.Env, keyFDEnv+"=3")
			defer r.Close()
		}
		// ctrl+c belongs to the chat app while it runs
		signal.Ignore(os.Interrupt)
		err := cmd.Run()
		signal.Reset(os.Interrupt)
		code = 0
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			code = ee.ExitCode()
		} else if err != nil {
			fmt.Fprintln(os.Stderr, "tuon:", err)
			return 1, true
		}
		switch code {
		case exitNeedLogin:
			fmt.Println("\n  Messenger logged you out. Log in again to keep going.")
			if err := login(); err != nil {
				fmt.Fprintln(os.Stderr, "tuon:", err)
				return 1, true
			}
		case exitNeedSetup:
			if err := setup(); err != nil {
				fmt.Fprintln(os.Stderr, "tuon:", err)
				return 1, true
			}
		default:
			return code, true
		}
	}
}
