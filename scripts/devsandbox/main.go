// devsandbox runs a command (go test, govulncheck) inside a macOS sandbox,
// because those run dependency code: a bad dependency then can't read your
// home folder, reach your SSH agent or the internet, or change the repo or
// Go's normal build cache. It imports only the standard library and
// internal/sandbox, so building it runs no dependency code.
//
//	go run ./scripts/devsandbox [--net] [--live] -- go test ./...
//
// --net: internet (govulncheck's database, TUON_LIVE tests).
// --live: also tuon's real folder (TUON_LIVE tests; implies --net).
package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/ralph/tuon/internal/sandbox"
)

func main() {
	net, live := false, false
	args := os.Args[1:]
	for len(args) > 0 && args[0] != "--" {
		switch args[0] {
		case "--net":
			net = true
		case "--live":
			net, live = true, true
		default:
			die("unknown flag " + args[0])
		}
		args = args[1:]
	}
	if len(args) < 2 {
		die("usage: devsandbox [--net] [--live] -- command args…")
	}
	args = args[1:]
	if runtime.GOOS != "darwin" {
		die("no sandbox on " + runtime.GOOS + ": run the command yourself, knowing dependency code runs with your rights")
	}
	home, err := os.UserHomeDir()
	must(err)
	repo, err := os.Getwd()
	must(err)
	gopath := goEnv("GOPATH")
	cache := filepath.Join(home, "Library", "Caches", "tuon-dev-gocache")
	must(os.MkdirAll(cache, 0o700))
	real := func(p string) string {
		if r, err := filepath.EvalSymlinks(p); err == nil {
			return r
		}
		return p
	}
	tmp := real(os.TempDir())
	profile := sandbox.DevProfile(real(home), tmp, real(repo), real(gopath), real(cache), net, live)

	env := []string{"GOCACHE=" + cache, "GOFLAGS=-mod=readonly", "GOTELEMETRY=off", "GOPROXY=off", "TUON_DEVSANDBOX=" + mode(net, live)}
	if net {
		env[3] = "GOPROXY=" + goEnv("GOPROXY")
	}
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		switch {
		case k == "GOCACHE", k == "GOFLAGS", k == "GOTELEMETRY", k == "GOPROXY", k == "TUON_DEVSANDBOX":
		case k == "HOME", k == "PATH", k == "TMPDIR", k == "USER", k == "LANG", k == "GOPATH", k == "GOROOT",
			k == "GOTOOLCHAIN", k == "__CF_USER_TEXT_ENCODING",
			strings.HasPrefix(k, "TERM"), strings.HasPrefix(k, "LC_"), strings.HasPrefix(k, "TUON_"):
			env = append(env, kv)
		}
	}
	cmd := exec.Command("/usr/bin/sandbox-exec", append([]string{"-p", profile}, args...)...)
	cmd.Env = env
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		if e, ok := err.(*exec.ExitError); ok {
			os.Exit(e.ExitCode())
		}
		die(err.Error())
	}
}

// mode tells TestDevSandboxHolds what to expect.
func mode(net, live bool) string {
	switch {
	case live:
		return "live"
	case net:
		return "net"
	}
	return "offline"
}

func goEnv(k string) string {
	out, err := exec.Command("go", "env", k).Output()
	must(err)
	return strings.TrimSpace(string(out))
}

func must(err error) {
	if err != nil {
		die(err.Error())
	}
}

func die(msg string) {
	fmt.Fprintln(os.Stderr, "devsandbox:", msg)
	os.Exit(2)
}
