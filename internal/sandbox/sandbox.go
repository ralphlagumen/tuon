// Package sandbox limits what tuon's own process can touch, so a bad
// dependency (or a bug) can't read your other secrets or plant files.
//
// On macOS tuon re-launches itself under sandbox-exec with a profile that:
//   - refuses reads of credential stores: SSH and cloud keys, password
//     managers, browser profiles and cookies, keychains, shell history,
//     other apps' messages and mail;
//   - refuses writes anywhere except tuon's folder, Downloads (saved
//     files) and the temp folder — so nothing can replace the tuon
//     binary or add itself to your shell's startup files.
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

// denyRead: paths under your home that tuon never needs to read.
var denyRead = []string{
	".ssh", ".gnupg", ".aws", ".azure", ".kube", ".docker", ".config/gh", ".config/gcloud",
	".config/op", ".password-store", ".netrc", ".npmrc", ".pypirc", ".git-credentials",
	".zsh_history", ".bash_history", ".local/share/fish",
	"Library/Keychains", "Library/Cookies", "Library/Safari", "Library/Messages", "Library/Mail",
	"Library/Application Support/Google/Chrome", "Library/Application Support/BraveSoftware",
	"Library/Application Support/Microsoft Edge", "Library/Application Support/Arc",
	"Library/Application Support/Firefox", "Library/Application Support/1Password",
	"Library/Group Containers/2BUA8C4S2C.com.1password",
}

// Active reports whether this process already runs sandboxed.
func Active() bool { return os.Getenv(envMark) == "1" }

// Disabled reports whether the user turned the sandbox off.
func Disabled() bool { return os.Getenv("TUON_NO_SANDBOX") != "" }

// Profile is the macOS sandbox profile for a home and temp folder.
func Profile(home, tmp string) string {
	q := func(p string) string { return fmt.Sprintf("%q", p) } // SBPL strings use Go-compatible escapes
	var b strings.Builder
	b.WriteString("(version 1)\n(allow default)\n")
	b.WriteString("(deny file-read* file-write*\n")
	for _, p := range denyRead {
		b.WriteString("  (subpath " + q(filepath.Join(home, p)) + ")\n")
	}
	b.WriteString(")\n")
	b.WriteString("(deny file-write* (require-not (require-any\n")
	for _, p := range []string{
		filepath.Join(home, ".config", "tuon"),
		filepath.Join(home, "Downloads"),
		tmp, "/private/tmp", "/private/var/folders", "/dev",
	} {
		b.WriteString("  (subpath " + q(p) + ")\n")
	}
	b.WriteString(")))\n")
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
