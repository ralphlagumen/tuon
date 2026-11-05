package main

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ralph/tuon/internal/auth"
	"github.com/ralph/tuon/internal/store"
)

func TestSwitchAccountClearsData(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // Windows
	st, err := store.Open()
	if err != nil {
		t.Fatal(err)
	}
	_ = st.SetSetting(store.SettingAccount, "111")
	st.Close()
	db := filepath.Join(store.Dir(), "tuon.db")

	same := auth.Creds{Cookies: map[string]string{"c_user": "111"}}
	if ok, err := sameAccount(same); !ok || err != nil {
		t.Fatalf("same account: ok=%v err=%v", ok, err)
	}

	other := auth.Creds{Cookies: map[string]string{"c_user": "222"}}
	stdin = bufio.NewReader(strings.NewReader("n\n"))
	if ok, _ := sameAccount(other); ok {
		t.Fatal("saying no must keep the old data")
	}
	if _, err := os.Stat(db); err != nil {
		t.Fatal("data deleted after no")
	}

	stdin = bufio.NewReader(strings.NewReader("y\n"))
	if ok, err := sameAccount(other); !ok || err != nil {
		t.Fatalf("yes: ok=%v err=%v", ok, err)
	}
	if _, err := os.Stat(db); !os.IsNotExist(err) {
		t.Fatal("old account's database still there")
	}
}

func TestPinDigits(t *testing.T) {
	for in, want := range map[string]string{
		"123456":                   "123456",
		"12 34 56":                 "123456",
		"123-456":                  "123456",
		"\x1b[200~123456\x1b[201~": "123456",
		"12345":                    "12345",
	} {
		if got := pinDigits(in); got != want {
			t.Errorf("pinDigits(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNewerTag(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{{"v1.10.0", "v1.9.0", true}, {"v0.2.0", "v0.2.0", false}, {"v0.2.0", "v0.3.0", false}, {"v1.0.0", "v0.9.9", true}} {
		if got := newerTag(c.a, c.b); got != c.want {
			t.Errorf("newerTag(%s, %s) = %v", c.a, c.b, got)
		}
	}
}
