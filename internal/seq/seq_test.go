package seq

import "testing"

func TestCompleteLen(t *testing.T) {
	wheel := "\x1b[<65;95;46M"
	for in, want := range map[string]int{
		"":                           0,
		"abc":                        3,
		wheel + wheel:                24,
		wheel + "\x1b[<65;9":         12,
		wheel + "\x1b":               12,
		"\x1b[A":                     3,
		"\x1b[":                      0,
		"\x1bO":                      0,
		"\x1bOA":                     3,
		"\x1bx":                      2,
		"\x1b]11;rgb:1f/23/29":       0,
		"\x1b]11;rgb:1f/23/29\x07":   18,
		"\x1b]11;rgb:1f/23/29\x1b\\": 19,
		"h\xc3":                      1,
		"h\xc3\xa9":                  3,
		"\xe2\x9d":                   0,
		"\x1b[200~text":              10,
	} {
		if got := CompleteLen([]byte(in)); got != want {
			t.Errorf("CompleteLen(%q) = %d, want %d", in, got, want)
		}
	}
}
