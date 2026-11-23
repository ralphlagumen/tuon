package sandbox

import (
	"strings"
	"testing"
)

func TestProfile(t *testing.T) {
	p := Profile(`/Users/a "b"`, "/private/var/folders/x/y/T", "/Users/a/.local/bin/tuon-app", 0)
	for _, want := range []string{`(allow default)`, `/Users/a \"b\"/.ssh`, `Library/Keychains`, `(deny file-write* (require-not`, `/Users/a \"b\"/.config/tuon`, `(subpath "/private/var/folders/x/y/T")`, `(deny process-exec*)`, `/usr/bin/afplay`, `/Users/a/.local/bin/tuon-keyhelper`, `(deny appleevent-send)`, `.claude`} {
		if !strings.Contains(p, want) {
			t.Errorf("profile missing %q", want)
		}
	}
	for _, bad := range []string{`"/usr/bin/open"`, `"/usr/bin/osascript"`} {
		if strings.Contains(p, bad) {
			t.Errorf("%s can ask macOS to start programs outside the sandbox", bad)
		}
	}
	if strings.Contains(p, `(subpath "/private/var/folders/x/y")`) {
		t.Error("the temp parent holds other apps' caches (C/): only T is writable")
	}
	if strings.Contains(p, `"/Users/a/.local/bin/tuon"`) {
		t.Error("the sandboxed app must not be able to start the launcher (it runs unsandboxed)")
	}
	if !strings.Contains(p, "(deny network-outbound (remote unix-socket))") || !strings.Contains(p, "mDNSResponder") {
		t.Error("Unix sockets (SSH agent, Docker) must be refused, name lookups allowed")
	}
	if strings.Contains(p, "(remote ip") {
		t.Error("the profile shouldn't limit the internet yet (Messenger needs it)")
	}
}

// TestProfileReads: home is unreadable except tuon's folders and the
// attachment folders; home itself can be stat'ed, not listed.
func TestProfileReads(t *testing.T) {
	p := Profile("/Users/a", "/private/var/folders/x/y/T", "/Users/a/.local/bin/tuon-app", 0)
	for _, want := range []string{`(deny file-read* (subpath "/Users/a"))`, `(subpath "/Users/a/Desktop")`, `(subpath "/Users/a/.config/tuon")`,
		`(subpath "/Users/a/.local/bin")`, "(allow file-read-metadata\n  (literal \"/Users/a\")"} {
		if !strings.Contains(p, want) {
			t.Errorf("profile missing %q", want)
		}
	}
	if strings.Contains(p, `(subpath "/Users/a/Projects")`) {
		t.Error("~/Projects must not be readable")
	}
	// the secret-folder denies come after the allows, so they still win
	if strings.Index(p, `/Users/a/.ssh`) < strings.Index(p, `(subpath "/Users/a/Desktop")`) {
		t.Error("deny list must follow the allow list")
	}
}

// TestProfileGated: with the launcher's gate, the only way out is its port:
// no other host, IP or port, no DNS socket (a lookup can carry data out).
func TestProfileGated(t *testing.T) {
	p := Profile("/Users/a", "/private/var/folders/x/y/T", "/Users/a/.local/bin/tuon-app", 52123)
	for _, want := range []string{"(deny network-outbound (remote ip))", `(allow network-outbound (remote ip "localhost:52123"))`, "(deny network-outbound (remote unix-socket))"} {
		if !strings.Contains(p, want) {
			t.Errorf("gated profile missing %q", want)
		}
	}
	if strings.Contains(p, "mDNSResponder") {
		t.Error("gated profile must not allow DNS lookups")
	}
}

func TestAppEnv(t *testing.T) {
	got := strings.Join(AppEnv([]string{
		"HOME=/h", "TERM=xterm", "TERM_PROGRAM=ghostty", "LC_ALL=C", "TUON_DEBUG=1", "__CF_USER_TEXT_ENCODING=0x1F5:0x8000100:0x0",
		"SSH_AUTH_SOCK=/tmp/agent", "GITHUB_TOKEN=x", "AWS_SECRET_ACCESS_KEY=y", "DOCKER_HOST=unix:///s", "GPG_AGENT_INFO=z",
	}), " ")
	for _, want := range []string{"HOME=/h", "TERM=xterm", "TERM_PROGRAM=ghostty", "LC_ALL=C", "TUON_DEBUG=1", "__CF_USER_TEXT_ENCODING"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %s", want)
		}
	}
	for _, bad := range []string{"SSH_AUTH_SOCK", "GITHUB_TOKEN", "AWS_SECRET", "DOCKER_HOST", "GPG_AGENT"} {
		if strings.Contains(got, bad) {
			t.Errorf("%s must not reach the app", bad)
		}
	}
}

func TestDevProfile(t *testing.T) {
	p := DevProfile("/Users/a", "/private/var/folders/x/y/T", "/Users/a/src/tuon", "/Users/a/go", "/Users/a/Library/Caches/tuon-dev-gocache", false, false)
	for _, want := range []string{`(deny file-read* (subpath "/Users/a"))`, `(subpath "/Users/a/src/tuon")`, `(remote unix-socket)`, `(deny network-outbound (remote ip))`} {
		if !strings.Contains(p, want) {
			t.Errorf("dev profile missing %q", want)
		}
	}
	if strings.Contains(p, ".config/tuon") {
		t.Error("tuon's real data only with live")
	}
	live := DevProfile("/Users/a", "/private/var/folders/x/y/T", "/Users/a/src/tuon", "/Users/a/go", "/Users/a/Library/Caches/tuon-dev-gocache", true, true)
	if !strings.Contains(live, "(allow file-read-metadata\n  (literal \"/Users/a/.config\")") {
		t.Error("live: SQLite needs to stat ~/.config on the way to ~/.config/tuon")
	}
}
