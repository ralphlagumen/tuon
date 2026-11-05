package auth

import (
	"context"
	"os"
	"path/filepath"
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
	cmd, dt, err := startBrowser(browser, profile, "--headless=new", loginURL)
	if err != nil {
		t.Fatal(err)
	}
	exited := make(chan struct{})
	go func() { _ = cmd.Wait(); close(exited) }()
	defer func() {
		dt.close()
		_ = cmd.Process.Kill()
		<-exited
		_ = os.RemoveAll(profile)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	var names []string
	for i := 1; i <= 20 && len(names) == 0; i++ {
		time.Sleep(time.Second)
		c, err := dt.cookies(ctx, i)
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

func TestRemoveStaleProfiles(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	old, fresh := filepath.Join(os.TempDir(), "tuon-login-1"), filepath.Join(os.TempDir(), "tuon-login-2")
	other := filepath.Join(os.TempDir(), "something-else")
	for _, d := range []string{old, fresh, other} {
		if err := os.MkdirAll(filepath.Join(d, "Default"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	past := time.Now().Add(-time.Hour)
	_ = os.Chtimes(old, past, past)
	_ = os.Chtimes(other, past, past)
	removeStaleProfiles()
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Error("stale profile kept")
	}
	for _, d := range []string{fresh, other} {
		if _, err := os.Stat(d); err != nil {
			t.Errorf("%s removed", d)
		}
	}
}
