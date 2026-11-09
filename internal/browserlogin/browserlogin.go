// Package browserlogin logs in to Messenger in a throwaway Chrome-type
// window and hands back the session cookies. It has no dependencies, so
// tuon's small launcher (outside the sandbox, where a browser can run) can
// use it without linking Messenger or database code.
package browserlogin

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Browser login: open facebook.com's login in its own Chrome-type window (a fresh,
// throwaway profile, so your normal browser isn't touched), let you log in
// there like on the website, and pick up the session once you're in.
// Tuon talks to that window over a private pipe (no network port), which
// closes with the window.

// Facebook's login page: the same account as Messenger, and steadier than
// messenger.com's own web login (which often fails with "Your request
// couldn't be processed").
const loginURL = "https://www.facebook.com/login/?next=https%3A%2F%2Fwww.facebook.com%2Fmessages%2F"

// FindBrowser returns a Chrome-type browser that can run the login window.
func FindBrowser() string {
	var paths []string
	switch runtime.GOOS {
	case "darwin":
		for _, app := range []string{"Google Chrome", "Microsoft Edge", "Brave Browser", "Chromium", "Arc"} {
			paths = append(paths, "/Applications/"+app+".app/Contents/MacOS/"+app)
		}
	case "windows":
		for _, base := range []string{os.Getenv("ProgramFiles"), os.Getenv("ProgramFiles(x86)"), os.Getenv("LocalAppData")} {
			if base == "" {
				continue
			}
			paths = append(paths,
				filepath.Join(base, `Google\Chrome\Application\chrome.exe`),
				filepath.Join(base, `Microsoft\Edge\Application\msedge.exe`),
				filepath.Join(base, `BraveSoftware\Brave-Browser\Application\brave.exe`))
		}
	default:
		for _, name := range []string{"google-chrome", "google-chrome-stable", "chromium", "chromium-browser", "microsoft-edge", "brave-browser"} {
			if p, err := exec.LookPath(name); err == nil {
				paths = append(paths, p)
			}
		}
	}
	for _, p := range paths {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	return ""
}

// removeStaleProfiles deletes login profiles an interrupted login left
// behind (tuon killed before it could clean up). Only one login runs at a
// time, so anything older than a few minutes is stale.
func removeStaleProfiles() {
	old, _ := filepath.Glob(filepath.Join(os.TempDir(), "tuon-login-*"))
	for _, p := range old {
		if st, err := os.Stat(p); err == nil && time.Since(st.ModTime()) > 10*time.Minute {
			_ = os.RemoveAll(p)
		}
	}
}

// Platform is the login flavor these cookies belong to (auth.Creds.Platform).
const Platform = "facebook"

// valid: the cookies of a logged-in session.
func valid(c map[string]string) bool { return c["c_user"] != "" && c["xs"] != "" }

// Login opens the login window and waits until you've logged in, then
// returns the window's facebook.com cookies.
func Login(ctx context.Context, browser string) (map[string]string, error) {
	removeStaleProfiles()
	profile, err := os.MkdirTemp("", "tuon-login-")
	if err != nil {
		return nil, err
	}
	cmd, dt, err := startBrowser(browser, profile,
		"--window-size=760,860",
		// a normal window with the address bar, so you can see you're on
		// https://www.facebook.com before typing anything
		"--new-window", loginURL)
	if err != nil {
		_ = os.RemoveAll(profile)
		return nil, fmt.Errorf("couldn't open the login window: %w", err)
	}
	exited := make(chan struct{})
	go func() { _ = cmd.Wait(); close(exited) }()
	defer func() {
		// close the window, wait for it to let go of its files, then delete
		// the throwaway profile (and its cookies)
		dt.close()
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		select {
		case <-exited:
		case <-time.After(5 * time.Second):
		}
		_ = os.RemoveAll(profile)
	}()

	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for id := 1; ; id += 3 {
		select {
		case <-ctx.Done():
			return nil, errors.New("login timed out")
		case <-exited:
			return nil, errors.New("the login window was closed before you logged in")
		case <-tick.C:
		}
		cookies, err := dt.cookies(ctx, id)
		if err != nil {
			continue // the window may be busy loading; try again
		}
		if valid(cookies) {
			// give Messenger a moment to set its remaining cookies
			time.Sleep(1500 * time.Millisecond)
			if more, err := dt.cookies(ctx, id+1); err == nil && valid(more) {
				cookies = more
			}
			_ = dt.send(id+2, "Browser.close")
			return cookies, nil
		}
	}
}

// startBrowser starts a Chrome-type browser on a throwaway profile, talking
// DevTools over a private pipe (--remote-debugging-pipe): there is no
// network port, so no other program on this computer can connect to the
// window and read its cookies.
func startBrowser(browser, profile string, extra ...string) (*exec.Cmd, *devPipe, error) {
	toChrome, ourEnd, err := os.Pipe() // Chrome reads commands on its fd 3
	if err != nil {
		return nil, nil, err
	}
	fromChrome, chromeEnd, err := os.Pipe() // and writes replies on fd 4
	if err != nil {
		return nil, nil, err
	}
	args := append([]string{
		"--user-data-dir=" + profile,
		"--remote-debugging-pipe",
		"--no-first-run", "--no-default-browser-check", "--disable-sync",
	}, extra...)
	cmd := exec.Command(browser, args...)
	cmd.ExtraFiles = []*os.File{toChrome, chromeEnd}
	if err := cmd.Start(); err != nil {
		return nil, nil, err
	}
	toChrome.Close() // Chrome's copies stay open in Chrome
	chromeEnd.Close()
	dt := &devPipe{w: ourEnd, msgs: make(chan []byte, 16)}
	go dt.readLoop(fromChrome)
	return cmd, dt, nil
}

// devPipe speaks the DevTools protocol over the pipe: JSON messages, each
// ended by a NUL byte.
type devPipe struct {
	w    *os.File
	msgs chan []byte
}

func (d *devPipe) readLoop(r *os.File) {
	defer close(d.msgs)
	defer r.Close()
	br := bufio.NewReaderSize(r, 1<<20)
	for {
		msg, err := br.ReadBytes(0)
		if err != nil {
			return
		}
		d.msgs <- msg[:len(msg)-1]
	}
}

func (d *devPipe) close() { _ = d.w.Close() }

func (d *devPipe) send(id int, method string) error {
	b, _ := json.Marshal(map[string]any{"id": id, "method": method})
	_, err := d.w.Write(append(b, 0))
	return err
}

// cookies asks the window for its facebook.com cookies.
func (d *devPipe) cookies(ctx context.Context, id int) (map[string]string, error) {
	if err := d.send(id, "Storage.getCookies"); err != nil {
		return nil, err
	}
	timeout := time.After(5 * time.Second)
	for {
		var b []byte
		var ok bool
		select {
		case b, ok = <-d.msgs:
			if !ok {
				return nil, errors.New("the login window closed")
			}
		case <-timeout:
			return nil, errors.New("the login window didn't answer")
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		var resp struct {
			ID     int `json:"id"`
			Result struct {
				Cookies []struct {
					Name, Value, Domain string
				} `json:"cookies"`
			} `json:"result"`
		}
		if json.Unmarshal(b, &resp) != nil || resp.ID != id {
			continue // events or other replies
		}
		out := map[string]string{}
		for _, c := range resp.Result.Cookies {
			if isFacebookDomain(c.Domain) {
				out[c.Name] = c.Value
			}
		}
		return out, nil
	}
}

// isFacebookDomain: facebook.com or a subdomain of it (".facebook.com",
// "www.facebook.com"), never a look-alike such as "evilfacebook.com".
func isFacebookDomain(d string) bool {
	d = strings.TrimPrefix(strings.ToLower(d), ".")
	return d == "facebook.com" || strings.HasSuffix(d, ".facebook.com")
}
