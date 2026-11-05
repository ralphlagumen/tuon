package auth

import (
	"context"
	"os"
	"os/exec"
	"testing"
	"time"
)

// TestBrowserWindowMechanics starts the login browser invisibly, connects
// to it and reads messenger.com's cookies (no login involved).
//
//	TUON_BROWSER_TEST=1 go test ./internal/auth -run TestBrowserWindowMechanics -v
func TestBrowserWindowMechanics(t *testing.T) {
	if os.Getenv("TUON_BROWSER_TEST") == "" {
		t.Skip("set TUON_BROWSER_TEST=1")
	}
	browser := FindBrowser()
	if browser == "" {
		t.Skip("no Chrome-type browser")
	}
	profile, _ := os.MkdirTemp("", "tuon-test-")
	cmd := exec.Command(browser, "--headless=new", "--user-data-dir="+profile,
		"--remote-debugging-port=0", "--remote-debugging-address=127.0.0.1",
		"--no-first-run", loginURL)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan struct{})
	go func() { _ = cmd.Wait(); close(exited) }()
	defer func() {
		_ = cmd.Process.Kill()
		<-exited
		_ = os.RemoveAll(profile)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	ws, err := devtools(ctx, profile, exited)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.CloseNow()
	var names []string
	for i := 1; i <= 20 && len(names) == 0; i++ {
		time.Sleep(time.Second)
		c, err := allCookies(ctx, ws, i)
		if err != nil {
			t.Fatal(err)
		}
		for k := range c {
			names = append(names, k)
		}
		if (Creds{Cookies: c}).Valid() {
			t.Fatal("a fresh profile must not be logged in")
		}
	}
	if len(names) == 0 {
		t.Fatal("read no facebook.com cookies")
	}
	t.Logf("read facebook.com cookies from the window: %v (not logged in, as expected)", names)
}
