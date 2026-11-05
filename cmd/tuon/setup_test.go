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
