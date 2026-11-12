package messenger

import (
	"testing"

	"go.mau.fi/mautrix-meta/pkg/messagix/socket"

	"github.com/ralph/tuon/internal/connector"
)

// @everyone goes out as Messenger's thread mention (type "t", ID = chat),
// next to a person tag, at UTF-16 offsets.
func TestLSMentionsEveryone(t *testing.T) {
	text := "😀 @Jen Cruz and @everyone lunch?"
	got := lsMentions(text, []connector.Mention{
		{UserID: "200", Name: "Jen Cruz"},
		{UserID: "10", Name: "everyone", Everyone: true, Members: []string{"200", "300"}},
	})
	want := socket.Mentions{
		{ID: 200, Offset: 3, Length: 9, Type: socket.MentionTypePerson},
		{ID: 10, Offset: 17, Length: 9, Type: socket.MentionTypeThread},
	}
	if len(got) != len(want) {
		t.Fatalf("got %+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("mention %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}
