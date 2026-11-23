// depsreview lists every module a dependency change touches, so a bump is
// reviewed as a whole and not only the "watched" modules (DEPENDENCIES.md):
// old → new version, whether tuon links it, its age, and where to read the
// diff. It refuses versions younger than the cooldown (14 days): a
// hijacked maintainer account usually ships a fresh release, and most are
// caught within days. Standard library only, so running it runs no
// dependency code.
//
//	go run ./scripts/depsreview [--base REV] [--allow mod@ver]… [--days N]
//
// Age is the Go module proxy's time for the version, which is the commit
// time: the author sets it, so a careful attacker could backdate it.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"
)

// watched: read these diffs line by line (they handle the network, the
// session or encryption). Everything else: skim the diff, keep the cooldown.
var watched = regexp.MustCompile(`^(go\.mau\.fi/|github\.com/beeper/|github\.com/imroc/req|golang\.org/x/crypto|github\.com/ncruces/|lukechampine\.com/|github\.com/zalando/go-keyring)`)

type change struct {
	Mod, Old, New string
	Replace       bool
	Linked        bool
	Watched       bool
	Age           time.Duration
	AgeErr        error
	Allowed       bool
}

func main() {
	base := flag.String("base", "HEAD", "git revision to compare go.mod against")
	days := flag.Int("days", 14, "cooldown: refuse versions younger than this many days")
	var allow multi
	flag.Var(&allow, "allow", "mod@version exempt from the cooldown (a vuln fix you're hit by); repeatable")
	flag.Parse()

	oldMod, err := exec.Command("git", "show", *base+":go.mod").Output()
	must(err)
	newMod, err := os.ReadFile("go.mod")
	must(err)
	changes := diff(parse(string(oldMod)), parse(string(newMod)))
	if len(changes) == 0 {
		fmt.Println("== no module versions changed since", *base)
		return
	}
	linked := linkedModules()
	cooldown := time.Duration(*days) * 24 * time.Hour
	young := 0
	for i := range changes {
		c := &changes[i]
		c.Linked = linked[c.Mod]
		c.Watched = watched.MatchString(c.Mod)
		if c.New != "" {
			c.Age, c.AgeErr = age(c.Mod, c.New, time.Now())
			c.Allowed = allow.has(c.Mod + "@" + c.New)
			if !c.Allowed && (c.AgeErr != nil || c.Age < cooldown) {
				young++
			}
		}
	}
	report(os.Stdout, changes, cooldown)
	if young > 0 {
		fmt.Printf("\n== REFUSED: %d version(s) younger than %d days (or of unknown age). Wait, or pass --allow mod@ver for a vuln fix you're hit by.\n", young, *days)
		os.Exit(1)
	}
}

// parse reads module → version from go.mod's require and replace lines
// (a replace counts as the module's version, marked with "=>").
func parse(gomod string) map[string]string {
	out := map[string]string{}
	inReq := false
	for _, line := range strings.Split(gomod, "\n") {
		line = strings.TrimSpace(strings.SplitN(line, "//", 2)[0])
		switch {
		case line == "require (":
			inReq = true
			continue
		case line == ")":
			inReq = false
			continue
		case strings.HasPrefix(line, "require "):
			line = strings.TrimPrefix(line, "require ")
		case strings.HasPrefix(line, "replace "):
			if l, r, ok := strings.Cut(strings.TrimPrefix(line, "replace "), "=>"); ok {
				if f := strings.Fields(r); len(f) == 2 {
					out["=> "+strings.Fields(l)[0]] = f[0] + " " + f[1]
				}
			}
			continue
		case !inReq:
			continue
		}
		if f := strings.Fields(line); len(f) == 2 {
			out[f[0]] = f[1]
		}
	}
	return out
}

func diff(old, new map[string]string) []change {
	var out []change
	for m, nv := range new {
		if ov := old[m]; ov != nv {
			out = append(out, toChange(m, ov, nv))
		}
	}
	for m, ov := range old {
		if _, ok := new[m]; !ok {
			out = append(out, toChange(m, ov, ""))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Mod < out[j].Mod })
	return out
}

// toChange turns a replace entry ("=> X" → "Y ver") into Y's change.
func toChange(m, ov, nv string) change {
	if !strings.HasPrefix(m, "=> ") {
		return change{Mod: m, Old: ov, New: nv}
	}
	c := change{Replace: true, Mod: strings.TrimPrefix(m, "=> ")}
	split := func(s string) (string, string) {
		f := strings.Fields(s)
		if len(f) == 2 {
			return f[0], f[1]
		}
		return "", ""
	}
	om, ov2 := split(ov)
	nm, nv2 := split(nv)
	if nm != "" {
		c.Mod = nm
	} else if om != "" {
		c.Mod = om
	}
	c.Old, c.New = ov2, nv2
	return c
}

func linkedModules() map[string]bool {
	out := map[string]bool{}
	b, err := exec.Command("go", "list", "-deps", "-f", "{{with .Module}}{{.Path}}{{end}}", "./cmd/tuon-app", "./cmd/tuon").Output()
	if err != nil {
		return out
	}
	for _, m := range strings.Fields(string(b)) {
		out[m] = true
	}
	return out
}

// age: how old mod@ver is, by the module proxy's time.
func age(mod, ver string, now time.Time) (time.Duration, error) {
	u := "https://proxy.golang.org/" + escape(mod) + "/@v/" + escape(ver) + ".info"
	cl := &http.Client{Timeout: 15 * time.Second}
	resp, err := cl.Get(u)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return 0, fmt.Errorf("proxy: %s", resp.Status)
	}
	var info struct{ Time time.Time }
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return 0, err
	}
	return now.Sub(info.Time), nil
}

// escape: the module proxy's case encoding (Upper → !lower).
func escape(s string) string {
	var b strings.Builder
	for _, r := range s {
		if unicode.IsUpper(r) {
			b.WriteByte('!')
			b.WriteRune(unicode.ToLower(r))
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// diffURL: where to read old...new (GitHub compare for known hosts).
func diffURL(c change) string {
	repo := ""
	switch {
	case strings.HasPrefix(c.Mod, "github.com/"):
		p := strings.Split(c.Mod, "/")
		if len(p) >= 3 {
			repo = strings.Join(p[:3], "/")
		}
	case strings.HasPrefix(c.Mod, "golang.org/x/"):
		repo = "github.com/golang/" + strings.Split(strings.TrimPrefix(c.Mod, "golang.org/x/"), "/")[0]
	case strings.HasPrefix(c.Mod, "go.mau.fi/"):
		repo = map[string]string{"go.mau.fi/whatsmeow": "github.com/tulir/whatsmeow", "go.mau.fi/mautrix-meta": "github.com/mautrix/meta",
			"go.mau.fi/util": "github.com/mautrix/go-util", "go.mau.fi/libsignal": "github.com/tulir/libsignal-protocol-go"}[c.Mod]
	}
	if c.New == "" {
		return "(removed: nothing to read)"
	}
	if c.Old == "" { // a new module: read all of it
		return "new module, read it all: $(go mod download -json " + c.Mod + "@" + c.New + " | grep '\"Dir\"' | cut -d'\"' -f4)"
	}
	if repo == "" {
		return "local: diff -ru $(go mod download -json " + c.Mod + "@" + c.Old + " | grep '\"Dir\"' | cut -d'\"' -f4) $(go mod download -json " + c.Mod + "@" + c.New + " | grep '\"Dir\"' | cut -d'\"' -f4)"
	}
	return "https://" + repo + "/compare/" + ref(c.Old) + "..." + ref(c.New)
}

// ref: a tag, or a pseudo-version's commit hash.
func ref(v string) string {
	v = strings.TrimSuffix(v, "+incompatible")
	if p := strings.Split(v, "-"); len(p) >= 3 && len(p[len(p)-1]) == 12 {
		return p[len(p)-1]
	}
	return v
}

func report(w *os.File, cs []change, cooldown time.Duration) {
	fmt.Fprintf(w, "== %d module(s) changed\n", len(cs))
	for _, c := range cs {
		tags := []string{}
		if c.Watched {
			tags = append(tags, "WATCHED: read every line")
		}
		if c.Linked {
			tags = append(tags, "linked into tuon")
		} else {
			tags = append(tags, "not linked")
		}
		if c.Replace {
			tags = append(tags, "replace")
		}
		ageText := ""
		switch {
		case c.New == "":
			ageText = "removed"
		case c.AgeErr != nil:
			ageText = "age unknown (" + c.AgeErr.Error() + ")"
		default:
			ageText = fmt.Sprintf("%d days old", int(c.Age.Hours()/24))
			if c.Age < cooldown {
				ageText += " — TOO NEW"
			}
		}
		if c.Allowed {
			ageText += " (allowed)"
		}
		old := c.Old
		if old == "" {
			old = "(new)"
		}
		fmt.Fprintf(w, "\n  %s\n    %s → %s · %s · %s\n    diff: %s\n", c.Mod, old, orNone(c.New), ageText, strings.Join(tags, " · "), diffURL(c))
	}
}

func orNone(s string) string {
	if s == "" {
		return "(gone)"
	}
	return s
}

type multi []string

func (m *multi) String() string     { return strings.Join(*m, ",") }
func (m *multi) Set(v string) error { *m = append(*m, v); return nil }
func (m multi) has(v string) bool {
	for _, x := range m {
		if x == v {
			return true
		}
	}
	return false
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "depsreview:", err)
		os.Exit(2)
	}
}
