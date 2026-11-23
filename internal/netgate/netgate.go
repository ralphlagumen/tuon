// Package netgate is the launcher's network gate: a tiny HTTP CONNECT
// proxy on localhost that lets the sandboxed chat app reach Meta's servers
// and nothing else. The sandbox refuses every other connection (other
// hosts, IPs, DNS, Unix sockets), so a bad library in the app can't upload
// your session or messages to a server of its own. It runs in the
// launcher (standard library only) and keeps its log where the app can't
// write, so a compromised app can't hide what it tried.
package netgate

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

// Env names the gate's address (host:port) for the app.
const Env = "TUON_PROXY"

// EventsFDEnv names the pipe (fd 7) the launcher writes refused hosts to,
// one per line, so the app can warn while it runs.
const EventsFDEnv = "TUON_NETGATE_FD"

// metaDomains: the app may reach these and their subdomains, on port 443
// only. Messenger (facebook.com, messenger.com), photos and files
// (fbcdn, fbsbx), encrypted chats (whatsapp).
var metaDomains = []string{
	"facebook.com", "facebook.net", "messenger.com", "fbcdn.net", "fbcdn.com", "fbsbx.com",
	"whatsapp.net", "whatsapp.com",
}

// Allowed reports whether the app may connect to hostport.
func Allowed(hostport string) bool {
	host, port, err := net.SplitHostPort(hostport)
	if err != nil || port != "443" {
		return false
	}
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	if net.ParseIP(host) != nil {
		return false // a bare address could be anyone's
	}
	for _, d := range metaDomains {
		if host == d || strings.HasSuffix(host, "."+d) {
			return true
		}
	}
	return false
}

// Gate counts what went through and what was refused.
type Gate struct {
	// OnRefuse is called (in its own goroutine) for each refused target.
	OnRefuse func(target string)
	// dial connects to an allowed target (net.DialTimeout; tests swap it).
	dial func(network, addr string, timeout time.Duration) (net.Conn, error)

	mu      sync.Mutex
	allowed map[string]int
	refused map[string]int
}

func New() *Gate {
	return &Gate{dial: net.DialTimeout, allowed: map[string]int{}, refused: map[string]int{}}
}

// Serve accepts connections until l is closed.
func (g *Gate) Serve(l net.Listener) {
	for {
		c, err := l.Accept()
		if err != nil {
			return
		}
		go g.handle(c)
	}
}

func (g *Gate) handle(c net.Conn) {
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(15 * time.Second))
	br := bufio.NewReader(c)
	req, err := http.ReadRequest(br)
	if err != nil {
		return
	}
	target := req.Host
	if req.Method != http.MethodConnect || !Allowed(target) {
		if req.Method != http.MethodConnect {
			target = req.Method + " " + req.Host
		}
		g.note(g.refused, clean(target))
		if g.OnRefuse != nil {
			go g.OnRefuse(clean(target))
		}
		_, _ = io.WriteString(c, "HTTP/1.1 403 Forbidden\r\nContent-Length: 0\r\n\r\n")
		return
	}
	up, err := g.dial("tcp", target, 15*time.Second)
	if err != nil {
		_, _ = io.WriteString(c, "HTTP/1.1 502 Bad Gateway\r\nContent-Length: 0\r\n\r\n")
		return
	}
	defer up.Close()
	g.note(g.allowed, strings.ToLower(target))
	if _, err := io.WriteString(c, "HTTP/1.1 200 Connection established\r\n\r\n"); err != nil {
		return
	}
	_ = c.SetDeadline(time.Time{})
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(up, br); closeWrite(up); done <- struct{}{} }() // br: bytes already read stay in order
	go func() { _, _ = io.Copy(c, up); closeWrite(c); done <- struct{}{} }()
	<-done
	<-done
}

func closeWrite(c net.Conn) {
	if t, ok := c.(*net.TCPConn); ok {
		_ = t.CloseWrite()
	}
}

func (g *Gate) note(m map[string]int, target string) {
	g.mu.Lock()
	m[target]++
	g.mu.Unlock()
}

// Refused lists refused targets with counts ("evil.example:443 ×2").
func (g *Gate) Refused() []string { return g.list(g.refused) }

// AllowedHosts lists reached targets with counts.
func (g *Gate) AllowedHosts() []string { return g.list(g.allowed) }

func (g *Gate) list(m map[string]int) []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	var out []string
	for k, n := range m {
		out = append(out, fmt.Sprintf("%s ×%d", k, n))
	}
	sort.Strings(out)
	return out
}

// clean keeps a target printable and short: it comes from the app, so it
// may be hostile text.
func clean(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune(".-:[] _", r) {
			b.WriteRune(r)
		} else {
			b.WriteRune('?')
		}
		if b.Len() >= 100 {
			break
		}
	}
	return b.String()
}
