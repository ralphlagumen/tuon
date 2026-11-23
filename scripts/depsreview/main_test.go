package main

import (
	"strings"
	"testing"
)

func TestDiffGoMod(t *testing.T) {
	old := `module x

go 1.27.4

require (
	go.mau.fi/whatsmeow v0.0.0-20261014094007-f16f9ca30d6a
	github.com/stretchr/testify v1.12.0 // indirect
	github.com/gone/away v1.0.0 // indirect
)

require golang.org/x/crypto v0.58.0

replace github.com/imroc/req/v3 => github.com/beeper/req/v3 v3.0.0-20260925135712-b49593750e56
`
	new := strings.NewReplacer(
		"v1.12.0", "v1.12.1",
		"\tgithub.com/gone/away v1.0.0 // indirect\n", "\tgithub.com/new/thing v0.1.0\n",
		"20260925135712-b49593750e56", "20261112020631-42bebc180e13",
	).Replace(old)
	cs := diff(parse(old), parse(new))
	var got []string
	for _, c := range cs {
		got = append(got, c.Mod+" "+c.Old+">"+c.New)
	}
	want := "github.com/beeper/req/v3 v3.0.0-20260925135712-b49593750e56>v3.0.0-20261112020631-42bebc180e13|" +
		"github.com/gone/away v1.0.0>|github.com/new/thing >v0.1.0|github.com/stretchr/testify v1.12.0>v1.12.1"
	if strings.Join(got, "|") != want {
		t.Fatalf("changes:\n%s\nwant\n%s", strings.Join(got, "|"), want)
	}
	if !watched.MatchString("github.com/beeper/req/v3") || watched.MatchString("github.com/stretchr/testify") {
		t.Fatal("watched list")
	}
}

func TestDiffURL(t *testing.T) {
	for c, want := range map[change]string{
		{Mod: "github.com/stretchr/testify", Old: "v1.12.0", New: "v1.12.1"}:                                               "https://github.com/stretchr/testify/compare/v1.12.0...v1.12.1",
		{Mod: "go.mau.fi/whatsmeow", Old: "v0.0.0-20261014094007-f16f9ca30d6a", New: "v0.0.0-20261120000000-0123456789ab"}: "https://github.com/tulir/whatsmeow/compare/f16f9ca30d6a...0123456789ab",
		{Mod: "golang.org/x/crypto", Old: "v0.58.0", New: "v0.59.0"}:                                                       "https://github.com/golang/crypto/compare/v0.58.0...v0.59.0",
	} {
		if got := diffURL(c); got != want {
			t.Errorf("%s: %s", c.Mod, got)
		}
	}
	if got := diffURL(change{Mod: "github.com/new/thing", New: "v0.1.0"}); !strings.HasPrefix(got, "new module, read it all") || strings.Contains(got, "@ ") {
		t.Errorf("new module: %s", got)
	}
	if !strings.HasPrefix(diffURL(change{Mod: "lukechampine.com/adiantum", Old: "v1.1.1", New: "v1.1.2"}), "local: diff -ru") {
		t.Error("unknown host should fall back to a local diff")
	}
}

func TestEscape(t *testing.T) {
	if got := escape("github.com/BurntSushi/toml"); got != "github.com/!burnt!sushi/toml" {
		t.Fatal(got)
	}
}
