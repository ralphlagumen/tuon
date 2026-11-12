package main

import (
	"os"
	"strings"
	"testing"
)

func TestDropProxyEnv(t *testing.T) {
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:9")
	t.Setenv("all_proxy", "socks5://127.0.0.1:9")
	t.Setenv("http_proxy", "") // set but empty: removed, not reported
	got := dropProxyEnv()
	for _, v := range proxyVars {
		if _, ok := os.LookupEnv(v); ok {
			t.Errorf("%s still set", v)
		}
	}
	if w := proxyWarning(got); !strings.Contains(w, "HTTPS_PROXY") || !strings.Contains(w, "all_proxy") || strings.Contains(w, "http_proxy") {
		t.Errorf("warning = %q", w)
	}
	if proxyWarning(dropProxyEnv()) != "" {
		t.Error("nothing set: no warning")
	}
}
