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
	"net/url"
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

// Messenger on facebook.com: the same account, and steadier than
// messenger.com's own web login (which often fails with "Your request
// couldn't be processed"). Logged out, Facebook shows its login page and
// comes back here; still logged in (the kept profile), it opens straight
// away. Opening /login/ instead made Facebook ask for the password again
// even with a valid session.
const loginURL = "https://www.facebook.com/messages/"

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
func removeStaleProfiles(base string) {
	old, _ := filepath.Glob(filepath.Join(base, "tuon-login-*"))
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

// ProfileName is the login window's own Chrome profile, kept in the folder
// passed to Login. Keeping it means Facebook knows this device (no new
// verification on every login) and a password manager extension you add
// there stays.
const ProfileName = "browser"

// Login opens the login window and waits until you've logged in and
// Messenger has opened, then returns the window's facebook.com cookies.
// The window uses its own profile in base/ProfileName, kept between logins;
// the launcher passes a folder the sandboxed app can't write.
func Login(ctx context.Context, browser, base string) (map[string]string, error) {
	removeStaleProfiles(base)
	removeStaleProfiles(os.TempDir()) // where older versions put throwaway profiles
	profile := filepath.Join(base, ProfileName)
	if err := os.MkdirAll(profile, 0o700); err != nil {
		return nil, err
	}
	cmd, dt, err := startBrowser(browser, profile,
		"--window-size=760,860",
		// a normal window with the address bar, so you can see you're on
		// https://www.facebook.com before typing anything
		"--new-window", loginURL)
	if err != nil {
		return nil, fmt.Errorf("couldn't open the login window: %w", err)
	}
	exited := make(chan struct{})
	go func() { _ = cmd.Wait(); close(exited) }()
	id := 0
	next := func() int { id++; return id }
	defer func() {
		// close the window the normal way so the profile saves its state,
		// then make sure it's gone
		_ = dt.send(next(), "Browser.close")
		select {
		case <-exited:
		case <-time.After(5 * time.Second):
			if cmd.Process != nil {
				_ = cmd.Process.Kill()
			}
			<-exited
		}
		dt.close()
	}()

	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil, errors.New("login timed out")
		case <-exited:
			return nil, errors.New("the login window was closed before you logged in")
		case <-tick.C:
		}
		// only once the window shows Messenger: before that, a kept profile
		// may still hold an old session's cookies, and a 2-factor step
		// may not be done yet
		urls, err := dt.pageURLs(ctx, next())
		if err != nil || !anyLoggedIn(urls) {
			continue
		}
		c, err := dt.cookies(ctx, next())
		if err != nil || !valid(c) {
			continue
		}
		// give Messenger a moment to set its remaining cookies
		time.Sleep(1500 * time.Millisecond)
		if more, err := dt.cookies(ctx, next()); err == nil && valid(more) {
			c = more
		}
		return c, nil
	}
}

// anyLoggedIn: some tab shows Messenger on facebook.com, which Facebook
// only does for a logged-in session (login, checkpoints and 2-factor steps
// are other pages).
func anyLoggedIn(urls []string) bool {
	for _, raw := range urls {
		u, err := url.Parse(raw)
		if err != nil || u.Scheme != "https" || !isFacebookDomain(u.Hostname()) {
			continue
		}
		if u.Path == "/messages" || strings.HasPrefix(u.Path, "/messages/") {
			return true
		}
	}
	return false
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

// pageURLs lists the addresses of the window's tabs.
func (d *devPipe) pageURLs(ctx context.Context, id int) ([]string, error) {
	b, err := d.call(ctx, id, "Target.getTargets")
	if err != nil {
		return nil, err
	}
	var resp struct {
		Result struct {
			TargetInfos []struct{ Type, URL string } `json:"targetInfos"`
		} `json:"result"`
	}
	if err := json.Unmarshal(b, &resp); err != nil {
		return nil, err
	}
	var out []string
	for _, t := range resp.Result.TargetInfos {
		if t.Type == "page" {
			out = append(out, t.URL)
		}
	}
	return out, nil
}

// cookies asks the window for its facebook.com cookies.
func (d *devPipe) cookies(ctx context.Context, id int) (map[string]string, error) {
	b, err := d.call(ctx, id, "Storage.getCookies")
	if err != nil {
		return nil, err
	}
	var resp struct {
		Result struct {
			Cookies []struct {
				Name, Value, Domain string
			} `json:"cookies"`
		} `json:"result"`
	}
	if err := json.Unmarshal(b, &resp); err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, c := range resp.Result.Cookies {
		if isFacebookDomain(c.Domain) {
			out[c.Name] = c.Value
		}
	}
	return out, nil
}

// call sends one DevTools command and returns its reply.
func (d *devPipe) call(ctx context.Context, id int, method string) ([]byte, error) {
	if err := d.send(id, method); err != nil {
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
			ID int `json:"id"`
		}
		if json.Unmarshal(b, &resp) != nil || resp.ID != id {
			continue // events or other replies
		}
		return b, nil
	}
}

// isFacebookDomain: facebook.com or a subdomain of it (".facebook.com",
// "www.facebook.com"), never a look-alike such as "evilfacebook.com".
func isFacebookDomain(d string) bool {
	d = strings.TrimPrefix(strings.ToLower(d), ".")
	return d == "facebook.com" || strings.HasSuffix(d, ".facebook.com")
}
