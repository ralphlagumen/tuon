package messenger

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/ralph/tuon/internal/auth"
	"github.com/ralph/tuon/internal/store"
)

// TUON_LIVE=1 TUON_CHAT=<id> go test ./internal/connector/messenger -run TestLiveHistory -v
// Read-only: fetches one page of a chat's encrypted backup and prints
// counts and times, never message text.
func TestLiveHistory(t *testing.T) {
	chat := os.Getenv("TUON_CHAT")
	if os.Getenv("TUON_LIVE") == "" || chat == "" {
		t.Skip("set TUON_LIVE=1 TUON_CHAT=<id>")
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
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	if err := c.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	go func() {
		for e := range c.Events() {
			_ = e
		}
	}()
	t.Log("history device:", c.HasHistory())
	dev := c.historyDevice()
	if dev == nil {
		t.Fatal("no history device")
	}
	h := c.cli.GetSecureHistoryClient()
	page, err := h.FetchMessages(ctx, dev, chat, true, time.Now().Add(time.Minute).UnixMilli(), 50)
	if err != nil {
		t.Fatal("fetch:", err)
	}
	t.Logf("fetched=%d missing=%v exception=%v range=%+v", len(page.Messages), page.ThreadMissing, page.Exception, page.Range)
	msgs, err := page.Decrypt(dev, chat, c.self, false)
	if err != nil {
		t.Fatal("decrypt:", err)
	}
	for _, m := range msgs {
		if m == nil || m.Message == nil {
			t.Log("nil message")
			continue
		}
		meta := m.Message.Message.GetMetadata()
		_, ok := c.archiveMessage(chat, m.Message)
		t.Logf("%s id=%s sender=%s payload=%T shown=%v", time.UnixMilli(m.Message.TimestampMS).Format("15:04:05"),
			meta.GetMessageID(), meta.GetSenderID(), m.Message.Message.GetPayload(), ok)
	}
}
