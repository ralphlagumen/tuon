package messenger

import "testing"

func TestCleanURL(t *testing.T) {
	cases := map[string]string{
		"/story.php?id=1": "https://www.facebook.com/story.php?id=1",
		"https://l.facebook.com/l.php?u=https%3A%2F%2Fwww.reddit.com%2Fr%2Fx&h=abc": "https://www.reddit.com/r/x",
		"https://www.reddit.com/r/x": "https://www.reddit.com/r/x",
		"":                           "",
	}
	for in, want := range cases {
		if got := cleanURL(in); got != want {
			t.Errorf("cleanURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestMediaOnlyFromMeta(t *testing.T) {
	for _, ok := range []string{
		"https://scontent.xx.fbcdn.net/v/t1/a.jpg",
		"https://www.facebook.com/x.jpg",
		"https://cdn.fbsbx.com/v/a",
	} {
		if err := checkMediaURL(ok); err != nil {
			t.Errorf("%s: %v", ok, err)
		}
	}
	for _, bad := range []string{
		"http://scontent.xx.fbcdn.net/a.jpg", // not https
		"https://evil.com/a.jpg",
		"https://fbcdn.net.evil.com/a.jpg",
		"file:///etc/passwd",
	} {
		if err := checkMediaURL(bad); err == nil {
			t.Errorf("%s should be refused", bad)
		}
	}
}
