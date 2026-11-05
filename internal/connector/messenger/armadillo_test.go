package messenger

import (
	"testing"

	"go.mau.fi/whatsmeow/proto/waArmadilloApplication"
	"go.mau.fi/whatsmeow/proto/waArmadilloXMA"
	"google.golang.org/protobuf/proto"

	"github.com/ralph/tuon/internal/connector"
)

func TestArmadilloSharedLink(t *testing.T) {
	video := &waArmadilloApplication.Armadillo{Payload: &waArmadilloApplication.Armadillo_Payload{
		Payload: &waArmadilloApplication.Armadillo_Payload_Content{Content: &waArmadilloApplication.Armadillo_Content{
			Content: &waArmadilloApplication.Armadillo_Content_ExtendedContentMessage{ExtendedContentMessage: &waArmadilloXMA.ExtendedContentMessage{
				TitleText:    proto.String("Funny video"),
				SubtitleText: proto.String("Facebook"),
				Ctas: []*waArmadilloXMA.ExtendedContentMessage_CTA{{
					NativeURL: proto.String("fb://video/1"), // not a web link: skipped
					ActionURL: proto.String("https://www.facebook.com/watch/?v=1"),
				}},
			}},
		}},
	}}
	text, media, ok := armadilloContent(video)
	if !ok || len(media) != 1 || media[0].Kind != connector.Link || media[0].URL != "https://www.facebook.com/watch/?v=1" || media[0].Title != "Funny video" {
		t.Fatalf("got %q %+v %v", text, media, ok)
	}

	other := &waArmadilloApplication.Armadillo{Payload: &waArmadilloApplication.Armadillo_Payload{
		Payload: &waArmadilloApplication.Armadillo_Payload_Content{Content: &waArmadilloApplication.Armadillo_Content{
			Content: &waArmadilloApplication.Armadillo_Content_CommonSticker_{CommonSticker: &waArmadilloApplication.Armadillo_Content_CommonSticker{}},
		}},
	}}
	if text, _, ok := armadilloContent(other); !ok || text == "" {
		t.Fatal("unknown kinds should become a note, not vanish")
	}
	if _, _, ok := armadilloContent(&waArmadilloApplication.Armadillo{}); ok {
		t.Fatal("a non-message (no content) should be skipped")
	}
}
