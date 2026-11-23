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
//   - refuses to start any program but the few tuon uses (sound, text
//     clipboard, the FileVault check, its Touch ID helper), and refuses
//     to script other apps; the launcher opens links and photos for it.
//
// The sandboxed program is tuon-app. tuon itself is a small launcher with
// none of the Messenger, encryption or database code: it unlocks, starts
// tuon-app here, runs the login browser when tuon-app asks (ExitBrowserLogin)
// and installs updates. So no dependency of the chat app ever runs outside.
//
// It also gets only the environment variables it needs (AppEnv), so no
// SSH agent socket or tokens, and it can't connect to Unix sockets (the
// SSH agent, Docker), except the one macOS needs for name lookups.
//
// Network: with a gate port (internal/netgate, run by the launcher) the app
// may connect only to that port on localhost, so everything it sends goes
// through the gate, which lets only Meta's servers through. No DNS either:
// a lookup can carry data out in the name. Elsewhere this is a no-op for
// now. TUON_NO_SANDBOX=1 turns it off for troubleshooting.
package sandbox

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
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
	// ParentTTYEnv: the device of the launcher's terminal; the app runs on
	// a terminal of its own (sandbox-test checks they differ).
	ParentTTYEnv = "TUON_PARENT_TTY"
)

// ReadableFolders: the folders in your home the chat app may read, so
// `@~/` attachments complete there. Anything else you attach goes through
// the launcher's file picker (ctrl+f), which hands over only what you pick.
var ReadableFolders = []string{"Downloads", "Desktop", "Documents", "Pictures"}

// denyRead: paths under your home that tuon never needs to read (refused
// even if a folder above them is ever made readable).
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
// itself and its Touch ID helper: sound, copying and pasting text, and the
// FileVault check. Not open or osascript: both can ask macOS to start any
// program, which then runs outside the sandbox (sandbox-test probes it).
// The launcher opens links and photos and reads pasted images for the app
// (internal/opener).
var allowExec = []string{
	"/usr/bin/afplay", "/usr/bin/pbcopy", "/usr/bin/pbpaste", "/usr/bin/fdesetup",
}

// keepEnv and keepPrefix: the environment the chat app gets. Everything
// else (SSH_AUTH_SOCK, tokens, other tools' sockets) stays with the
// launcher, so a bad library in the app can't find or use it. Proxy
// variables pass so the app can say it ignored them (it drops them).
var keepEnv = map[string]bool{
	"HOME": true, "USER": true, "LOGNAME": true, "PATH": true, "TMPDIR": true, "TZ": true, "LANG": true,
	"COLORTERM": true, "COLORFGBG": true, "NO_COLOR": true, "CLICOLOR": true, "CLICOLOR_FORCE": true,
	"VTE_VERSION": true, "__CF_USER_TEXT_ENCODING": true, // pbcopy/pbpaste use it for UTF-8
	"HTTPS_PROXY": true, "https_proxy": true, "HTTP_PROXY": true, "http_proxy": true, "ALL_PROXY": true, "all_proxy": true,
}
var keepPrefix = []string{"TUON_", "LC_", "TERM", "KITTY_", "GHOSTTY_", "WEZTERM_", "ITERM_"}

// AppEnv keeps only the variables in keepEnv / keepPrefix.
func AppEnv(env []string) []string {
	var out []string
	for _, kv := range env {
		k, _, _ := strings.Cut(kv, "=")
		ok := keepEnv[k]
		for _, p := range keepPrefix {
			ok = ok || strings.HasPrefix(k, p)
		}
		if ok {
			out = append(out, kv)
		}
	}
	return out
}

// dnsSocket: macOS name lookups go through this socket; every other Unix
// socket is refused.
const dnsSocket = "/private/var/run/mDNSResponder"

// ClearMark forgets an inherited "already sandboxed" mark: the launcher
// calls it first, so a mark planted in its environment can't make it
// start the app unsandboxed (or make the app think it is sandboxed).
func ClearMark() { os.Unsetenv(envMark) }

// Active reports whether this process already runs sandboxed.
func Active() bool { return os.Getenv(envMark) == "1" }

// Disabled reports whether the user turned the sandbox off.
func Disabled() bool { return os.Getenv("TUON_NO_SANDBOX") != "" }

// Profile is the macOS sandbox profile for a home folder, temp folder and
// the chat app (exe, tuon-app, with the Touch ID helper next to it). With
// gatePort > 0 the app's only way out is the launcher's gate on that port.
func Profile(home, tmp, exe string, gatePort int) string {
	q := func(p string) string { return fmt.Sprintf("%q", p) } // SBPL strings use Go-compatible escapes
	var b strings.Builder
	b.WriteString("(version 1)\n(allow default)\n")
	// reads in your home: only tuon's folder, its program folder and the
	// attachment folders. Not ~/Projects, dotfiles or anything else.
	readable := []string{filepath.Join(home, ".config", "tuon"), filepath.Dir(realDir(exe)), filepath.Dir(exe),
		filepath.Join(home, ".CFUserTextEncoding")} // pbcopy, afplay & co read the text encoding setting
	for _, f := range ReadableFolders {
		readable = append(readable, filepath.Join(home, f), realDir(filepath.Join(home, f)))
	}
	b.WriteString("(deny file-read* (subpath " + q(home) + "))\n(allow file-read*\n")
	for _, p := range uniq(readable) {
		b.WriteString("  (subpath " + q(p) + ")\n")
	}
	b.WriteString(")\n")
	// stat (not list) home and the folders on the way to those
	parents := map[string]bool{home: true}
	for _, p := range readable {
		for d := filepath.Dir(p); strings.HasPrefix(d, home+"/") && !parents[d]; d = filepath.Dir(d) {
			parents[d] = true
		}
	}
	b.WriteString("(allow file-read-metadata\n")
	for _, d := range sortedKeys(parents) {
		b.WriteString("  (literal " + q(d) + ")\n")
	}
	b.WriteString(")\n")
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
	// writes: tuon's folder, this user's temp folder (T only: its parent
	// also holds every other app's caches, C/), terminals. Not Downloads:
	// the launcher saves files there, marked as downloaded (OpSave).
	b.WriteString("(deny file-write* (require-not (require-any\n")
	for _, p := range []string{filepath.Join(home, ".config", "tuon"), tmp} {
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
	// no Unix sockets (SSH agent, Docker, local tools)
	b.WriteString("(deny network-outbound (remote unix-socket))\n")
	if gatePort > 0 {
		// only the gate: no other host, IP, port or DNS lookup
		fmt.Fprintf(&b, "(deny network-outbound (remote ip))\n(allow network-outbound (remote ip \"localhost:%d\"))\n", gatePort)
	} else {
		b.WriteString("(allow network-outbound (remote unix-socket (path-literal " + q(dnsSocket) + ")))\n")
	}
	return b.String()
}

// DevProfile is the profile for running Go tests and tools on the dev
// machine, where dependency code runs too (`go test` runs their init and
// test code). It may read only the repo, Go's module cache and toolchain
// and its own build cache under home; it may write only temp and that
// build cache (separate from the normal one, so a test can't poison what
// a later `go build` links). No Unix sockets; no network beyond localhost
// unless net. live adds tuon's own folder, for TUON_LIVE tests.
func DevProfile(home, tmp, repo, gopath, cache string, net, live bool) string {
	q := func(p string) string { return fmt.Sprintf("%q", p) }
	var b strings.Builder
	b.WriteString("(version 1)\n(allow default)\n")
	b.WriteString("(deny file-read* (subpath " + q(home) + "))\n")
	read := []string{repo, gopath, cache, filepath.Join(home, "Library", "Application Support", "go")}
	if live {
		read = append(read, filepath.Join(home, ".config", "tuon"), filepath.Join(home, ".local", "bin"))
	}
	b.WriteString("(allow file-read*\n  (literal " + q(home) + ")\n")
	for _, p := range read {
		b.WriteString("  (subpath " + q(p) + ")\n")
	}
	b.WriteString(")\n")
	// stat (not list or read) the folders on the way to them: SQLite and
	// others lstat each parent of a path they open
	parents := map[string]bool{}
	for _, p := range read {
		for d := filepath.Dir(p); strings.HasPrefix(d, home+"/") && !parents[d]; d = filepath.Dir(d) {
			parents[d] = true
		}
	}
	if len(parents) > 0 {
		b.WriteString("(allow file-read-metadata\n")
		for _, d := range sortedKeys(parents) {
			b.WriteString("  (literal " + q(d) + ")\n")
		}
		b.WriteString(")\n")
	}
	write := []string{cache, tmp}
	if live {
		write = append(write, filepath.Join(home, ".config", "tuon"))
	}
	b.WriteString("(deny file-write* (require-not (require-any\n")
	for _, p := range write {
		b.WriteString("  (subpath " + q(p) + ")\n")
	}
	// /dev/ptmx: the relay tests open a pseudo-terminal of their own
	b.WriteString(`  (regex #"^/dev/(null|zero|tty|ttys[0-9]+|ptmx|dtracehelper|random|urandom)$")` + "\n")
	b.WriteString(")))\n")
	b.WriteString("(deny appleevent-send)\n")
	b.WriteString("(deny network-outbound (remote unix-socket))\n")
	if net {
		b.WriteString("(allow network-outbound (remote unix-socket (path-literal " + q(dnsSocket) + ")))\n")
	} else {
		b.WriteString("(deny network-outbound (remote ip))\n(allow network-outbound (remote ip \"localhost:*\"))\n")
	}
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

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func uniq(ps []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, p := range ps {
		if p != "" && !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	return out
}
