// Package messenger connects Tuon to Facebook Messenger using messagix
// (the library behind mautrix-meta / Beeper). Login is by browser cookies.
package messenger

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"html"
	"image"
	"image/color"
	"image/draw"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf16"

	"github.com/rs/zerolog"
	"go.mau.fi/mautrix-meta/pkg/messagix"
	"go.mau.fi/mautrix-meta/pkg/messagix/cookies"
	"go.mau.fi/mautrix-meta/pkg/messagix/httpclient"
	"go.mau.fi/mautrix-meta/pkg/messagix/methods"
	"go.mau.fi/mautrix-meta/pkg/messagix/socket"
	"go.mau.fi/mautrix-meta/pkg/messagix/table"
	"go.mau.fi/mautrix-meta/pkg/messagix/types"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waCommon"
	"go.mau.fi/whatsmeow/proto/waConsumerApplication"
	"go.mau.fi/whatsmeow/proto/waMediaTransport"
	"go.mau.fi/whatsmeow/proto/waMsgApplication"
	"go.mau.fi/whatsmeow/store/sqlstore"
	waTypes "go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"
	"google.golang.org/protobuf/proto"

	"github.com/ralph/tuon/internal/connector"
)

const (
	maxThreadPages = 10
	waImagePrefix  = "wa:image:"
)

type Conn struct {
	cookieStr string
	db        *sql.DB
	log       zerolog.Logger

	cli    *messagix.Client
	wa     *whatsmeow.Client
	self   int64
	events chan any

	mu         sync.Mutex
	threadType map[int64]table.ThreadType
	names      map[string]string // user ID -> name, to show encrypted @mentions

	e2eeOnce  sync.Once
	fetchOnce sync.Once
	ctx       context.Context
	cancel    context.CancelFunc
}

var _ connector.Connector = (*Conn)(nil)

// New makes a Messenger connector. cookieStr is "datr=..; c_user=..; xs=..".
// db holds the encrypted-chat keys (shared with Tuon's own store).
func New(cookieStr string, db *sql.DB, log zerolog.Logger) *Conn {
	return &Conn{
		cookieStr:  cookieStr,
		db:         db,
		log:        log,
		events:     make(chan any, 1024),
		threadType: map[int64]table.ThreadType{},
		names:      map[string]string{},
	}
}

func (c *Conn) Events() <-chan any { return c.events }
func (c *Conn) SelfID() string     { return strconv.FormatInt(c.self, 10) }

func (c *Conn) emit(evt any) {
	select {
	case c.events <- evt:
	case <-c.ctx.Done():
	}
}

func (c *Conn) status(text string, err error) {
	c.emit(connector.StatusEvent{Text: text, Err: err})
}

func (c *Conn) Start(ctx context.Context) error {
	c.ctx, c.cancel = context.WithCancel(ctx)
	ck := &cookies.Cookies{Platform: types.Messenger}
	ck.UpdateValues(parseCookies(c.cookieStr))
	if missing := ck.GetMissingCookieNames(); len(missing) > 0 {
		return fmt.Errorf("missing cookies: %v", missing)
	}

	c.cli = messagix.NewClient(ck, c.log, &messagix.Config{})
	user, tbl, err := c.cli.LoadMessagesPage(c.ctx)
	if err != nil {
		return fmt.Errorf("load inbox (cookies expired?): %w", err)
	}
	c.self = user.GetFBID()
	c.handleTable(tbl)

	c.cli.SetEventHandler(c.onMeta)
	if err := c.cli.Connect(c.ctx); err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	return nil
}

func (c *Conn) Close() {
	if c.cancel != nil {
		c.cancel()
	}
	if c.wa != nil {
		c.wa.Disconnect()
	}
	if c.cli != nil {
		c.cli.Disconnect()
	}
}

func (c *Conn) onMeta(ctx context.Context, raw any) {
	switch evt := raw.(type) {
	case *table.LSTable:
		c.handleTable(evt)
	case *messagix.ConnectedEvent:
		c.status("connected", nil)
		c.e2eeOnce.Do(func() { go c.connectE2EE() })
		c.fetchOnce.Do(func() { go c.fetchMoreThreads() })
	case *messagix.ReconnectedEvent:
		c.status("connected", nil)
	case *messagix.TransientDisconnectEvent:
		c.status("reconnecting", nil)
	case *messagix.PermanentErrorEvent:
		c.status("disconnected", evt.Err)
	}
}

func (c *Conn) fetchMoreThreads() {
	var prevMin int64
	for i := 0; i < maxThreadPages; i++ {
		ks, tbl, err := c.cli.FetchMoreThreads(c.ctx, 1)
		if err != nil {
			c.log.Warn().Err(err).Msg("fetch more threads")
			return
		}
		if tbl == nil {
			return
		}
		c.handleTable(tbl)
		if ks == nil || !ks.HasMoreBefore || ks.MinThreadKey == prevMin {
			return
		}
		prevMin = ks.MinThreadKey
	}
}

// ---- plain (lightspeed) chats ----

func (c *Conn) handleTable(tbl *table.LSTable) {
	if tbl == nil {
		return
	}
	for _, ct := range tbl.LSVerifyContactRowExists {
		c.contact(ct.ContactId, ct.Name)
	}
	for _, ct := range tbl.LSDeleteThenInsertContact {
		c.contact(ct.Id, ct.Name)
	}
	for _, p := range tbl.LSAddParticipantIdToGroupThread {
		c.emit(connector.ParticipantEvent{ChatID: id(p.ThreadKey), UserID: id(p.ContactId), Nickname: clean(p.Nickname)})
	}
	for _, t := range tbl.LSDeleteThenInsertThread {
		c.mu.Lock()
		c.threadType[t.ThreadKey] = t.ThreadType
		c.mu.Unlock()
		c.emit(connector.ChatEvent{Chat: connector.Chat{
			ID:           id(t.ThreadKey),
			Name:         clean(t.ThreadName),
			Snippet:      clean(oneLine(t.Snippet)),
			LastActivity: time.UnixMilli(t.LastActivityTimestampMs),
			Unread:       t.LastReadWatermarkTimestampMs < t.LastActivityTimestampMs,
			Group:        !t.ThreadType.IsOneToOne(),
			Encrypted:    t.ThreadType.IsWhatsApp(),
		}})
	}

	// Chats can also arrive as these records (often with live messages
	// for chats that weren't in the first pages).
	for _, t := range tbl.LSUpdateOrInsertThread {
		c.setType(t.ThreadKey, t.ThreadType)
		c.emit(connector.ChatEvent{Chat: connector.Chat{
			ID:           id(t.ThreadKey),
			Name:         clean(t.ThreadName),
			Snippet:      clean(oneLine(t.Snippet)),
			LastActivity: time.UnixMilli(t.LastActivityTimestampMs),
			Unread:       t.LastReadWatermarkTimestampMs < t.LastActivityTimestampMs,
			Group:        !t.ThreadType.IsOneToOne(),
			Encrypted:    t.ThreadType.IsWhatsApp(),
		}})
	}
	for _, t := range tbl.LSVerifyThreadExists {
		c.knownType(t.ThreadKey, t.ThreadType)
	}
	for _, t := range tbl.LSVerifyHybridThreadExists {
		c.knownType(t.ThreadKey, t.ThreadType)
	}
	for _, t := range tbl.LSApplyNewGroupThread {
		c.knownType(t.ThreadKey, t.ThreadType)
	}
	for _, t := range tbl.LSSyncUpdateThreadName {
		if t.ThreadName != "" {
			c.emit(connector.ChatEvent{Chat: connector.Chat{ID: id(t.ThreadKey), Name: clean(t.ThreadName)}})
		}
	}

	media := map[string][]connector.Media{}
	for _, a := range tbl.LSInsertBlobAttachment {
		m := connector.Media{Kind: kindOf(a.AttachmentType), Ref: a.PreviewUrl}
		if m.Ref == "" {
			m.Ref = a.PlayableUrl
		}
		media[a.MessageId] = append(media[a.MessageId], m)
	}

	defer func() { // reactions after messages, so they have something to attach to
		for _, r := range tbl.LSUpsertReaction {
			c.emit(connector.ReactionEvent{ChatID: id(r.ThreadKey), MessageID: r.MessageId, ActorID: id(r.ActorId), Emoji: r.Reaction})
		}
		for _, r := range tbl.LSDeleteReaction {
			c.emit(connector.ReactionEvent{ChatID: id(r.ThreadKey), MessageID: r.MessageId, ActorID: id(r.ActorId)})
		}
	}()

	msgs := tbl.LSInsertMessage
	for _, u := range tbl.LSUpsertMessage {
		msgs = append(msgs, u.ToInsert())
	}
	for _, m := range msgs {
		if m.IsUnsent {
			continue
		}
		att := media[m.MessageId]
		if m.StickerId != 0 && len(att) == 0 {
			att = []connector.Media{{Kind: connector.Sticker}}
		}
		if m.Text == "" && len(att) == 0 {
			continue
		}
		msg := connector.Message{
			ID:       m.MessageId,
			ChatID:   id(m.ThreadKey),
			SenderID: id(m.SenderId),
			Text:     clean(m.Text),
			Time:     time.UnixMilli(m.TimestampMs),
			FromMe:   m.SenderId == c.self,
			Media:    att,
		}
		if m.OfflineThreadingId != "" {
			msg.ReplacesID = pendingID(m.OfflineThreadingId)
		}
		c.emit(connector.MessageEvent{Message: msg})
	}
}

func (c *Conn) setType(key int64, tt table.ThreadType) {
	c.mu.Lock()
	c.threadType[key] = tt
	c.mu.Unlock()
}

// knownType records a chat's kind (group, 1:1, encrypted) without
// touching its name, snippet or time.
func (c *Conn) knownType(key int64, tt table.ThreadType) {
	if tt == 0 {
		return
	}
	c.setType(key, tt)
	c.emit(connector.ChatEvent{Chat: connector.Chat{
		ID: id(key), Group: !tt.IsOneToOne(), Encrypted: tt.IsWhatsApp(),
	}})
}

func (c *Conn) contact(uid int64, name string) {
	if name == "" {
		return
	}
	name = clean(name)
	c.mu.Lock()
	c.names[id(uid)] = name
	c.mu.Unlock()
	c.emit(connector.ContactEvent{ID: id(uid), Name: name})
}

func kindOf(t table.AttachmentType) connector.MediaKind {
	switch t {
	case table.AttachmentTypeImage, table.AttachmentTypeAnimatedImage, table.AttachmentTypeEphemeralImage:
		return connector.Photo
	case table.AttachmentTypeVideo, table.AttachmentTypeEphemeralVideo:
		return connector.Video
	case table.AttachmentTypeAudio, table.AttachmentTypeSoundBite:
		return connector.Voice
	case table.AttachmentTypeSticker, table.AttachmentTypeThirdPartySticker, table.AttachmentTypeSelfieSticker:
		return connector.Sticker
	default:
		return connector.File
	}
}

func (c *Conn) LoadOlder(ctx context.Context, chatID string, before time.Time, beforeID string) error {
	key, err := strconv.ParseInt(chatID, 10, 64)
	if err != nil {
		return err
	}
	if c.isEncrypted(key) {
		return nil // no server history for encrypted chats yet (needs PIN restore)
	}
	resp, err := c.cli.ExecuteTasks(ctx, &socket.FetchMessagesTask{
		ThreadKey:            key,
		ReferenceTimestampMs: before.UnixMilli(),
		ReferenceMessageId:   beforeID,
		SyncGroup:            1,
		Cursor:               c.cli.GetCursor(1),
	})
	if err != nil {
		return err
	}
	c.handleTable(resp)
	return nil
}

// ---- encrypted chats ----

func (c *Conn) connectE2EE() {
	container := sqlstore.NewWithDB(c.db, "sqlite3", waLog.Zerolog(c.log.With().Str("c", "wastore").Logger()))
	if err := container.Upgrade(c.ctx); err != nil {
		c.status("encrypted chats off", err)
		return
	}
	dev, err := container.GetFirstDevice(c.ctx)
	if err != nil {
		c.status("encrypted chats off", err)
		return
	}
	c.cli.SetDevice(dev)
	if dev.ID == nil {
		if err := c.cli.RegisterE2EE(c.ctx, c.self); err != nil {
			c.status("encrypted chats off", fmt.Errorf("register device: %w", err))
			return
		}
		if err := dev.Save(c.ctx); err != nil {
			c.status("encrypted chats off", err)
			return
		}
	}
	wa, err := c.cli.PrepareE2EEClient()
	if err != nil {
		c.status("encrypted chats off", err)
		return
	}
	wa.AddEventHandler(c.onWA)
	if err := wa.Connect(); err != nil {
		c.status("encrypted chats off", err)
		return
	}
	c.mu.Lock()
	c.wa = wa
	c.mu.Unlock()
}

func (c *Conn) onWA(raw any) {
	evt, ok := raw.(*events.FBMessage)
	if !ok {
		return
	}
	app, ok := evt.Message.(*waConsumerApplication.ConsumerApplication)
	if !ok {
		return // reactions, edits, receipts etc. — not shown
	}
	content := app.GetPayload().GetContent()
	if content == nil {
		return
	}
	chat := evt.Info.Chat
	if r := content.GetReactionMessage(); r != nil {
		c.emit(connector.ReactionEvent{
			ChatID: chat.User, MessageID: r.GetKey().GetID(),
			ActorID: evt.Info.Sender.User, Emoji: r.GetText(),
		})
		return
	}
	text, media := waContent(content)
	if text == "" && len(media) == 0 {
		return
	}
	// encrypted mentions arrive as "@<id>@msgr"; show the name instead
	for _, jid := range content.GetMessageText().GetMentionedJID() {
		if parsed, err := waTypes.ParseJID(jid); err == nil {
			c.mu.Lock()
			name := c.names[parsed.User]
			c.mu.Unlock()
			if name != "" {
				text = strings.ReplaceAll(text, "@"+jid, "@"+name)
			}
		}
	}
	key, _ := strconv.ParseInt(chat.User, 10, 64)
	tt := table.ENCRYPTED_OVER_WA_ONE_TO_ONE
	if chat.Server == waTypes.GroupServer {
		tt = table.ENCRYPTED_OVER_WA_GROUP
	}
	c.mu.Lock()
	if _, known := c.threadType[key]; !known {
		c.threadType[key] = tt
	}
	c.mu.Unlock()

	msg := connector.Message{
		ID:       evt.Info.ID,
		ChatID:   chat.User,
		SenderID: evt.Info.Sender.User,
		Text:     text,
		Time:     evt.Info.Timestamp,
		FromMe:   evt.Info.Sender.User == c.SelfID(),
		Media:    media,
	}
	c.emit(connector.ChatEvent{Chat: connector.Chat{
		ID:           chat.User,
		Snippet:      snippetOf(msg),
		LastActivity: msg.Time,
		Unread:       !msg.FromMe,
		Group:        tt == table.ENCRYPTED_OVER_WA_GROUP,
		Encrypted:    true,
	}})
	c.emit(connector.MessageEvent{Message: msg})
}

func waContent(c *waConsumerApplication.ConsumerApplication_Content) (string, []connector.Media) {
	switch {
	case c.GetMessageText() != nil:
		return c.GetMessageText().GetText(), nil
	case c.GetExtendedTextMessage() != nil:
		return c.GetExtendedTextMessage().GetText().GetText(), nil
	case c.GetImageMessage() != nil:
		img := c.GetImageMessage()
		m := connector.Media{Kind: connector.Photo}
		if dec, err := img.Decode(); err == nil {
			if b, err := proto.Marshal(dec.GetIntegral().GetTransport()); err == nil {
				m.Ref = waImagePrefix + base64.StdEncoding.EncodeToString(b)
			}
		}
		return img.GetCaption().GetText(), []connector.Media{m}
	case c.GetVideoMessage() != nil:
		return c.GetVideoMessage().GetCaption().GetText(), []connector.Media{{Kind: connector.Video}}
	case c.GetAudioMessage() != nil:
		return "", []connector.Media{{Kind: connector.Voice}}
	case c.GetDocumentMessage() != nil:
		return "", []connector.Media{{Kind: connector.File}}
	case c.GetStickerMessage() != nil:
		return "", []connector.Media{{Kind: connector.Sticker}}
	}
	return "", nil
}

func snippetOf(m connector.Message) string {
	s := m.Text
	if s == "" && len(m.Media) > 0 {
		s = "[" + string(m.Media[0].Kind) + "]"
	}
	if m.FromMe {
		s = "You: " + s
	}
	return oneLine(s)
}

// ---- sending ----

func (c *Conn) isEncrypted(key int64) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.threadType[key].IsWhatsApp()
}

// lsMentions finds each "@Name" in text, in order, as UTF-16 offsets
// (what Messenger uses).
func lsMentions(text string, mentions []connector.Mention) socket.Mentions {
	var out socket.Mentions
	from := 0
	for _, m := range mentions {
		tag := "@" + m.Name
		i := strings.Index(text[from:], tag)
		uid, err := strconv.ParseInt(m.UserID, 10, 64)
		if i < 0 || err != nil {
			continue
		}
		i += from
		out = append(out, socket.Mention{
			ID:     uid,
			Offset: len(utf16.Encode([]rune(text[:i]))),
			Length: len(utf16.Encode([]rune(tag))),
			Type:   socket.MentionTypePerson,
		})
		from = i + len(tag)
	}
	return out
}

func (c *Conn) Send(ctx context.Context, chatID, text string, mentions []connector.Mention) error {
	key, err := strconv.ParseInt(chatID, 10, 64)
	if err != nil {
		return err
	}
	c.mu.Lock()
	tt := c.threadType[key]
	wa := c.wa
	c.mu.Unlock()

	if !tt.IsWhatsApp() {
		otid := methods.GenerateEpochID()
		// show it now; Messenger's echo (same otid) replaces it
		pend := connector.Message{
			ID: pendingID(strconv.FormatInt(otid, 10)), ChatID: chatID, SenderID: c.SelfID(),
			Text: text, Time: time.Now(), FromMe: true, Pending: true,
		}
		c.emit(connector.ChatEvent{Chat: connector.Chat{ID: chatID, Snippet: snippetOf(pend), LastActivity: pend.Time}})
		c.emit(connector.MessageEvent{Message: pend})
		task := &socket.SendMessageTask{
			ThreadId:         key,
			Otid:             otid,
			Source:           table.MESSENGER_INBOX_IN_THREAD,
			InitiatingSource: table.FACEBOOK_INBOX,
			SendType:         table.TEXT,
			SyncGroup:        1,
			Text:             text,
		}
		if ms := lsMentions(text, mentions); len(ms) > 0 {
			task.MentionData = ms.ToData()
		}
		if _, err := c.cli.ExecuteTasks(ctx, task); err != nil {
			c.emit(connector.MessageRemovedEvent{ID: pend.ID})
			return err
		}
		return nil
	}

	if wa == nil {
		return errors.New("encrypted chats not connected yet")
	}
	jid := waTypes.JID{User: chatID, Server: waTypes.MessengerServer}
	if tt == table.ENCRYPTED_OVER_WA_GROUP {
		jid.Server = waTypes.GroupServer
	}
	msgID := strconv.FormatInt(methods.GenerateEpochID(), 10)
	pend := connector.Message{ID: msgID, ChatID: chatID, SenderID: c.SelfID(), Text: text, Time: time.Now(), FromMe: true, Pending: true}
	c.emit(connector.MessageEvent{Message: pend}) // show it now
	// encrypted mentions: "@Name" becomes "@<id>@msgr" plus the ID list
	wireText := text
	var mentioned []string
	for _, m := range mentions {
		mj := waTypes.JID{User: m.UserID, Server: waTypes.MessengerServer}.String()
		if strings.Contains(wireText, "@"+m.Name) {
			wireText = strings.Replace(wireText, "@"+m.Name, "@"+mj, 1)
			mentioned = append(mentioned, mj)
		}
	}
	resp, err := wa.SendFBMessage(ctx, jid, wrapContent(&waConsumerApplication.ConsumerApplication_Content{
		Content: &waConsumerApplication.ConsumerApplication_Content_MessageText{
			MessageText: &waCommon.MessageText{Text: proto.String(wireText), MentionedJID: mentioned},
		},
	}), &waMsgApplication.MessageApplication_Metadata{}, whatsmeow.SendRequestExtra{ID: msgID})
	if err != nil {
		c.emit(connector.MessageRemovedEvent{ID: msgID})
		return err
	}
	// Encrypted sends don't echo back, so confirm our own message.
	msg := connector.Message{ID: msgID, ChatID: chatID, SenderID: c.SelfID(), Text: text, Time: resp.Timestamp, FromMe: true}
	c.emit(connector.ChatEvent{Chat: connector.Chat{ID: chatID, Snippet: snippetOf(msg), LastActivity: msg.Time, Encrypted: true, Group: tt == table.ENCRYPTED_OVER_WA_GROUP}})
	c.emit(connector.MessageEvent{Message: msg})
	return nil
}

func wrapContent(content *waConsumerApplication.ConsumerApplication_Content) *waConsumerApplication.ConsumerApplication {
	return &waConsumerApplication.ConsumerApplication{
		Payload: &waConsumerApplication.ConsumerApplication_Payload{
			Payload: &waConsumerApplication.ConsumerApplication_Payload_Content{Content: content},
		},
	}
}

func (c *Conn) React(ctx context.Context, chatID, messageID, senderID, emoji string) error {
	key, err := strconv.ParseInt(chatID, 10, 64)
	if err != nil {
		return err
	}
	c.mu.Lock()
	tt := c.threadType[key]
	wa := c.wa
	c.mu.Unlock()
	now := time.Now().UnixMilli()

	if !tt.IsWhatsApp() {
		_, err := c.cli.ExecuteTasks(ctx, &socket.SendReactionTask{
			ThreadKey:       key,
			TimestampMs:     now,
			MessageID:       messageID,
			ActorID:         c.self,
			Reaction:        emoji,
			SyncGroup:       1,
			SendAttribution: table.MESSENGER_INBOX_IN_THREAD,
		})
		if err != nil {
			return err
		}
	} else {
		if wa == nil {
			return errors.New("encrypted chats not connected yet")
		}
		jid := waTypes.JID{User: chatID, Server: waTypes.MessengerServer}
		if tt == table.ENCRYPTED_OVER_WA_GROUP {
			jid.Server = waTypes.GroupServer
		}
		mk := &waCommon.MessageKey{RemoteJID: proto.String(jid.String()), ID: proto.String(messageID)}
		if senderID == c.SelfID() {
			mk.FromMe = proto.Bool(true)
		}
		if jid.Server == waTypes.GroupServer {
			mk.Participant = proto.String(waTypes.JID{User: senderID, Server: waTypes.MessengerServer}.String())
		}
		_, err := wa.SendFBMessage(ctx, jid, wrapContent(&waConsumerApplication.ConsumerApplication_Content{
			Content: &waConsumerApplication.ConsumerApplication_Content_ReactionMessage{
				ReactionMessage: &waConsumerApplication.ConsumerApplication_ReactionMessage{
					Key: mk, Text: proto.String(emoji), SenderTimestampMS: proto.Int64(now),
				},
			},
		}), nil, whatsmeow.SendRequestExtra{ID: strconv.FormatInt(methods.GenerateEpochID(), 10)})
		if err != nil {
			return err
		}
	}
	// show it right away; the server echo (if any) says the same thing
	c.emit(connector.ReactionEvent{ChatID: chatID, MessageID: messageID, ActorID: c.SelfID(), Emoji: emoji})
	return nil
}

func (c *Conn) SendFile(ctx context.Context, chatID string, file connector.Attachment) error {
	key, err := strconv.ParseInt(chatID, 10, 64)
	if err != nil {
		return err
	}
	c.mu.Lock()
	tt := c.threadType[key]
	wa := c.wa
	c.mu.Unlock()

	if !tt.IsWhatsApp() {
		resp, err := c.cli.GetHTTP().SendMercuryUploadRequest(ctx, key, &httpclient.MercuryUploadMedia{
			Filename: file.Name, MimeType: file.Mime, MediaData: file.Data,
		})
		if err != nil {
			return fmt.Errorf("upload: %w", err)
		}
		fbid := resp.Payload.RealMetadata.GetFbId()
		if fbid == 0 {
			return errors.New("upload: Messenger didn't return a file ID")
		}
		_, err = c.cli.ExecuteTasks(ctx, &socket.SendMessageTask{
			ThreadId:         key,
			Otid:             methods.GenerateEpochID(),
			Source:           table.MESSENGER_INBOX_IN_THREAD,
			InitiatingSource: table.FACEBOOK_INBOX,
			SendType:         table.MEDIA,
			AttachmentFBIds:  []int64{fbid},
			SyncGroup:        1,
		})
		return err // the echo comes back as a normal message
	}

	if wa == nil {
		return errors.New("encrypted chats not connected yet")
	}
	jid := waTypes.JID{User: chatID, Server: waTypes.MessengerServer}
	if tt == table.ENCRYPTED_OVER_WA_GROUP {
		jid.Server = waTypes.GroupServer
	}
	data, mimeType := file.Data, file.Mime
	isImage := file.IsImage()
	var w, h int
	if isImage {
		cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
		if err != nil {
			isImage = false // not a readable picture: send it as a file
		} else {
			w, h = cfg.Width, cfg.Height
			if mimeType == "image/png" && cfg.ColorModel != color.NRGBAModel {
				data = reencodePNG(data) // Messenger rejects some PNG color models
			}
		}
	}
	mediaType := whatsmeow.MediaDocument
	if isImage {
		mediaType = whatsmeow.MediaImage
	}
	up, err := wa.Upload(ctx, data, mediaType)
	if err != nil {
		return fmt.Errorf("upload: %w", err)
	}
	tw, th := clamp400(w, h)
	transport := &waMediaTransport.WAMediaTransport{
		Integral: &waMediaTransport.WAMediaTransport_Integral{
			FileSHA256: up.FileSHA256, MediaKey: up.MediaKey, FileEncSHA256: up.FileEncSHA256,
			DirectPath: &up.DirectPath, MediaKeyTimestamp: proto.Int64(time.Now().Unix()),
		},
		Ancillary: &waMediaTransport.WAMediaTransport_Ancillary{
			FileLength: proto.Uint64(uint64(len(data))),
			Mimetype:   &mimeType,
			// Messenger apps won't show media without this (and non-zero sizes)
			Thumbnail: &waMediaTransport.WAMediaTransport_Ancillary_Thumbnail{
				ThumbnailWidth: proto.Uint32(uint32(tw)), ThumbnailHeight: proto.Uint32(uint32(th)),
			},
			ObjectID: &up.ObjectID,
		},
	}
	var content waConsumerApplication.ConsumerApplication_Content
	media := connector.Media{Kind: connector.File}
	if isImage {
		msg := &waConsumerApplication.ConsumerApplication_ImageMessage{Caption: &waCommon.MessageText{}}
		if err := msg.Set(&waMediaTransport.ImageTransport{
			Integral:  &waMediaTransport.ImageTransport_Integral{Transport: transport},
			Ancillary: &waMediaTransport.ImageTransport_Ancillary{Width: proto.Uint32(uint32(w)), Height: proto.Uint32(uint32(h))},
		}); err != nil {
			return err
		}
		content.Content = &waConsumerApplication.ConsumerApplication_Content_ImageMessage{ImageMessage: msg}
		if b, err := proto.Marshal(transport); err == nil {
			media = connector.Media{Kind: connector.Photo, Ref: waImagePrefix + base64.StdEncoding.EncodeToString(b)}
		}
	} else {
		name := file.Name
		msg := &waConsumerApplication.ConsumerApplication_DocumentMessage{FileName: &name}
		if err := msg.Set(&waMediaTransport.DocumentTransport{
			Integral:  &waMediaTransport.DocumentTransport_Integral{Transport: transport},
			Ancillary: &waMediaTransport.DocumentTransport_Ancillary{},
		}); err != nil {
			return err
		}
		content.Content = &waConsumerApplication.ConsumerApplication_Content_DocumentMessage{DocumentMessage: msg}
	}
	msgID := strconv.FormatInt(methods.GenerateEpochID(), 10)
	resp, err := wa.SendFBMessage(ctx, jid, wrapContent(&content), &waMsgApplication.MessageApplication_Metadata{}, whatsmeow.SendRequestExtra{ID: msgID})
	if err != nil {
		return err
	}
	msg := connector.Message{ID: msgID, ChatID: chatID, SenderID: c.SelfID(), Time: resp.Timestamp, FromMe: true, Media: []connector.Media{media}}
	c.emit(connector.ChatEvent{Chat: connector.Chat{ID: chatID, Snippet: snippetOf(msg), LastActivity: msg.Time, Encrypted: true, Group: tt == table.ENCRYPTED_OVER_WA_GROUP}})
	c.emit(connector.MessageEvent{Message: msg})
	return nil
}

func clamp400(w, h int) (int, int) {
	if w == 0 || h == 0 {
		return 400, 400
	}
	if w > 400 {
		h, w = h*400/w, 400
	}
	if h > 400 {
		w, h = w*400/h, 400
	}
	return max(1, w), max(1, h)
}

func reencodePNG(data []byte) []byte {
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return data
	}
	b := img.Bounds()
	out := image.NewNRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(out, out.Bounds(), img, b.Min, draw.Src)
	var buf bytes.Buffer
	if png.Encode(&buf, out) != nil {
		return data
	}
	return buf.Bytes()
}

// ---- media ----

func (c *Conn) FetchMedia(ctx context.Context, m connector.Media) ([]byte, error) {
	if m.Ref == "" {
		return nil, errors.New("no media to fetch")
	}
	if strings.HasPrefix(m.Ref, waImagePrefix) {
		c.mu.Lock()
		wa := c.wa
		c.mu.Unlock()
		if wa == nil {
			return nil, errors.New("encrypted chats not connected yet")
		}
		b, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(m.Ref, waImagePrefix))
		if err != nil {
			return nil, err
		}
		var t waMediaTransport.WAMediaTransport
		if err := proto.Unmarshal(b, &t); err != nil {
			return nil, err
		}
		return wa.DownloadFB(ctx, t.GetIntegral(), whatsmeow.MediaImage)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.Ref, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("media: HTTP %d (link may have expired)", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 25<<20))
}

// ---- helpers ----

func pendingID(otid string) string { return "pending:" + otid }

func id(n int64) string { return strconv.FormatInt(n, 10) }

func clean(s string) string { return html.UnescapeString(s) }

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

func parseCookies(s string) map[cookies.MetaCookieName]string {
	out := map[cookies.MetaCookieName]string{}
	for _, part := range strings.Split(s, ";") {
		k, v, ok := strings.Cut(strings.TrimSpace(part), "=")
		if ok && k != "" {
			out[cookies.MetaCookieName(k)] = v
		}
	}
	return out
}
