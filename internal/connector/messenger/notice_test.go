package messenger

import (
	"context"
	"testing"
	"time"

	"github.com/ralph/tuon/internal/connector"
)

// tuon's own notices never reach Messenger as read receipts: with only a
// notice, MarkRead returns before touching the chat ID or the network.
func TestMarkReadSkipsLocalNotices(t *testing.T) {
	c := &Conn{}
	n := connector.KeyChangeNotice("not-a-number", "200", time.Now())
	if err := c.MarkRead(context.Background(), "not-a-number", []connector.Message{n}); err != nil {
		t.Fatalf("a notice alone should be dropped, got %v", err)
	}
	if err := c.MarkRead(context.Background(), "not-a-number", []connector.Message{n, {ID: "real", Time: time.Now()}}); err == nil {
		t.Fatal("a real message should still be processed")
	}
}
