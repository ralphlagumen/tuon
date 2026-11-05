package store

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ralph/tuon/internal/connector"
)

func TestOpenEncryptedConvertsPlainFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tuon.db")
	s, err := OpenPath(path) // an install from before: plain
	if err != nil {
		t.Fatal(err)
	}
	_ = s.UpsertContact("42", "Visible Name")
	_ = s.UpsertChat(connector.Chat{ID: "42", Name: "Visible Chat", LastActivity: time.Now()})
	_, _ = s.AddMessage(connector.Message{ID: "m1", ChatID: "42", SenderID: "42", Text: "hello there", Time: time.Now()}, false)
	s.Close()
	if !IsPlain(path) {
		t.Fatal("setup: expected a plain file")
	}

	key := bytes.Repeat([]byte{7}, 32)
	s, err = OpenEncrypted(path, key)
	if err != nil {
		t.Fatal(err)
	}
	msgs, _ := s.Messages("42", 10, "1")
	c, _ := s.Chat("42")
	s.Close()
	if len(msgs) != 1 || msgs[0].Text != "hello there" || c.Display != "Visible Chat" {
		t.Fatalf("data lost: %+v %+v", msgs, c)
	}
	raw, _ := os.ReadFile(path)
	for _, w := range []string{"Visible Name", "Visible Chat", "hello there", "SQLite format 3", "tuon_chats"} {
		if bytes.Contains(raw, []byte(w)) {
			t.Fatalf("encrypted file still contains %q", w)
		}
	}
	for _, f := range []string{path + ".enc", path + "-wal"} {
		if st, err := os.Stat(f); err == nil && st.Size() > 0 && f == path+".enc" {
			t.Fatalf("%s left behind", f)
		}
	}
	// opens again with the key, refuses another key, and isn't converted twice
	if s, err = OpenEncrypted(path, key); err != nil {
		t.Fatal(err)
	}
	s.Close()
	if _, err := OpenEncrypted(path, bytes.Repeat([]byte{8}, 32)); err == nil {
		t.Fatal("opened with the wrong key")
	} else if bytes.Contains([]byte(err.Error()), []byte("0808")) {
		t.Fatalf("error shows the key: %v", err)
	}
}
