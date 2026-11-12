package main

import (
	"os"
	"strings"
)

// proxyVars are the environment variables Go's HTTP stack (and the
// libraries tuon uses) read to send traffic through a proxy.
var proxyVars = []string{"HTTPS_PROXY", "https_proxy", "HTTP_PROXY", "http_proxy", "ALL_PROXY", "all_proxy"}

// dropProxyEnv makes tuon talk to Meta directly: a proxy named in the
// environment (set by malware, a tool, or a shell profile) would see every
// connection and, with a certificate the Mac trusts, read them. It runs
// before any network use (Go reads these once) and returns what it removed.
func dropProxyEnv() []string {
	var dropped []string
	for _, v := range proxyVars {
		if val, ok := os.LookupEnv(v); ok {
			_ = os.Unsetenv(v)
			if strings.TrimSpace(val) != "" {
				dropped = append(dropped, v)
			}
		}
	}
	return dropped
}

// proxyWarning: the header note when a proxy setting was ignored.
func proxyWarning(dropped []string) string {
	if len(dropped) == 0 {
		return ""
	}
	return "ignored " + strings.Join(dropped, ", ") + ": tuon connects to Meta directly"
}
