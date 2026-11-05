package store

import (
	"os"
	"testing"
	"time"
)

// TUON_BENCH_DB=<copy of tuon.db> go test ./internal/store -run TestBenchRealDB -v
// Times what tuon does with its database at start and while browsing, on
// a copy of real data: open (schema checks), the chat list, and the newest
// messages of every chat.
func TestBenchRealDB(t *testing.T) {
	path := os.Getenv("TUON_BENCH_DB")
	if path == "" {
		t.Skip("set TUON_BENCH_DB")
	}
	const rounds = 20
	var open, list, msgs time.Duration
	for i := 0; i < rounds; i++ {
		t0 := time.Now()
		s, err := benchOpen(path)
		if err != nil {
			t.Fatal(err)
		}
		t1 := time.Now()
		chats, err := s.Chats(Unread, "")
		if err != nil {
			t.Fatal(err)
		}
		all, _ := s.Chats(Space(""), "")
		chats = append(chats, all...)
		for _, sp := range s.Spaces() {
			c, _ := s.Chats(sp, "")
			chats = append(chats, c...)
		}
		t2 := time.Now()
		for _, c := range chats {
			if _, err := s.Messages(c.ID, 200, "1"); err != nil {
				t.Fatal(err)
			}
		}
		t3 := time.Now()
		s.Close()
		open, list, msgs = open+t1.Sub(t0), list+t2.Sub(t1), msgs+t3.Sub(t2)
		if i == 0 {
			t.Logf("chats listed: %d", len(chats))
		}
	}
	t.Logf("per round: open %v, chat lists %v, messages of every chat %v", open/rounds, list/rounds, msgs/rounds)
}
