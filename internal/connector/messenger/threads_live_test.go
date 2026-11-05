package messenger

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/ralph/tuon/internal/connector"

	"github.com/ralph/tuon/internal/auth"
	"github.com/ralph/tuon/internal/store"
)

// TUON_LIVE=1 go test ./internal/connector/messenger -run TestLiveThreads -v
// Read-only: pages the chat list for sync groups 1 (plain) and 95
// (encrypted) and prints counts per folder (parent thread key) and where
// TUON_CHAT sits. No names or content. tuon must be closed.
func TestLiveThreads(t *testing.T) {
	if os.Getenv("TUON_LIVE") == "" {
		t.Skip("set TUON_LIVE=1")
	}
	release, err := store.Lock()
	if err != nil {
		t.Skip(err)
	}
	defer release()
	st, err := store.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	creds, err := auth.Load(store.Dir())
	if err != nil {
		t.Fatal(err)
	}
	c := New(creds, st.DB, zerolog.New(zerolog.NewTestWriter(t)).Level(zerolog.ErrorLevel))
	c.fetchOnce.Do(func() {}) // run the paging by hand below
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	if err := c.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	var mu sync.Mutex
	chats := map[string]bool{}
	go func() {
		for e := range c.Events() {
			if ce, ok := e.(connector.ChatEvent); ok {
				mu.Lock()
				chats[ce.Chat.ID] = true
				mu.Unlock()
			}
		}
	}()
	count := func() int { mu.Lock(); defer mu.Unlock(); return len(chats) }
	time.Sleep(4 * time.Second)
	t.Logf("chats after start: %d", count())
	want := os.Getenv("TUON_CHAT")
	for _, group := range []int64{1, 95} {
		seen := map[int64]int{}
		total := 0
		for page := 1; page <= 6; page++ {
			ks, tbl, err := c.cli.FetchMoreThreads(ctx, group)
			if err != nil {
				t.Logf("group %d page %d: error %v", group, page, err)
				break
			}
			if tbl == nil {
				t.Logf("group %d page %d: key store says no more", group, page)
				break
			}
			for _, th := range tbl.LSDeleteThenInsertThread {
				seen[th.ParentThreadKey]++
				total++
				if want != "" && id(th.ThreadKey) == want {
					t.Logf("  TUON_CHAT found: group %d, parent key %d", group, th.ParentThreadKey)
				}
			}
			t.Logf("group %d page %d: threads=%d tables=%v more=%v min_key=%d", group, page,
				len(tbl.LSDeleteThenInsertThread)+len(tbl.LSUpdateOrInsertThread), tableNames(tbl), ks != nil && ks.HasMoreBefore, keyMin(ks))
			time.Sleep(3 * time.Second)
			mu.Lock()
			found := chats[want]
			mu.Unlock()
			t.Logf("  chats seen over the connection so far: %d (TUON_CHAT seen: %v)", count(), found)
		}
		t.Logf("group %d: %d threads by parent key %v", group, total, seen)
	}
}

// TUON_LIVE=1 go test ./internal/connector/messenger -run TestLivePaging -v
// Runs tuon's own chat-list paging and counts the chats that arrive.
func TestLivePaging(t *testing.T) {
	if os.Getenv("TUON_LIVE") == "" {
		t.Skip("set TUON_LIVE=1")
	}
	release, err := store.Lock()
	if err != nil {
		t.Skip(err)
	}
	defer release()
	st, err := store.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	creds, err := auth.Load(store.Dir())
	if err != nil {
		t.Fatal(err)
	}
	c := New(creds, st.DB, zerolog.New(zerolog.NewTestWriter(t)).Level(zerolog.WarnLevel))
	c.fetchOnce.Do(func() {}) // run it by hand below
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	if err := c.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	var mu sync.Mutex
	chats := map[string]bool{}
	go func() {
		for e := range c.Events() {
			if ce, ok := e.(connector.ChatEvent); ok {
				mu.Lock()
				chats[ce.Chat.ID] = true
				mu.Unlock()
			}
		}
	}()
	time.Sleep(4 * time.Second)
	mu.Lock()
	start := len(chats)
	mu.Unlock()
	c.fetchMoreThreads()
	mu.Lock()
	defer mu.Unlock()
	t.Logf("chats: %d at start, %d after paging", start, len(chats))
}
