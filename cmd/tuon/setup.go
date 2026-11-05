package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/rs/zerolog"
	"golang.org/x/term"

	"github.com/ralph/tuon/internal/auth"
	"github.com/ralph/tuon/internal/connector/messenger"
	"github.com/ralph/tuon/internal/store"
)

const usage = `tuon: calm, chat-only Messenger

  tuon                 open (run it inside Ghostty for real photos)
  tuon setup           log in and restore older encrypted chats (run by install.sh)
  tuon login           log in again (when Messenger logs you out)
  tuon history         restore older encrypted chats with your Messenger PIN
  tuon update          pull the latest tuon and rebuild this program
  tuon logout          forget your login on this computer
  tuon logout --wipe   also delete all of tuon's data on this computer
  tuon --help          this help
`

var stdin = bufio.NewReader(os.Stdin)

// loadLogin returns the saved login, asking you to log in if there's none.
func loadLogin() (auth.Creds, error) {
	if c := strings.TrimSpace(os.Getenv("TUON_COOKIES")); c != "" {
		return auth.Creds{Platform: "messenger", Cookies: auth.ParseCookieLine(c)}, nil
	}
	if c, err := auth.Load(store.Dir()); err == nil {
		return c, nil
	}
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return auth.Creds{}, fmt.Errorf("not logged in: run `tuon login` first")
	}
	if err := setup(); err != nil { // first run: log in, then offer history
		return auth.Creds{}, err
	}
	return auth.Load(store.Dir())
}

// login asks how to log in, then saves the session (never the password).
func login() error {
	// an open tuon saves its own session when it quits, which would
	// overwrite this login: refuse while it runs
	release, err := store.Lock()
	if err != nil {
		return err
	}
	defer release()
	browser := auth.FindBrowser()
	fmt.Print("\n  tuon · log in to Messenger\n\n")
	def := "2"
	if browser != "" {
		def = "1"
		fmt.Print("    1  In a browser window              (recommended)\n")
	}
	fmt.Print("    2  Email or phone, and password\n")
	fmt.Print("    3  Paste cookies from your browser\n\n")
	fmt.Printf("  choose [%s]: ", def)
	choice := strings.TrimSpace(readLine())
	if choice == "" {
		choice = def
	}
	var creds auth.Creds
	switch {
	case choice == "1" && browser != "":
		creds, err = browserLogin(browser)
	case choice == "3":
		creds, err = cookieLogin()
	default:
		creds, err = passwordLogin()
	}
	if err != nil {
		return err
	}
	if err := auth.Save(store.Dir(), creds); err != nil {
		return err
	}
	if auth.Where() == "keychain" {
		fmt.Println("  Logged in. Your session is saved in your system keychain.")
	} else {
		fmt.Println("  Logged in. Your session is saved in a file only you can read.")
	}
	return nil
}

func browserLogin(browser string) (auth.Creds, error) {
	fmt.Print(`
  A browser window is opening at Facebook's login page (Messenger uses
  your Facebook account). Check the address bar shows
  https://www.facebook.com with a padlock, then log in like you normally
  would. It's a separate, temporary window: your usual browser and its
  logins aren't touched. When you're in, tuon picks up your session and
  closes the window.

  Waiting for you to log in…
`)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	return auth.BrowserLogin(ctx, browser)
}

func passwordLogin() (auth.Creds, error) {
	fmt.Print(`
  Use the email or phone and password you use for Messenger.
  Your password goes only to Meta and isn't saved.
`)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	ask := func(f messenger.Field) (string, error) {
		if f.Description != "" && !strings.EqualFold(f.Description, f.Name) {
			fmt.Printf("  %s\n", f.Description)
		}
		fmt.Printf("  %s: ", f.Name)
		if f.Secret {
			b, err := term.ReadPassword(int(os.Stdin.Fd()))
			fmt.Println()
			return string(b), err
		}
		return strings.TrimSpace(readLine()), nil
	}
	say := func(s string) { fmt.Printf("\n  %s\n", s) }
	return messenger.PasswordLogin(ctx, zerolog.Nop(), ask, say)
}

func cookieLogin() (auth.Creds, error) {
	fmt.Print(`
  1. Open messenger.com in your browser and log in.
  2. Open its cookies:
       Safari: Develop > Show Web Inspector > Storage > Cookies
       Chrome: DevTools > Application > Cookies
  3. Paste one line with these values:  datr=…; c_user=…; xs=…

  (Your paste stays hidden.)
  cookies: `)
	var line string
	if term.IsTerminal(int(os.Stdin.Fd())) {
		b, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Println()
		if err != nil {
			return auth.Creds{}, err
		}
		line = string(b)
	} else {
		line = readLine()
	}
	c := auth.Creds{Platform: "messenger", Cookies: auth.ParseCookieLine(line)}
	var missing []string
	for _, name := range []string{"datr", "c_user", "xs"} {
		if c.Cookies[name] == "" {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		return auth.Creds{}, fmt.Errorf("that line is missing %s. Run `tuon login` and try again", strings.Join(missing, ", "))
	}
	return c, nil
}

// logout forgets the login; with wipe, all local data too.
func logout(wipe bool) error {
	release, err := store.Lock() // not while tuon is open
	if err != nil {
		return err
	}
	defer release()
	if err := auth.Delete(store.Dir()); err != nil {
		return err
	}
	fmt.Println("  Logged out: your session is gone from this computer.")
	if !wipe {
		fmt.Println("  Your chats, folders and encryption keys are still here. `tuon logout --wipe` deletes them too.")
		return nil
	}
	fmt.Printf("  Delete everything in %s (chats, folders, encryption keys)? [y/N] ", store.Dir())
	if a := strings.ToLower(strings.TrimSpace(readLine())); a != "y" && a != "yes" {
		fmt.Println("  Kept your data.")
		return nil
	}
	cleanTemp()
	if err := os.RemoveAll(store.Dir()); err != nil {
		return err
	}
	fmt.Println("  Deleted.")
	return nil
}

func readLine() string {
	s, err := stdin.ReadString('\n')
	if err != nil && err != io.EOF {
		return ""
	}
	return strings.TrimRight(s, "\r\n")
}

// privateFiles: tuon's folder and files are readable only by you, even
// ones created before this version (or by SQLite with default permissions).
func privateFiles() {
	setUmask()
	dir := store.Dir()
	_ = os.MkdirAll(dir, 0o700)
	_ = filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			_ = os.Chmod(p, 0o700)
		} else {
			_ = os.Chmod(p, 0o600)
		}
		return nil
	})
}

// cleanTemp deletes photos written for the system viewer.
func cleanTemp() { _ = os.RemoveAll(filepath.Join(os.TempDir(), "tuon")) }

// trimPhotoCache deletes cached photos not used for two weeks, and the
// oldest ones if the cache passes 100 MB.
func trimPhotoCache(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	type f struct {
		path string
		mod  time.Time
		size int64
	}
	var files []f
	var total int64
	for _, e := range entries {
		info, err := e.Info()
		if err != nil || e.IsDir() {
			continue
		}
		p := filepath.Join(dir, e.Name())
		if time.Since(info.ModTime()) > 14*24*time.Hour {
			_ = os.Remove(p)
			continue
		}
		files = append(files, f{p, info.ModTime(), info.Size()})
		total += info.Size()
	}
	sort.Slice(files, func(i, j int) bool { return files[i].mod.Before(files[j].mod) })
	for _, x := range files {
		if total <= 100<<20 {
			break
		}
		_ = os.Remove(x.path)
		total -= x.size
	}
}

// trimLog keeps the log from growing forever: past 5 MB, start fresh.
func trimLog(path string) {
	if st, err := os.Stat(path); err == nil && st.Size() > 5<<20 {
		_ = os.Remove(path)
	}
}
