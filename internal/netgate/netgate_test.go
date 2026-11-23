package netgate

import (
	"bufio"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestAllowed(t *testing.T) {
	for hp, want := range map[string]bool{
		"www.facebook.com:443":       true,
		"edge-chat.facebook.com:443": true,
		"scontent.xx.fbcdn.net:443":  true,
		"web.whatsapp.com:443":       true,
		"WWW.Messenger.COM.:443":     true,
		"facebook.com:80":            false, // https only
		"facebook.com.evil.io:443":   false,
		"evilfacebook.com:443":       false,
		"example.com:443":            false,
		"157.240.1.35:443":           false, // a bare IP could be anyone's
		"[2a03:2880::1]:443":         false,
		"facebook.com":               false,
	} {
		if got := Allowed(hp); got != want {
			t.Errorf("Allowed(%q) = %v, want %v", hp, got, want)
		}
	}
}

// TestGate: CONNECT to a Meta host is tunnelled (to a fake upstream here),
// anything else is refused, reported and counted.
func TestGate(t *testing.T) {
	up, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer up.Close()
	go func() {
		for {
			c, err := up.Accept()
			if err != nil {
				return
			}
			go func() { defer c.Close(); _, _ = io.Copy(c, c) }() // echo
		}
	}()
	g := New()
	g.dial = func(network, addr string, timeout time.Duration) (net.Conn, error) {
		return net.DialTimeout(network, up.Addr().String(), timeout) // every allowed host → the echo server
	}
	refused := make(chan string, 4)
	g.OnRefuse = func(s string) { refused <- s }
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	go g.Serve(l)

	connect := func(target string) (int, net.Conn, *bufio.Reader) {
		c, err := net.Dial("tcp", l.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.WriteString(c, "CONNECT "+target+" HTTP/1.1\r\nHost: "+target+"\r\n\r\n")
		br := bufio.NewReader(c)
		resp, err := http.ReadResponse(br, nil)
		if err != nil {
			t.Fatal(err)
		}
		return resp.StatusCode, c, br
	}

	code, c, br := connect("www.facebook.com:443")
	if code != 200 {
		t.Fatalf("facebook: %d", code)
	}
	_, _ = io.WriteString(c, "ping\n")
	if line, _ := br.ReadString('\n'); line != "ping\n" {
		t.Fatalf("tunnel echoed %q", line)
	}
	c.Close()

	code, c, _ = connect("evil.example:443")
	c.Close()
	if code != 403 {
		t.Fatalf("evil: %d", code)
	}
	select {
	case s := <-refused:
		if s != "evil.example:443" {
			t.Fatalf("refused %q", s)
		}
	case <-time.After(time.Second):
		t.Fatal("no refusal reported")
	}
	// a plain GET (not CONNECT) is refused too
	pc, _ := net.Dial("tcp", l.Addr().String())
	_, _ = io.WriteString(pc, "GET http://example.com/ HTTP/1.1\r\nHost: example.com\r\n\r\n")
	resp, err := http.ReadResponse(bufio.NewReader(pc), nil)
	pc.Close()
	if err != nil || resp.StatusCode != 403 {
		t.Fatalf("GET through the gate: %v %v", resp, err)
	}
	<-refused
	if r := strings.Join(g.Refused(), ","); !strings.Contains(r, "evil.example:443 ×1") || !strings.Contains(r, "GET example.com") {
		t.Fatalf("refused list %q", r)
	}
	if a := strings.Join(g.AllowedHosts(), ","); a != "www.facebook.com:443 ×1" {
		t.Fatalf("allowed list %q", a)
	}
}

func TestCleanHostileTarget(t *testing.T) {
	if got := clean("evil\x1b]0;pwned\x07.com:443"); strings.ContainsAny(got, "\x1b\x07") {
		t.Fatalf("control codes kept: %q", got)
	}
}
