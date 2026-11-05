package auth

import (
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

	"github.com/coder/websocket"
)

// Browser login: open facebook.com's login in its own Chrome-type window (a fresh,
// throwaway profile, so your normal browser isn't touched), let you log in
// there like on the website, and pick up the session once you're in.
// Tuon talks to that window over Chrome's local debugging port, which
// listens only on this computer and closes with the window.

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

// BrowserLogin opens the login window and waits until you've logged in.
func BrowserLogin(ctx context.Context, browser string) (Creds, error) {
	profile, err := os.MkdirTemp("", "tuon-login-")
	if err != nil {
		return Creds{}, err
	}

	cmd := exec.Command(browser,
		"--user-data-dir="+profile,
		"--remote-debugging-port=0", // any free port, local only
		"--remote-debugging-address=127.0.0.1",
		"--no-first-run", "--no-default-browser-check", "--disable-sync",
		"--window-size=760,860",
		// a normal window with the address bar, so you can see you're on
		// https://www.messenger.com before typing anything
		"--new-window", loginURL,
	)
	if err := cmd.Start(); err != nil {
		return Creds{}, fmt.Errorf("couldn't open the login window: %w", err)
	}
	exited := make(chan struct{})
	go func() { _ = cmd.Wait(); close(exited) }()
	defer func() {
		// close the window, wait for it to let go of its files, then delete
		// the throwaway profile (and its cookies)
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		select {
		case <-exited:
		case <-time.After(5 * time.Second):
		}
		_ = os.RemoveAll(profile)
	}()

	ws, err := devtools(ctx, profile, exited)
	if err != nil {
		return Creds{}, err
	}
	defer ws.CloseNow()

	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for id := 1; ; id++ {
		select {
		case <-ctx.Done():
			return Creds{}, errors.New("login timed out")
		case <-exited:
			return Creds{}, errors.New("the login window was closed before you logged in")
		case <-tick.C:
		}
		cookies, err := allCookies(ctx, ws, id)
		if err != nil {
			continue // the window may be busy loading; try again
		}
		if c := (Creds{Platform: "facebook", Cookies: cookies}); c.Valid() {
			// give Messenger a moment to set its remaining cookies
			time.Sleep(1500 * time.Millisecond)
			if more, err := allCookies(ctx, ws, id+1); err == nil && (Creds{Cookies: more}).Valid() {
				c.Cookies = more
			}
			_ = send(ctx, ws, id+2, "Browser.close")
			return c, nil
		}
	}
}

// devtools connects to the window's local debugging port.
func devtools(ctx context.Context, profile string, exited <-chan struct{}) (*websocket.Conn, error) {
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case <-exited:
			return nil, errors.New("the login window closed right away")
		default:
		}
		b, err := os.ReadFile(filepath.Join(profile, "DevToolsActivePort"))
		if lines := strings.Split(strings.TrimSpace(string(b)), "\n"); err == nil && len(lines) == 2 {
			url := "ws://127.0.0.1:" + strings.TrimSpace(lines[0]) + strings.TrimSpace(lines[1])
			dctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			ws, _, err := websocket.Dial(dctx, url, nil)
			cancel()
			if err == nil {
				ws.SetReadLimit(8 << 20)
				return ws, nil
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	return nil, errors.New("couldn't talk to the login window")
}

func send(ctx context.Context, ws *websocket.Conn, id int, method string) error {
	b, _ := json.Marshal(map[string]any{"id": id, "method": method})
	return ws.Write(ctx, websocket.MessageText, b)
}

// allCookies asks the window for its facebook.com cookies.
func allCookies(ctx context.Context, ws *websocket.Conn, id int) (map[string]string, error) {
	if err := send(ctx, ws, id, "Storage.getCookies"); err != nil {
		return nil, err
	}
	rctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	for {
		_, b, err := ws.Read(rctx)
		if err != nil {
			return nil, err
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
			if strings.HasSuffix(c.Domain, "facebook.com") {
				out[c.Name] = c.Value
			}
		}
		return out, nil
	}
}
