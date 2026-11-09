// Command tuon starts tuon. It is deliberately small: it unlocks tuon's
// vault, starts the chat app (tuon-app, next to it) inside the macOS
// sandbox, opens the login browser when the app asks for one, and installs
// updates. All Messenger, encryption and database code lives in tuon-app,
// so none of it (or of its dependencies) ever runs outside the sandbox.
// TestLauncherStaysSmall keeps it that way.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"golang.org/x/term"

	"github.com/ralph/tuon/internal/browserlogin"
	"github.com/ralph/tuon/internal/opener"
	"github.com/ralph/tuon/internal/paths"
	"github.com/ralph/tuon/internal/sandbox"
	"github.com/ralph/tuon/internal/vault"
)

func main() {
	setUmask()
	sandbox.ClearMark()
	args := os.Args[1:]
	if len(args) > 0 && args[0] == "update" { // needs to write tuon's own files: never sandboxed
		if err := update(); err != nil {
			fail(err)
		}
		return
	}
	app, err := findApp()
	if err != nil {
		fail(err)
	}
	if base := paths.WorkBase(); base != "" { // its folder for update and login work, out of the app's reach
		_ = os.MkdirAll(base, 0o700)
	}
	os.Exit(launch(app, args))
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "tuon:", err)
	os.Exit(1)
}

// appName is the chat app's file, next to tuon.
func appName() string {
	if runtime.GOOS == "windows" {
		return "tuon-app.exe"
	}
	return "tuon-app"
}

// findApp finds tuon-app next to this program, and only there (never on
// PATH). One that other users can change isn't used.
func findApp() (string, error) {
	exe, err := os.Executable()
	if err == nil {
		exe, err = filepath.EvalSymlinks(exe)
	}
	if err != nil {
		return "", err
	}
	p := filepath.Join(filepath.Dir(exe), appName())
	st, err := os.Stat(p)
	if err != nil || !st.Mode().IsRegular() {
		return "", fmt.Errorf("%s is missing next to tuon (%s): run `tuon update` again, or ./install.sh in tuon's source folder", appName(), filepath.Dir(exe))
	}
	if runtime.GOOS != "windows" && st.Mode().Perm()&0o022 != 0 {
		return "", fmt.Errorf("%s can be changed by other users, so tuon won't run it: chmod 755 %s", p, p)
	}
	return p, nil
}

// launch runs the chat app until it's done and returns its exit code.
func launch(app string, args []string) int {
	if c, why := sandbox.Command(app, args); c == nil {
		return runPlain(app, args, why)
	}
	var key []byte   // the unlocked vault's key, for the app
	var login []byte // cookies from a browser login, for the app
	for {
		if key == nil && needsKey(args) {
			v, err := unlock()
			if err != nil {
				fmt.Fprintln(os.Stderr, "tuon:", err)
				return 1
			}
			key = v.Handoff()
		}
		cmd, _ := sandbox.Command(app, args)
		code, err := runWith(cmd, key, login)
		clear(login)
		login = nil
		if err != nil {
			fmt.Fprintln(os.Stderr, "tuon:", err)
			return 1
		}
		if code == 0 && len(args) > 0 && args[0] == "logout" {
			forgetBrowser() // the login window's profile holds a Facebook login too
		}
		if code != sandbox.ExitBrowserLogin {
			return code
		}
		if login, err = browserLogin(); err != nil {
			fmt.Fprintln(os.Stderr, "tuon:", err)
			return 1
		}
	}
}

// needsKey: the commands that open tuon's data get the key from here, so a
// browser login in between doesn't ask for Touch ID twice. The rest (help,
// logout, sandbox-test) don't need it, so they don't ask.
func needsKey(args []string) bool {
	if len(args) == 0 {
		return true
	}
	switch args[0] {
	case "--here", "-here", "setup", "login", "history", "relink":
		return true
	}
	return false
}

// forgetBrowser deletes the login window's kept profile (the sandboxed
// app can't: it's outside what the app may write).
func forgetBrowser() {
	if base := paths.WorkBase(); base != "" {
		if err := os.RemoveAll(filepath.Join(base, browserlogin.ProfileName)); err == nil {
			fmt.Println("  Deleted the login window's browser profile.")
		}
	}
}

// runPlain runs the app without a sandbox (other systems, or turned off):
// it then unlocks and logs in by itself.
func runPlain(app string, args []string, why string) int {
	cmd := exec.Command(app, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	cmd.Env = os.Environ()
	if why != "" {
		cmd.Env = append(cmd.Env, sandbox.NoteEnv+"="+why)
	}
	code, err := run(cmd)
	if err != nil {
		fmt.Fprintln(os.Stderr, "tuon:", err)
		return 1
	}
	if code == 0 && len(args) > 0 && args[0] == "logout" {
		forgetBrowser()
	}
	return code
}

// runWith starts the sandboxed app with the key on fd 3 and a login on
// fd 4 (each only when there is one). Pipes, never files or arguments.
func runWith(cmd *exec.Cmd, key, login []byte) (int, error) {
	pipe := func(b []byte, env string) (*os.File, error) {
		if b == nil {
			return os.Open(os.DevNull)
		}
		r, w, err := os.Pipe()
		if err != nil {
			return nil, err
		}
		_, err = w.Write(b) // small: fits the pipe's buffer before the app reads
		w.Close()
		if err != nil {
			r.Close()
			return nil, err
		}
		cmd.Env = append(cmd.Env, env)
		return r, nil
	}
	k, err := pipe(key, sandbox.KeyFDEnv+"=3")
	if err != nil {
		return 0, err
	}
	defer k.Close()
	l, err := pipe(login, sandbox.LoginFDEnv+"=4")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	// fd 5 and 6: the app asks the launcher to open links and photos and
	// to read the clipboard (it can't start open or osascript itself)
	reqR, reqW, err := os.Pipe()
	if err != nil {
		return 0, err
	}
	repR, repW, err := os.Pipe()
	if err != nil {
		reqR.Close()
		reqW.Close()
		return 0, err
	}
	cmd.Env = append(cmd.Env, opener.Env+"=5")
	cmd.ExtraFiles = []*os.File{k, l, reqW, repR} // fd 3, 4, 5, 6 in the app
	done := make(chan struct{})
	go func() {
		opener.Serve(reqR, repW, handleOpen)
		close(done)
	}()
	defer func() {
		reqR.Close()
		repW.Close()
		<-done
	}()
	// started: the app's ends stay open in the app only
	started := func() { reqW.Close(); repR.Close() }
	code, onPty, err := runOnPty(cmd, started)
	if onPty {
		return code, err
	}
	if err := cmd.Start(); err != nil {
		started()
		return 0, err
	}
	started()
	return wait(cmd)
}

// run runs the app in the foreground and returns its exit code.
func run(cmd *exec.Cmd) (int, error) {
	if err := cmd.Start(); err != nil {
		return 0, err
	}
	return wait(cmd)
}

// wait waits for the started app and returns its exit code.
func wait(cmd *exec.Cmd) (int, error) {
	signal.Ignore(os.Interrupt) // ctrl+c belongs to the app while it runs
	defer signal.Reset(os.Interrupt)
	err := cmd.Wait()
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode(), nil
	}
	return 0, err
}

// unlock opens the vault: Touch ID (or the Mac password) with the Secure
// Enclave helper, a passphrase otherwise. Creating it is the app's job.
func unlock() (*vault.Vault, error) {
	dir := paths.Dir()
	if !vault.Exists(dir) {
		return create(dir)
	}
	if vault.Kind(dir) == vault.KindEnclave {
		h := vault.FindHelper()
		if h == nil {
			return nil, errors.New("tuon-keyhelper is missing, so the Touch ID lock can't open: run ./install.sh again")
		}
		fmt.Println("  Unlock tuon with Touch ID or your Mac password…")
		v, err := vault.UnlockEnclave(dir, h)
		if errors.Is(err, vault.ErrNotConfirmed) {
			return nil, errors.New("not unlocked")
		}
		return v, err
	}
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return nil, errors.New("tuon is locked: run it in a terminal to type your passphrase")
	}
	for try := 1; try <= 5; try++ {
		fmt.Print("  tuon passphrase: ")
		p, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Println()
		if err != nil {
			return nil, err
		}
		v, err := vault.UnlockPassphrase(dir, strings.TrimRight(string(p), "\r\n"))
		clear(p)
		if err == nil {
			return v, nil
		}
		if try < 5 {
			wait := time.Duration(1<<(try-1)) * time.Second
			fmt.Printf("  Wrong passphrase. Try again in %s.\n", wait)
			time.Sleep(wait)
		}
	}
	return nil, errors.New("too many wrong passphrases")
}

// create sets up the vault on first run (here, so the app gets the key
// and a browser login in between doesn't ask for Touch ID twice): Touch ID
// with the Secure Enclave helper, a passphrase otherwise.
func create(dir string) (*vault.Vault, error) {
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return nil, errors.New("tuon needs to set up its lock: run `tuon` in a terminal")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	if h := vault.FindHelper(); h != nil {
		fmt.Print(`
  tuon now locks your chats. Your session, messages and keys are encrypted
  with a key kept in this Mac's Secure Enclave, and opening tuon asks for
  Touch ID or your Mac password. Other apps can't read them anymore.
`)
		v, err := vault.CreateEnclave(dir, h)
		if err == nil {
			fmt.Println("  Lock set up.")
			return v, nil
		}
		fmt.Fprintln(os.Stderr, "  Touch ID lock unavailable:", err, "- using a passphrase instead.")
	}
	fmt.Print(`
  Choose a passphrase for tuon. It encrypts your session, messages and keys
  on this computer, and you'll type it when tuon opens. If you forget it,
  you log in again and restore your history with your Messenger PIN.
`)
	for {
		fmt.Print("  passphrase (8+ characters): ")
		p1, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Println()
		if err != nil {
			return nil, err
		}
		fmt.Print("  again: ")
		p2, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Println()
		if err != nil {
			return nil, err
		}
		if string(p1) != string(p2) {
			fmt.Println("  They don't match. Try again.")
			continue
		}
		v, err := vault.CreatePassphrase(dir, string(p1))
		clear(p1)
		clear(p2)
		if err != nil {
			fmt.Println(" ", err)
			continue
		}
		return v, nil
	}
}

// browserLogin runs the login window here, outside the sandbox, and
// returns the session as the app reads it (auth.Creds' JSON).
func browserLogin() ([]byte, error) {
	browser := browserlogin.FindBrowser()
	if browser == "" {
		return nil, errors.New("no Chrome-type browser found: run `tuon login` and choose another way")
	}
	base := paths.WorkBase() // a folder the sandboxed app can't write
	if err := os.MkdirAll(base, 0o700); err != nil {
		return nil, err
	}
	if _, err := os.Stat(filepath.Join(base, browserlogin.ProfileName)); os.IsNotExist(err) {
		fmt.Print(`
  This login window keeps its own browser profile (in ~/Library/Caches/tuon),
  separate from your usual browser. Facebook then knows this device next
  time, and anything you add to it stays: if you use a password manager,
  install its browser extension in this window once. tuon logout deletes it.
`)
	}
	fmt.Print(`
  A browser window is opening at Facebook's login page (Messenger uses
  your Facebook account). Check the address bar shows
  https://www.facebook.com with a padlock, then log in like you normally
  would. It's tuon's own window: your usual browser and its logins aren't
  touched. When Messenger opens there, tuon picks up your session and
  closes the window.

  Waiting for you to log in…
`)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	cookies, err := browserlogin.Login(ctx, browser, base)
	if err != nil {
		return nil, err
	}
	return json.Marshal(struct {
		Platform string            `json:"platform"`
		Cookies  map[string]string `json:"cookies"`
	}{browserlogin.Platform, cookies})
}

var stdin = bufio.NewReader(os.Stdin)

func readLine() string {
	s, err := stdin.ReadString('\n')
	if err != nil && err != io.EOF {
		return ""
	}
	return strings.TrimRight(s, "\r\n")
}
