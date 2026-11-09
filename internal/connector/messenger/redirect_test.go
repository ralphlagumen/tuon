package messenger

import (
	"net/http"
	"testing"
)

type hops struct{ cookies map[string]string }

// www.facebook.com redirects to `to`; every request's Cookie header is kept by host.
func (h *hops) client(to string) *http.Client {
	return &http.Client{Transport: rtFunc(func(r *http.Request) (*http.Response, error) {
		h.cookies[r.URL.Host] = r.Header.Get("Cookie")
		res := &http.Response{StatusCode: 200, Header: http.Header{}, Body: http.NoBody, Request: r}
		if r.URL.Host == "www.facebook.com" {
			res.StatusCode = 302
			res.Header.Set("Location", to)
		}
		return res, nil
	})}
}

type rtFunc func(*http.Request) (*http.Response, error)

func (f rtFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func get(t *testing.T, c *http.Client) {
	req, _ := http.NewRequest("GET", "https://www.facebook.com/messages", nil)
	req.Header.Set("Cookie", "c_user=1; xs=2")
	res, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
}

func TestCookiesFollowFacebookRedirect(t *testing.T) {
	h := &hops{cookies: map[string]string{}}
	c := h.client("https://web.facebook.com/messages?_rdc=1&_rdr")
	get(t, c)
	if h.cookies["web.facebook.com"] != "" {
		t.Fatal("Go itself kept the cookie; the fix may no longer be needed")
	}
	keepCookiesOnRedirect(c)
	get(t, c)
	if got := h.cookies["web.facebook.com"]; got != "c_user=1; xs=2" {
		t.Fatalf("web.facebook.com got Cookie %q", got)
	}
	for _, to := range []string{"https://evilfacebook.com/x", "http://web.facebook.com/x", "https://example.com/x"} {
		h := &hops{cookies: map[string]string{}}
		c := h.client(to)
		keepCookiesOnRedirect(c)
		get(t, c)
		for host, ck := range h.cookies {
			if host != "www.facebook.com" && ck != "" {
				t.Fatalf("%s got the cookie", to)
			}
		}
	}
}
