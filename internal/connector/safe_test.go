package connector

import "testing"

func TestSafeText(t *testing.T) {
	cases := []struct {
		in   string
		keep bool
		want string
	}{
		{"hi\x1b]52;c;ZXZpbA==\x07there", true, "hi]52;c;ZXZpbA==there"}, // clipboard write
		{"\x1b[2J\x1b[Hgotcha", true, "[2J[Hgotcha"},                     // clear screen
		{"title\x1b]0;pwned\x07", false, "title]0;pwned"},                // window title
		{"photo‮gpj.exe", false, "photogpj.exe"},                         // reversed text
		{"line1\nline2", true, "line1\nline2"},
		{"line1\nline2", false, "line1line2"},
		{"a\tb\u0085c\u009bd", true, "a bcd"}, // tab, C1 controls
		{"ok 😆 ₱25", true, "ok 😆 ₱25"},
		{"Ra\u200bine", false, "Raine"},              // zero-width space
		{"\ufeffRaine\u2060", false, "Raine"},        // BOM, word joiner
		{"a\u2028b", true, "a\nb"},                   // line separator
		{"a\u2029b", false, "a b"},                   // paragraph separator
		{"👨\u200d👩\u200d👧", true, "👨\u200d👩\u200d👧"}, // emoji joiners kept here
	}
	for _, c := range cases {
		if got := SafeText(c.in, c.keep); got != c.want {
			t.Errorf("SafeText(%q, %v) = %q, want %q", c.in, c.keep, got, c.want)
		}
	}
}
