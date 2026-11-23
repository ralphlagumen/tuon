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
	"sync/atomic"
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

// Upload alarms. tuon's whole history and keys are a few MB, so a theft
// through Meta (sent to the thief's account) is small; what gives it away
// is when it happens. You send things right after typing; an idle tuon
// uploads only protocol chatter (KBs). So: more than IdleWarnBytes
// uploaded within IdleWindow while you've been idle is unusual, and so is
// more than TotalWarnBytes in one run (bulk).
const (
	IdleAfter      = 5 * time.Minute // no key or mouse input for this long = idle
	IdleWindow     = 10 * time.Minute
	IdleWarnBytes  = 1 << 20
	TotalWarnBytes = 200 << 20
)

// Gate counts what went through and what was refused.
type Gate struct {
	// OnRefuse is called (in its own goroutine) for each refused target.
	OnRefuse func(target string)
	// OnUnusual is called (in its own goroutine) when uploads look unusual.
	OnUnusual func(msg string)
	// Idle reports whether you've been away from the keyboard IdleAfter
	// or longer (nil: never counted as idle).
	Idle func() bool
	now  func() time.Time

	up, down atomic.Int64
	idleMu   sync.Mutex
	idleUp   int64     // all idle upload this run
	bucket   time.Time // start of the current IdleWindow
	inBucket int64
	warned   bool  // this window already warned
	maxIdle  int64 // the most idle upload seen in one window (for tuning)
	totalAt  int64 // next TotalWarnBytes mark
	// dial connects to an allowed target (net.DialTimeout; tests swap it).
	dial func(network, addr string, timeout time.Duration) (net.Conn, error)

	mu      sync.Mutex
	allowed map[string]int
	refused map[string]int
}

func New() *Gate {
	return &Gate{dial: net.DialTimeout, now: time.Now, allowed: map[string]int{}, refused: map[string]int{}, totalAt: TotalWarnBytes}
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
	go func() { _, _ = io.Copy(counter{up, g.sent}, br); closeWrite(up); done <- struct{}{} }() // br: bytes already read stay in order
	go func() { _, _ = io.Copy(counter{c, g.received}, up); closeWrite(c); done <- struct{}{} }()
	<-done
	<-done
}

// counter passes writes on and counts them.
type counter struct {
	w   io.Writer
	add func(int)
}

func (c counter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.add(n)
	return n, err
}

func (g *Gate) received(n int) { g.down.Add(int64(n)) }

// sent counts upload toward Meta and raises the alarms above.
func (g *Gate) sent(n int) {
	if n <= 0 {
		return
	}
	total := g.up.Add(int64(n))
	var msgs []string
	g.idleMu.Lock()
	if total >= g.totalAt {
		msgs = append(msgs, fmt.Sprintf("tuon uploaded %s to Meta this run, far more than normal", size(total)))
		for g.totalAt <= total {
			g.totalAt += TotalWarnBytes
		}
	}
	if g.Idle != nil && g.Idle() {
		now := g.now()
		if now.Sub(g.bucket) > IdleWindow {
			g.bucket, g.inBucket, g.warned = now, 0, false
		}
		g.inBucket += int64(n)
		g.idleUp += int64(n)
		g.maxIdle = max(g.maxIdle, g.inBucket)
		if g.inBucket > IdleWarnBytes && !g.warned {
			g.warned = true
			msgs = append(msgs, fmt.Sprintf("tuon uploaded %s while you were away from the keyboard", size(g.inBucket)))
		}
	}
	g.idleMu.Unlock()
	if g.OnUnusual != nil {
		for _, m := range msgs {
			go g.OnUnusual(m)
		}
	}
}

// Traffic: bytes up and down this run, idle upload, and the most idle
// upload in one window.
func (g *Gate) Traffic() (up, down, idleUp, maxIdle int64) {
	g.idleMu.Lock()
	defer g.idleMu.Unlock()
	return g.up.Load(), g.down.Load(), g.idleUp, g.maxIdle
}

func size(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%d KB", n>>10)
	}
	return fmt.Sprintf("%d B", n)
}

// Size formats a byte count the way the gate's messages do.
func Size(n int64) string { return size(n) }

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
