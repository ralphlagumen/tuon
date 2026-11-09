package main

import (
	_ "embed"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/ralph/tuon/internal/paths"
)

// releaseRepo is where releases come from. Updates never use a local
// clone: anything on this computer (including tuon's own sandboxed app)
// could have planted files, hooks or git settings there. Tests point it
// elsewhere with -ldflags "-X main.releaseRepo=…".
var releaseRepo = "https://github.com/ralphlagumen/tuon.git"

// version is the release this binary was built from ("dev" otherwise).
// install.sh and scripts/release.sh set it, the same for everyone, so
// release builds stay reproducible.
var version = "dev"

// releaseLDFlags: the same flags as install.sh and scripts/release.sh
// (which also build with CGO_ENABLED=0 and -buildvcs=false).
func releaseLDFlags(tag string) string { return "-s -w -buildid= -X main.version=" + tag }

//go:embed release_signers
var releaseSigners string

// trustedSigners: the public keys allowed to sign releases.
var trustedSigners = releaseSigners

// update installs the newest release signed by tuon's maintainer:
// fetched fresh from releaseRepo into a temporary folder with git's
// local configuration ignored, verified, exported and built there.
func update() error {
	for _, tool := range []string{"git", "go"} {
		if _, err := exec.LookPath(tool); err != nil {
			return fmt.Errorf("%s is needed to update tuon", tool)
		}
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if exe, err = filepath.EvalSymlinks(exe); err != nil {
		return err
	}
	// in a folder the sandboxed app can't write, so it can't swap the
	// source between the checks and the build; the tools' own temp files
	// (go's build folder, swiftc's objects, the signers list) go there too
	work, err := paths.WorkDir("update-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)
	toolTmp = filepath.Join(work, "tmp")
	if err := os.MkdirAll(toolTmp, 0o700); err != nil {
		return err
	}
	repo := filepath.Join(work, "repo.git")

	fmt.Println("\n  Fetching tuon's releases…")
	if out, err := safeGit(work, "clone", "--quiet", "--bare", "--no-tags", releaseRepo, repo); err != nil {
		return fmt.Errorf("couldn't fetch tuon:\n%s", indent(out))
	}
	if out, err := safeGit(repo, "fetch", "--quiet", "--tags", "origin"); err != nil {
		return fmt.Errorf("couldn't fetch releases:\n%s", indent(out))
	}
	tag, err := latestSignedRelease(repo)
	if err != nil {
		return err
	}
	app := filepath.Join(filepath.Dir(exe), appName())
	_, appErr := os.Stat(app) // missing after an update by v0.3.0, which only built tuon
	if tag == version && appErr == nil {
		fmt.Printf("  Already up to date (%s).\n", tag)
		return nil
	}
	if version != "dev" && newerTag(version, tag) && appErr == nil {
		fmt.Printf("  This tuon (%s) is newer than the latest release (%s); nothing to do.\n", version, tag)
		return nil
	}
	fmt.Printf("  Release %s, signed by tuon's maintainer.\n", tag)
	if version != "dev" { // what changed since the installed release
		if log, err := safeGit(repo, "log", "--oneline", "--no-decorate", version+".."+tag); err == nil && log != "" {
			fmt.Println("  New:")
			fmt.Println(indent(log))
		}
	}
	// dependency changes are where a supply-chain attack would come in:
	// show them and ask before building anything
	base := version
	if base == "dev" {
		base = previousTag(repo, tag)
	}
	if base != "" {
		if deps, err := safeGit(repo, "diff", base, tag, "--", "go.mod"); err == nil && deps != "" {
			fmt.Printf("\n  This release changes tuon's dependencies since %s (go.mod):\n", base)
			fmt.Println(indent(changedRequires(deps)))
			fmt.Println("\n  Only continue if you trust these changes (see the release notes).")
			fmt.Print("  Type yes to continue: ")
			if strings.TrimSpace(readLine()) != "yes" {
				return errors.New("update cancelled: dependency changes not accepted")
			}
		}
	}

	// exactly the signed tree, nothing else: no checkout filters, no extra files
	src := filepath.Join(work, "src")
	if err := os.MkdirAll(src, 0o700); err != nil {
		return err
	}
	archive := exec.Command("sh", "-c", `git -c core.hooksPath=/dev/null archive --format=tar "$1" | tar -x -C "$2"`, "sh", tag, src)
	archive.Dir = repo
	archive.Env = safeGitEnv()
	if out, err := archive.CombinedOutput(); err != nil {
		return fmt.Errorf("couldn't export %s:\n%s", tag, indent(strings.TrimSpace(string(out))))
	}
	verify := goCmd(src, "mod", "verify") // every module must match Go's checksums
	if out, err := verify.CombinedOutput(); err != nil {
		return fmt.Errorf("dependency check failed, not building:\n%s", indent(strings.TrimSpace(string(out))))
	}

	fmt.Println("  Building…")
	// the chat app first: a new launcher must never run next to an old app
	if err := buildInto(src, tag, "./cmd/tuon-app", app); err != nil {
		return err
	}
	if err := buildInto(src, tag, "./cmd/tuon", exe); err != nil {
		return err
	}
	fmt.Printf("  Updated %s and %s to %s.\n", exe, appName(), tag)
	if runtime.GOOS == "darwin" { // the Touch ID helper lives next to tuon
		helper := filepath.Join(filepath.Dir(exe), "tuon-keyhelper")
		sw := exec.Command("swiftc", "-O", "cmd/tuon-keyhelper/main.swift", "-o", helper+".new")
		sw.Dir = src
		sw.Env = withTmp(os.Environ())
		if out, err := sw.CombinedOutput(); err != nil {
			fmt.Printf("  Couldn't rebuild the Touch ID helper (kept the old one):\n%s\n", indent(strings.TrimSpace(string(out))))
		} else if err := os.Rename(helper+".new", helper); err != nil {
			return err
		}
	}
	// a tuon that's open keeps running the old version until it restarts
	if release, err := paths.Lock(); err == nil {
		release()
	} else if errors.Is(err, paths.ErrRunning) {
		fmt.Println("  tuon is open in another window: quit it and start it again to use the update.")
	}
	return nil
}

// buildInto builds pkg from the exported release and puts it at dst,
// replacing what's there only once the build worked.
func buildInto(src, tag, pkg, dst string) error {
	tmp := dst + ".new"
	build := goCmd(src, "build", "-trimpath", "-buildvcs=false", "-ldflags", releaseLDFlags(tag), "-o", tmp, pkg)
	if out, err := build.CombinedOutput(); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("build of %s failed:\n%s", pkg, indent(strings.TrimSpace(string(out))))
	}
	if runtime.GOOS == "windows" { // a running .exe can be renamed, not replaced
		_ = os.Remove(dst + ".old")
		if err := os.Rename(dst, dst+".old"); err != nil && !os.IsNotExist(err) {
			_ = os.Remove(tmp)
			return err
		}
	}
	if err := os.Rename(tmp, dst); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// latestSignedRelease finds the newest v* tag that is signed by a trusted
// key and whose signed name is its own name (so an old signed tag can't be
// passed off as a newer one). Others are skipped, loudly.
func latestSignedRelease(repo string) (string, error) {
	list, err := safeGit(repo, "tag", "--list", "v*", "--sort=-v:refname")
	if err != nil {
		return "", err
	}
	f, err := os.CreateTemp(toolTmp, "tuon-signers-")
	if err != nil {
		return "", err
	}
	defer os.Remove(f.Name())
	if _, err := f.WriteString(trustedSigners); err != nil {
		return "", err
	}
	f.Close()
	for _, t := range strings.Fields(list) {
		if _, err := safeGit(repo, "-c", "gpg.ssh.allowedSignersFile="+f.Name(), "verify-tag", t); err != nil {
			fmt.Printf("  Skipping %s: not signed by tuon's maintainer.\n", t)
			continue
		}
		if name, err := signedTagName(repo, t); err != nil || name != t {
			fmt.Printf("  Skipping %s: its signature is for a different tag (%s).\n", t, name)
			continue
		}
		return t, nil
	}
	return "", errors.New("no signed release found, so nothing was installed")
}

// signedTagName reads the "tag" line inside the (signed) tag object.
func signedTagName(repo, ref string) (string, error) {
	obj, err := safeGit(repo, "cat-file", "tag", ref)
	if err != nil {
		return "", err
	}
	for _, l := range strings.Split(obj, "\n") {
		if name, ok := strings.CutPrefix(l, "tag "); ok {
			return name, nil
		}
		if l == "" {
			break
		}
	}
	return "", errors.New("no tag name")
}

// previousTag is the release before tag (for showing dependency changes).
func previousTag(repo, tag string) string {
	out, err := safeGit(repo, "describe", "--tags", "--abbrev=0", "--match", "v*", tag+"^")
	if err != nil {
		return ""
	}
	return out
}

// newerTag reports whether version a sorts after b (v1.10.0 > v1.9.0).
func newerTag(a, b string) bool {
	pa, pb := splitVer(a), splitVer(b)
	for i := 0; i < len(pa) && i < len(pb); i++ {
		if pa[i] != pb[i] {
			return pa[i] > pb[i]
		}
	}
	return len(pa) > len(pb)
}

func splitVer(v string) []int {
	var out []int
	for _, p := range strings.Split(strings.TrimPrefix(v, "v"), ".") {
		n := 0
		for _, r := range p {
			if r < '0' || r > '9' {
				break
			}
			n = n*10 + int(r-'0')
		}
		out = append(out, n)
	}
	return out
}

// safeGit runs git with every way a repository or the environment could
// make it run other programs turned off: no hooks, no fsmonitor, the
// system ssh-keygen for signatures, no global or system config, and only
// https (or local files, for tests) as transports.
func safeGit(dir string, args ...string) (string, error) {
	full := append([]string{
		"-c", "core.hooksPath=/dev/null",
		"-c", "core.fsmonitor=false",
		"-c", "gpg.ssh.program=ssh-keygen",
		"-c", "protocol.allow=never", "-c", "protocol.https.allow=always", "-c", "protocol.file.allow=always",
	}, args...)
	cmd := exec.Command("git", full...)
	cmd.Dir = dir
	cmd.Env = safeGitEnv()
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func safeGitEnv() []string {
	env := []string{"GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_TERMINAL_PROMPT=0"}
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "GIT_") {
			continue // nothing from outside steers git
		}
		env = append(env, kv)
	}
	return withTmp(env)
}

// toolTmp is the update's private temp folder ("" before update starts).
var toolTmp string

// withTmp points the tools' temp files into toolTmp, away from the shared
// temp folder the sandboxed app can write.
func withTmp(env []string) []string {
	if toolTmp == "" {
		return env
	}
	out := []string{"TMPDIR=" + toolTmp, "GOTMPDIR=" + toolTmp}
	for _, kv := range env {
		if !strings.HasPrefix(kv, "TMPDIR=") && !strings.HasPrefix(kv, "GOTMPDIR=") {
			out = append(out, kv)
		}
	}
	return out
}

// goCmd runs go in the exported release with workspace files and local
// overrides ignored.
func goCmd(dir string, args ...string) *exec.Cmd {
	cmd := exec.Command("go", args...)
	cmd.Dir = dir
	// the same settings as scripts/release.sh, so the result matches the
	// published SHA256SUMS
	env := []string{"GOWORK=off", "GOFLAGS=-mod=readonly", "CGO_ENABLED=0"}
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "GOWORK=") || strings.HasPrefix(kv, "GOFLAGS=") || strings.HasPrefix(kv, "CGO_ENABLED=") {
			continue
		}
		env = append(env, kv)
	}
	cmd.Env = withTmp(env)
	return cmd
}

// changedRequires keeps the added/removed module lines of a go.mod diff.
func changedRequires(diff string) string {
	var out []string
	for _, l := range strings.Split(diff, "\n") {
		if (strings.HasPrefix(l, "+") || strings.HasPrefix(l, "-")) && !strings.HasPrefix(l, "+++") && !strings.HasPrefix(l, "---") {
			if t := strings.TrimSpace(l[1:]); t != "" {
				out = append(out, l[:1]+" "+t)
			}
		}
	}
	return strings.Join(out, "\n")
}

func indent(s string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = "    " + l
	}
	return strings.Join(lines, "\n")
}
