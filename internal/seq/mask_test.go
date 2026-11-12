package seq

import "testing"

func TestMasked(t *testing.T) {
	for in, want := range map[string]string{
		"hi\x1b[<65;127;46Mok": `xx\e[<65;127;46Mxx`,
		"\x1b":                 `\e`,
		"\x1b\x1b[<65;1;2M":    `\e\e[<65;1;2M`,
		"\x1bOA pass1\r":       `\eOAxxxxxx\x0d`,
		"\x1b[<65;12":          `\e[<65;12`,
		"\x1bx":                `\ex`,
	} {
		if got := Masked([]byte(in)); got != want {
			t.Errorf("Masked(%q) = %s, want %s", in, got, want)
		}
	}
}
