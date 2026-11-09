// Package sandbox limits what tuon's own process can touch, so a bad
// dependency (or a bug) can't read your other secrets or plant files.
//
// On macOS tuon re-launches itself under sandbox-exec with a profile that:
//   - refuses reads of credential stores: SSH and cloud keys, password
//     managers, browser profiles and cookies, keychains, shell history,
//     other apps' messages and mail;
//   - refuses writes anywhere except tuon's folder, Downloads (saved
//     files) and your temp folder — so nothing can replace the tuon
//     binary or add itself to your shell's startup files;
//   - refuses to start any program but the few tuon uses (sound,
//     clipboard, opening files and links, its Touch ID helper), and
//     refuses to script other apps.
//
// The sandboxed program is tuon-app. tuon itself is a small launcher with
// none of the Messenger, encryption or database code: it unlocks, starts
// tuon-app here, runs the login browser when tuon-app asks (ExitBrowserLogin)
// and installs updates. So no dependency of the chat app ever runs outside.
//
// Network stays open (Messenger needs it). Elsewhere this is a no-op for
// now. TUON_NO_SANDBOX=1 turns it off for troubleshooting.
package sandbox

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const envMark = "TUON_SANDBOXED"

// How the launcher and the sandboxed app talk.
const (
	// ExitBrowserLogin: the app wants a browser login, which can't run in
	// the sandbox (the browser needs the keychain). The launcher runs it
	// and starts the app again with the cookies on LoginFDEnv.
	ExitBrowserLogin = 43
	// KeyFDEnv names the pipe (fd 3) with the unlocked vault's key.
	KeyFDEnv = "TUON_KEY_FD"
	// LoginFDEnv names the pipe (fd 4) with a browser login's cookies.
	LoginFDEnv = "TUON_LOGIN_FD"
	// NoteEnv: why the app runs without the sandbox, for its header.
	NoteEnv = "TUON_SANDBOX_NOTE"
)

// denyRead: paths under your home that tuon never needs to read.
var denyRead = []string{
	// keys, tokens and credentials
	".ssh", ".gnupg", ".aws", ".azure", ".kube", ".docker", ".config/gh", ".config/gcloud",
	".config/op", ".password-store", ".netrc", ".npmrc", ".pypirc", ".git-credentials",
	".cargo/credentials", ".cargo/credentials.toml", ".vault-token", ".terraform.d", ".config/hub",
	".config/rclone", ".gem/credentials", ".m2/settings.xml", ".gradle/gradle.properties",
	".claude", ".claude.json", ".config/claude", ".codex", ".config/github-copilot",
	// histories
	".zsh_history", ".bash_history", ".local/share/fish", ".zsh_sessions", ".python_history",
	".node_repl_history", ".psql_history", ".mysql_history", ".sqlite_history", ".lesshst", ".viminfo",
	// keychains, mail, messages, other apps' private data
	"Library/Keychains", "Library/Cookies", "Library/Safari", "Library/Messages", "Library/Mail",
	"Library/Containers", "Library/Group Containers", "Library/Accounts", "Library/Calendars",
	"Library/Application Support/AddressBook", "Library/Application Support/com.apple.TCC",
	// browsers
	"Library/Application Support/Google", "Library/Application Support/Chromium",
	"Library/Application Support/BraveSoftware", "Library/Application Support/Microsoft Edge",
	"Library/Application Support/Arc", "Library/Application Support/Firefox",
	"Library/Application Support/Vivaldi", "Library/Application Support/com.operasoftware.Opera",
	// password managers and other chat apps
	"Library/Application Support/1Password", "Library/Application Support/Bitwarden",
	"Library/Application Support/Signal", "Library/Application Support/Slack",
	"Library/Application Support/discord", "Library/Application Support/Telegram Desktop",
	// the launcher's own folder: the login window's kept browser profile, updates
	"Library/Caches/tuon",
	"Library/Application Support/WhatsApp", "Library/Application Support/Caprine",
}

// allowExec: the only programs the sandboxed app may start, besides tuon
// itself and its Touch ID helper: sound, opening files and links, the
// clipboard, and the FileVault check.
var allowExec = []string{
	"/usr/bin/afplay", "/usr/bin/open", "/usr/bin/pbcopy", "/usr/bin/pbpaste",
	"/usr/bin/osascript", "/usr/bin/fdesetup",
}

// Active reports whether this process already runs sandboxed.
func Active() bool { return os.Getenv(envMark) == "1" }

// Disabled reports whether the user turned the sandbox off.
func Disabled() bool { return os.Getenv("TUON_NO_SANDBOX") != "" }

// Profile is the macOS sandbox profile for a home folder, temp folder and
// the chat app (exe, tuon-app, with the Touch ID helper next to it).
func Profile(home, tmp, exe string) string {
	q := func(p string) string { return fmt.Sprintf("%q", p) } // SBPL strings use Go-compatible escapes
	var b strings.Builder
	b.WriteString("(version 1)\n(allow default)\n")
	b.WriteString("(deny file-read* file-write*\n")
	seen := map[string]bool{}
	for _, p := range denyRead {
		// both the path and where it really points: a symlinked folder
		// would otherwise slip past
		for _, v := range []string{filepath.Join(home, p), realDir(filepath.Join(home, p))} {
			if !seen[v] {
				seen[v] = true
				b.WriteString("  (subpath " + q(v) + ")\n")
			}
		}
	}
	b.WriteString(")\n")
	// writes: tuon's folder, Downloads, this user's temp folders, terminals
	b.WriteString("(deny file-write* (require-not (require-any\n")
	for _, p := range []string{filepath.Join(home, ".config", "tuon"), filepath.Join(home, "Downloads"), filepath.Dir(tmp)} {
		b.WriteString("  (subpath " + q(p) + ")\n")
	}
	b.WriteString(`  (regex #"^/dev/(null|zero|tty|ttys[0-9]+|dtracehelper|random|urandom)$")` + "\n")
	b.WriteString(")))\n")
	// programs: only the listed ones
	b.WriteString("(deny process-exec*)\n(allow process-exec*\n")
	exes := append([]string{exe, realDir(exe), filepath.Join(filepath.Dir(realDir(exe)), "tuon-keyhelper")}, allowExec...)
	for _, p := range exes {
		b.WriteString("  (literal " + q(p) + ")\n")
	}
	b.WriteString(")\n")
	// no scripting other apps (Terminal, Finder, System Events…)
	b.WriteString("(deny appleevent-send)\n")
	return b.String()
}

// realDir resolves symlinks (/var -> /private/var), since the sandbox
// matches real paths.
func realDir(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return p
}
