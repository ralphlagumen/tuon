package main

import "testing"

func TestNewerTag(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{{"v1.10.0", "v1.9.0", true}, {"v0.2.0", "v0.2.0", false}, {"v0.2.0", "v0.3.0", false}, {"v1.0.0", "v0.9.9", true}} {
		if got := newerTag(c.a, c.b); got != c.want {
			t.Errorf("newerTag(%s, %s) = %v", c.a, c.b, got)
		}
	}
}

func TestNeedsKey(t *testing.T) {
	for args, want := range map[string]bool{"": true, "login": true, "history": true, "relink": true, "setup": true, "--help": false, "logout": false, "sandbox-test": false} {
		var a []string
		if args != "" {
			a = []string{args}
		}
		if needsKey(a) != want {
			t.Errorf("needsKey(%q) = %v", args, !want)
		}
	}
}
