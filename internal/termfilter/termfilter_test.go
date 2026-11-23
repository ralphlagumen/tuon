package termfilter

import (
	"strings"
	"testing"
)

func run(in string) string {
	var f Filter
	return string(f.Filter([]byte(in)))
}

// byteByByte feeds one byte at a time: a code split anywhere across reads
// must come out the same as in one piece.
func byteByByte(in string) string {
	var f Filter
	var b strings.Builder
	for i := 0; i < len(in); i++ {
		b.Write(f.Filter([]byte{in[i]}))
	}
	return b.String()
}

func TestFilter(t *testing.T) {
	const st = "\x1b\\"
	kittyFirst := "\x1b_Ga=t,f=100,t=d,i=7,q=2,m=1;iVBORw0KGgo=" + st
	kittyMore := "\x1b_Gm=0;AAAA" + st
	kittyPlace := "\x1b_Ga=p,U=1,i=7,c=10,r=5,q=2" + st
	cases := []struct{ name, in, want string }{
		{"plain text and CSI", "hi \x1b[1;31mred\x1b[0m \x1b[?1049h\x1b[2J\x1b[6n é ❤", "hi \x1b[1;31mred\x1b[0m \x1b[?1049h\x1b[2J\x1b[6n é ❤"},
		{"bubble tea color query (BEL)", "\x1b]11;?\x07x", "\x1b]11;?\x07x"},
		{"color query (ST)", "\x1b]10;?" + st + "x", "\x1b]10;?" + st + "x"},
		{"set color refused", "\x1b]11;#ff0000\x07x", "x"},
		{"hyperlink refused, text kept", "\x1b]8;;x-apple.systempreferences:\x07click\x1b]8;;\x07!", "click!"},
		{"title refused", "\x1b]0;sudo rm -rf ~\x07ok", "ok"},
		{"iTerm file download refused", "\x1b]1337;File=name=eA==;inline=0:AAAA\x07ok", "ok"},
		{"notification refused", "\x1b]9;You've been logged out" + st + "ok", "ok"},
		{"clipboard write passes", "\x1b]52;c;aGVsbG8=\x07ok", "\x1b]52;c;aGVsbG8=\x07ok"},
		{"clipboard read refused", "\x1b]52;c;?\x07ok", "ok"},
		{"kitty inline image passes", kittyFirst + kittyMore + kittyPlace, kittyFirst + kittyMore + kittyPlace},
		{"kitty delete passes", "\x1b_Ga=d,d=A,q=2" + st, "\x1b_Ga=d,d=A,q=2" + st},
		{"kitty file path refused", "\x1b_Ga=T,t=f,f=100;L1VzZXJzL3gvLnNzaC9pZF9yc2E=" + st + "ok", "ok"},
		{"kitty shared memory refused", "\x1b_Ga=T,t=s;bmFtZQ==" + st + "ok", "ok"},
		{"other APC refused", "\x1b_Xsomething" + st + "ok", "ok"},
		{"DCS tmux passthrough refused", "\x1bPtmux;\x1b\x1b]0;evil\x07" + st + "ok", "ok"},
		{"DECRQSS refused", "\x1bP$qm" + st + "ok", "ok"},
		{"UTF-8 C1 OSC refused", "a\u009d0;evil\x07b", "a0;evil\x07b"},
		{"UTF-8 non-C1 kept", "\u00a0\u00e9", "\u00a0\u00e9"},
		{"aborted string closes", "\x1b]52;c;aGk=\x1b[31mred", "\x1b]52;c;aGk=" + st + "\x1b[31mred"},
		{"long header refused", "\x1b_G" + strings.Repeat("a", 600) + ";x" + st + "ok", "ok"},
	}
	for _, c := range cases {
		if got := run(c.in); got != c.want {
			t.Errorf("%s:\n got %q\nwant %q", c.name, got, c.want)
		}
		if got := byteByByte(c.in); got != c.want {
			t.Errorf("%s (byte by byte):\n got %q\nwant %q", c.name, got, c.want)
		}
	}
}

func TestDroppedCount(t *testing.T) {
	var f Filter
	f.Filter([]byte("\x1b]0;t\x07\x1b]11;?\x07\x1bPq\x1b\\"))
	if f.Dropped != 2 {
		t.Fatalf("dropped %d, want 2 (title, DCS)", f.Dropped)
	}
}

func BenchmarkFilter(b *testing.B) {
	frame := []byte(strings.Repeat("\x1b[38;2;10;20;30mhello world ❤ \x1b[0m", 2000))
	var f Filter
	b.SetBytes(int64(len(frame)))
	for i := 0; i < b.N; i++ {
		f.Filter(frame)
	}
}
