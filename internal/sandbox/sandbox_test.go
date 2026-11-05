package sandbox

import (
	"strings"
	"testing"
)

func TestProfile(t *testing.T) {
	p := Profile(`/Users/a "b"`, "/private/var/folders/x/T")
	for _, want := range []string{`(allow default)`, `/Users/a \"b\"/.ssh`, `Library/Keychains`, `(deny file-write* (require-not`, `/Users/a \"b\"/.config/tuon`, `/private/var/folders/x/T`} {
		if !strings.Contains(p, want) {
			t.Errorf("profile missing %q", want)
		}
	}
	if strings.Contains(p, "network") {
		t.Error("the profile shouldn't touch the network (Messenger needs it)")
	}
}
