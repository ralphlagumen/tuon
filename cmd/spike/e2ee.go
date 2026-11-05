package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/rs/zerolog"
	"go.mau.fi/mautrix-meta/pkg/messagix"
	"go.mau.fi/mautrix-meta/pkg/messagix/methods"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waCommon"
	"go.mau.fi/whatsmeow/proto/waConsumerApplication"
	"go.mau.fi/whatsmeow/proto/waMsgApplication"
	"go.mau.fi/whatsmeow/store/sqlstore"
	waTypes "go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"
	"google.golang.org/protobuf/proto"
	_ "modernc.org/sqlite"
)

// runE2EE registers (once) an encrypted device for this account, connects to the
// encrypted socket, prints incoming 1:1 messages, and optionally sends one.
func runE2EE(ctx context.Context, cli *messagix.Client, fbid int64, sendTo int64, text string, listen time.Duration, log zerolog.Logger) {
	home, _ := os.UserHomeDir()
	dir := filepath.Join(home, ".config", "tuon")
	_ = os.MkdirAll(dir, 0o700)
	dbPath := filepath.Join(dir, "tuon.db")

	db, err := sql.Open("sqlite", "file:"+dbPath+"?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		fail("open db: " + err.Error())
	}
	defer db.Close()
	_ = os.Chmod(dbPath, 0o600)

	container := sqlstore.NewWithDB(db, "sqlite3", waLog.Zerolog(log.With().Str("c", "store").Logger()))
	if err := container.Upgrade(ctx); err != nil {
		fail("db upgrade: " + err.Error())
	}
	dev, err := container.GetFirstDevice(ctx)
	if err != nil {
		fail("load device: " + err.Error())
	}
	cli.SetDevice(dev)

	if dev.ID == nil {
		fmt.Println("registering new encrypted device (one time)...")
		if err := cli.RegisterE2EE(ctx, fbid); err != nil {
			fail("register e2ee: " + err.Error())
		}
		if err := dev.Save(ctx); err != nil {
			fail("save device: " + err.Error())
		}
		fmt.Printf("registered as device %d\n", dev.ID.Device)
	} else {
		fmt.Printf("using saved encrypted device %d\n", dev.ID.Device)
	}

	wa, err := cli.PrepareE2EEClient()
	if err != nil {
		fail("prepare e2ee: " + err.Error())
	}
	connected := make(chan struct{}, 1)
	wa.AddEventHandler(func(raw any) {
		switch evt := raw.(type) {
		case *events.Connected:
			select {
			case connected <- struct{}{}:
			default:
			}
		case *events.FBMessage:
			fmt.Printf("  <- [e2ee %s] %s: %s\n", evt.Info.Chat.User, evt.Info.Sender.User, e2eeText(evt))
		case *events.OfflineSyncCompleted:
			fmt.Printf("  (offline sync done: %d queued)\n", evt.Count)
		}
	})
	if err := wa.Connect(); err != nil {
		fail("e2ee connect: " + err.Error())
	}
	defer wa.Disconnect()

	select {
	case <-connected:
		fmt.Println("encrypted socket connected")
	case <-time.After(30 * time.Second):
		fail("encrypted socket did not connect in 30s (try -debug)")
	}

	if sendTo != 0 {
		sendE2EE(ctx, wa, sendTo, text)
	}
	fmt.Printf("listening %s for encrypted messages...\n", listen)
	select {
	case <-time.After(listen):
	case <-ctx.Done():
	}
}

func sendE2EE(ctx context.Context, wa *whatsmeow.Client, userID int64, text string) {
	chat := waTypes.JID{User: strconv.FormatInt(userID, 10), Server: waTypes.MessengerServer}
	msg := &waConsumerApplication.ConsumerApplication{
		Payload: &waConsumerApplication.ConsumerApplication_Payload{
			Payload: &waConsumerApplication.ConsumerApplication_Payload_Content{
				Content: &waConsumerApplication.ConsumerApplication_Content{
					Content: &waConsumerApplication.ConsumerApplication_Content_MessageText{
						MessageText: &waCommon.MessageText{Text: proto.String(text)},
					},
				},
			},
		},
	}
	_, err := wa.SendFBMessage(ctx, chat, msg, &waMsgApplication.MessageApplication_Metadata{}, whatsmeow.SendRequestExtra{
		ID: strconv.FormatInt(methods.GenerateEpochID(), 10),
	})
	if err != nil {
		fail("e2ee send: " + err.Error())
	}
	fmt.Printf("sent encrypted %q to %d\n", text, userID)
}

func e2eeText(evt *events.FBMessage) string {
	app, ok := evt.Message.(*waConsumerApplication.ConsumerApplication)
	if !ok {
		return fmt.Sprintf("[%T]", evt.Message)
	}
	c := app.GetPayload().GetContent()
	switch {
	case c.GetMessageText() != nil:
		return c.GetMessageText().GetText()
	case c.GetExtendedTextMessage() != nil:
		return c.GetExtendedTextMessage().GetText().GetText()
	case c.GetImageMessage() != nil:
		return "[photo]"
	case c.GetVideoMessage() != nil:
		return "[video]"
	case c.GetAudioMessage() != nil:
		return "[voice]"
	case c.GetDocumentMessage() != nil:
		return "[file]"
	case c.GetStickerMessage() != nil:
		return "[sticker]"
	default:
		return "[other]"
	}
}
