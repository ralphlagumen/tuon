package messenger

import (
	"net/http"
	"strings"
)

// keepCookiesOnRedirect: Facebook can send www.facebook.com/messages on to
// web.facebook.com (a regional redirect, "?_rdc=1&_rdr"). messagix sends the
// login as a Cookie header, and Go drops that header when a redirect changes
// host, so web.facebook.com saw no login and sent tuon to login.php: a
// "logged out" loop right after logging in. This puts the header back, only
// for https facebook.com hosts and only when the first request carried it.
func keepCookiesOnRedirect(h *http.Client) {
	next := h.CheckRedirect
	h.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if next != nil {
			if err := next(req, via); err != nil {
				return err
			}
		} else if len(via) >= 10 {
			return http.ErrUseLastResponse
		}
		if len(via) == 0 || req.Header.Get("Cookie") != "" || req.URL.Scheme != "https" || !isFacebookHost(req.URL.Hostname()) {
			return nil
		}
		if ck := via[0].Header.Get("Cookie"); ck != "" && isFacebookHost(via[0].URL.Hostname()) {
			req.Header.Set("Cookie", ck)
		}
		return nil
	}
}

// isFacebookHost: facebook.com or a subdomain, never a look-alike.
func isFacebookHost(h string) bool {
	h = strings.ToLower(h)
	return h == "facebook.com" || strings.HasSuffix(h, ".facebook.com")
}
