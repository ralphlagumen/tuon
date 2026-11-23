package messenger

import (
	"net/http"
	"net/url"

	"go.mau.fi/util/exhttp"
)

// gateURL: the launcher's network gate (internal/netgate), when tuon runs
// in the sandbox. Every connection goes through it: the sandbox refuses
// anything else, so a path that skipped it would fail, not leak.
var gateURL string

// SetGate routes messagix, whatsmeow and photo downloads through the gate
// at addr (host:port). Call it before any connection.
func SetGate(addr string) error {
	if addr == "" {
		return nil
	}
	u, err := url.Parse("http://" + addr)
	if err != nil {
		return err
	}
	gateURL = u.String()
	mediaHTTP.Transport = &http.Transport{Proxy: http.ProxyURL(u), ForceAttemptHTTP2: true}
	return nil
}

// clientSettings for messagix: its HTTP client and its websocket both take
// the proxy from here (it's compiled into both when the client is made).
func clientSettings() exhttp.ClientSettings {
	var cs exhttp.ClientSettings
	if gateURL != "" {
		cs, _ = cs.WithProxy(gateURL)
	}
	return cs
}
