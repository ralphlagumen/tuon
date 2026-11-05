package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/term"

	"github.com/ralph/tuon/internal/store"
)

func cookiePath() string { return filepath.Join(store.Dir(), "cookies.txt") }

// loadCookies returns the saved cookie line, asking for it on first run.
func loadCookies() (string, error) {
	if c := strings.TrimSpace(os.Getenv("TUON_COOKIES")); c != "" {
		return c, nil
	}
	if b, err := os.ReadFile(cookiePath()); err == nil && strings.TrimSpace(string(b)) != "" {
		return strings.TrimSpace(string(b)), nil
	}
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return "", fmt.Errorf("not logged in: run `tuon login` first")
	}
	return login()
}

// login asks for the messenger.com cookie line and saves it (owner-only).
func login() (string, error) {
	fmt.Print(`
  tuon · log in to Messenger

  1. Open messenger.com in your browser and log in.
  2. Open the cookies for messenger.com:
       Safari: Develop > Show Web Inspector > Storage > Cookies
       Chrome: DevTools > Application > Cookies
  3. Paste one line with these three values, then press enter:

       datr=…; c_user=…; xs=…

  (Your paste stays hidden. It's saved only on this Mac, readable only by you.)

  cookies: `)
	var line string
	if term.IsTerminal(int(os.Stdin.Fd())) {
		b, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Println()
		if err != nil {
			return "", err
		}
		line = string(b)
	} else {
		l, err := bufio.NewReader(os.Stdin).ReadString('\n')
		if err != nil && l == "" {
			return "", err
		}
		line = l
	}
	line = strings.TrimSpace(line)
	var missing []string
	for _, name := range []string{"datr", "c_user", "xs"} {
		if !strings.Contains(line, name+"=") {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		return "", fmt.Errorf("that line is missing %s. Run `tuon login` and paste again", strings.Join(missing, ", "))
	}
	if err := os.MkdirAll(store.Dir(), 0o700); err != nil {
		return "", err
	}
	if err := os.WriteFile(cookiePath(), []byte(line+"\n"), 0o600); err != nil {
		return "", err
	}
	fmt.Println("  Saved.")
	return line, nil
}

const usage = `tuon: calm, chat-only Messenger

  tuon           open in this terminal (run it inside Ghostty for real photos)
  tuon login     paste new cookies (when Messenger logs you out)
  tuon --help    this help
`
