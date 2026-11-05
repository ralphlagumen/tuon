package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rs/zerolog"
	"golang.org/x/term"

	"github.com/ralph/tuon/internal/auth"
	"github.com/ralph/tuon/internal/connector/messenger"
	"github.com/ralph/tuon/internal/keystore"
	"github.com/ralph/tuon/internal/sandbox"
	"github.com/ralph/tuon/internal/store"
)

const usage = `tuon: calm, chat-only Messenger

  tuon                 open (run it inside Ghostty for real photos)
  tuon setup           log in and restore older encrypted chats (run by install.sh)
  tuon login           log in again (when Messenger logs you out)
  tuon history         restore older encrypted chats with your Messenger PIN
  tuon update          install the latest signed release
  tuon relink          register tuon again as a new linked device (if a
                       contact's messages keep failing to decrypt)
  tuon logout          forget your login on this computer
  tuon logout --wipe   also delete all of tuon's data on this computer
  tuon --help          this help
`

var stdin = bufio.NewReader(os.Stdin)

// migrateLogin: a session from before the vault (keychain, login.json,
// cookies.txt) is only picked up while moving an old install over, before
// keys.db exists. After that, a file appearing there is ignored.
func migrateLogin() bool { return !keystore.Exists(store.Dir()) }

// loadLogin returns the saved login, asking you to log in if there's none.
func loadLogin() (auth.Creds, error) {
	v, err := openVault() // first, even with TUON_COOKIES: nothing is stored unencrypted
	if err != nil {
		return auth.Creds{}, err
	}
	if c := strings.TrimSpace(os.Getenv("TUON_COOKIES")); c != "" {
		return auth.Creds{Platform: "messenger", Cookies: auth.ParseCookieLine(c)}, nil
	}
	if c, err := auth.LoadSealed(store.Dir(), v, migrateLogin()); err == nil {
		return c, nil
	}
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return auth.Creds{}, fmt.Errorf("not logged in: run `tuon login` first")
	}
	if sandbox.Active() { // logging in needs a browser: the launcher does it
		os.Exit(exitNeedSetup)
	}
	if err := setup(); err != nil { // first run: log in, then offer history
		return auth.Creds{}, err
	}
	return auth.LoadSealed(store.Dir(), v, migrateLogin())
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
	if ok, err := sameAccount(creds); err != nil {
		return err
	} else if !ok {
		return errors.New("login cancelled: kept the other account's data")
	}
	v, err := openVault()
	if err != nil {
		return err
	}
	if err := auth.SaveSealed(store.Dir(), v, creds); err != nil {
		return err
	}
	fmt.Println("  Logged in. Your session is saved encrypted; only your unlock opens it.")
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

// sameAccount checks a new login against the account the local data
// belongs to. A different account would see the old one's chats and run on
// its encrypted-chat device, so its data is cleared first (with a yes).
func sameAccount(creds auth.Creds) (bool, error) {
	if _, err := os.Stat(filepath.Join(store.Dir(), "tuon.db")); err != nil {
		return true, nil // nothing stored yet
	}
	st, err := store.Open()
	if err != nil {
		return false, err
	}
	old := st.Account()
	st.Close()
	if old == "" || old == creds.Cookies["c_user"] {
		return true, nil
	}
	fmt.Println("\n  This is a different Messenger account from the one tuon has data for.")
	fmt.Println("  To switch, tuon clears the other account's chats, folders and encryption keys")
	fmt.Println("  on this computer (nothing on Messenger changes).")
	fmt.Print("  Clear them and switch? [y/N] ")
	if a := strings.ToLower(strings.TrimSpace(readLine())); a != "y" && a != "yes" {
		return false, nil
	}
	for _, name := range []string{"tuon.db", "tuon.db-wal", "tuon.db-shm", "keys.db", "keys.db-wal", "keys.db-shm", "cache"} {
		if err := os.RemoveAll(filepath.Join(store.Dir(), name)); err != nil {
			return false, err
		}
	}
	cleanTemp()
	fmt.Println("  Cleared. Starting fresh for this account.")
	return true, nil
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
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			return nil // chmod follows links: never touch what one points at
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

// removeRetired deletes what older versions kept and nothing uses now: the
// plain PNG photo cache (photos now go to the terminal without touching the
// disk) and `source`, the clone path `tuon update` no longer trusts.
func removeRetired() {
	_ = os.RemoveAll(filepath.Join(store.Dir(), "cache", "img"))
	_ = os.Remove(filepath.Join(store.Dir(), "source"))
}

// trimLog keeps the log from growing forever: past 5 MB, start fresh.
func trimLog(path string) {
	if st, err := os.Stat(path); err == nil && st.Size() > 5<<20 {
		_ = os.Remove(path)
	}
}
