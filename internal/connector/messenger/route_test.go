package messenger

import (
	"context"
	"strings"
	"testing"

	"github.com/rs/zerolog"

	"github.com/ralph/tuon/internal/auth"
	"github.com/ralph/tuon/internal/connector"
)

// A chat the store knows is encrypted must go the encrypted way, even right
// after a restart (sending it the plain way fails with "Thread Disabled").
func TestKnownEncryptedChatUsesEncryptedRoute(t *testing.T) {
	c := New(auth.Creds{}, nil, zerolog.Nop())
	c.KnowChat(connector.Chat{ID: "1264755401", Encrypted: true})
	err := c.Send(context.Background(), "1264755401", "hi", nil, nil)
	if err == nil || !strings.Contains(err.Error(), "encrypted chats are still starting") {
		t.Fatalf("expected the encrypted route, got %v", err)
	}
	if !c.isEncrypted(1264755401) {
		t.Fatal("chat should be remembered as encrypted")
	}
}
