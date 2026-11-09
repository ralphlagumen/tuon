package sandbox

import (
	"strings"
	"testing"
)

func TestProfile(t *testing.T) {
	p := Profile(`/Users/a "b"`, "/private/var/folders/x/y/T", "/Users/a/.local/bin/tuon-app")
	for _, want := range []string{`(allow default)`, `/Users/a \"b\"/.ssh`, `Library/Keychains`, `(deny file-write* (require-not`, `/Users/a \"b\"/.config/tuon`, `/private/var/folders/x/y"`, `(deny process-exec*)`, `/usr/bin/afplay`, `/Users/a/.local/bin/tuon-keyhelper`, `(deny appleevent-send)`, `.claude`} {
		if !strings.Contains(p, want) {
			t.Errorf("profile missing %q", want)
		}
	}
	for _, bad := range []string{`"/usr/bin/open"`, `"/usr/bin/osascript"`} {
		if strings.Contains(p, bad) {
			t.Errorf("%s can ask macOS to start programs outside the sandbox", bad)
		}
	}
	if strings.Contains(p, `"/Users/a/.local/bin/tuon"`) {
		t.Error("the sandboxed app must not be able to start the launcher (it runs unsandboxed)")
	}
	if strings.Contains(p, "network") {
		t.Error("the profile shouldn't touch the network (Messenger needs it)")
	}
}
