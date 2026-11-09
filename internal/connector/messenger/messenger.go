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
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf16"

	"github.com/rs/zerolog"
	"go.mau.fi/mautrix-meta/pkg/messagix"
	"go.mau.fi/mautrix-meta/pkg/messagix/cookies"
	"go.mau.fi/mautrix-meta/pkg/messagix/httpclient"
	"go.mau.fi/mautrix-meta/pkg/messagix/methods"
	"go.mau.fi/mautrix-meta/pkg/messagix/securehistory"
	"go.mau.fi/mautrix-meta/pkg/messagix/socket"
	"go.mau.fi/mautrix-meta/pkg/messagix/table"
	"go.mau.fi/mautrix-meta/pkg/messagix/types"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waArmadilloApplication"
	"go.mau.fi/whatsmeow/proto/waCommon"
	"go.mau.fi/whatsmeow/proto/waConsumerApplication"
	"go.mau.fi/whatsmeow/proto/waMediaTransport"
	"go.mau.fi/whatsmeow/proto/waMsgApplication"
	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/store/sqlstore"
	waTypes "go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"
	"google.golang.org/protobuf/proto"

	"github.com/ralph/tuon/internal/auth"
	"github.com/ralph/tuon/internal/connector"
)

const (
	maxThreadPages = 10
	waImagePrefix  = "wa:image:"
	waDocPrefix    = "wa:doc:"
)

type Conn struct {
	creds auth.Creds
	db    *sql.DB
	log   zerolog.Logger

	cli    *messagix.Client
	wa     *whatsmeow.Client
	self   int64
	events chan any

	mu         sync.Mutex
	threadType map[int64]table.ThreadType
	names      map[string]string // user ID -> name, to show encrypted @mentions

	wadev *store.Device         // encrypted-chat identity
	hdev  *securehistory.Device // backup device, once the PIN restore ran

	e2eeOnce sync.Once
	// resetAt: when we last dropped our session with a sender's device
	// after it failed to decrypt (see resetSession)
	resetAt map[string]time.Time
	// failed: per sender device, the distinct messages that failed to
	// decrypt since the last one that worked (see trackFailure)
	failed    map[string]map[string]bool
	okDevices map[string]bool // sender devices whose messages decrypted this run
	// e2eeErr: why encrypted chats couldn't start, if they couldn't
	e2eeErr   error
	fetchOnce sync.Once
	// threadsSeen counts distinct chats received (known holds their keys),
	// so paging can tell when a page brought nothing new
	threadsSeen atomic.Int64
	known       map[int64]bool
	seal        Sealer  // encrypts the PIN-backup keys at rest; nil = plain
	keyDB       *sql.DB // encrypted-chat keys (keys.db); nil = in db
	// AllowNewDevice lets the connector register tuon as a new linked
	// device when it finds no keys. Only a fresh setup may: if keys existed
	// and can't be found, registering would silently replace them.
	AllowNewDevice bool
	ctx            context.Context
	cancel         context.CancelFunc
}

var _ connector.Connector = (*Conn)(nil)

// Sealer encrypts stored secrets (the vault).
type Sealer interface {
	SealString(string) string
	OpenString(string) (string, error)
	IsSealed(string) bool
}

// SetSealer keeps the PIN-backup keys encrypted in the database.
func (c *Conn) SetSealer(s Sealer) { c.seal = s }

// WaitEncrypted waits until encrypted chats are connected (registering a
// new device first, when allowed), so a short command doesn't quit halfway.
func (c *Conn) WaitEncrypted(ctx context.Context) error {
	for {
		c.mu.Lock()
		wa, why := c.wa, c.e2eeErr
		c.mu.Unlock()
		if wa != nil && wa.IsConnected() {
			return nil
		}
		if why != nil {
			return fmt.Errorf("encrypted chats couldn't start: %w", why)
		}
		select {
		case <-ctx.Done():
			if wa == nil {
				return errors.New("encrypted chats didn't start (no answer from Messenger's encrypted service)")
			}
			return errors.New("encrypted chats started but couldn't connect: check your network, or run `tuon relink` if this keeps happening")
		case <-time.After(300 * time.Millisecond):
		}
	}
}

// SetKeyDB puts the encrypted-chat keys in their own (encrypted) database.
func (c *Conn) SetKeyDB(db *sql.DB) { c.keyDB = db }

// New makes a Messenger connector from a saved login.
// db holds the encrypted-chat keys (shared with Tuon's own store).
func New(creds auth.Creds, db *sql.DB, log zerolog.Logger) *Conn {
	setupSignalLog(log)
	return &Conn{
		creds:      creds,
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
	if err != nil { // error text can carry server-provided strings
		err = errors.New(connector.SafeText(err.Error(), false))
		if text == "encrypted chats off" { // kept, so WaitEncrypted can say why
			c.mu.Lock()
			c.e2eeErr = err
			c.mu.Unlock()
		}
	}
	c.emit(connector.StatusEvent{Text: text, Err: err})
}

func (c *Conn) Start(ctx context.Context) error {
	c.ctx, c.cancel = context.WithCancel(ctx)
	platform := types.Messenger
	switch c.creds.Platform {
	case "messenger-lite":
		platform = types.MessengerLiteIOS
	case "facebook":
		platform = types.Facebook // logged in through facebook.com
	}
	ck := &cookies.Cookies{Platform: platform}
	vals := map[cookies.MetaCookieName]string{}
	for k, v := range c.creds.Cookies {
		vals[cookies.MetaCookieName(k)] = v
	}
	ck.UpdateValues(vals)
	if missing := ck.GetMissingCookieNames(); len(missing) > 0 {
		return fmt.Errorf("missing cookies: %v", missing)
	}

	c.cli = messagix.NewClient(ck, c.log, &messagix.Config{})
	keepCookiesOnRedirect(c.cli.GetHTTP().GetHTTPClient())
	user, tbl, err := c.cli.LoadMessagesPage(c.ctx)
	if err != nil {
		if isLoggedOut(err) {
			return ErrLoggedOut
		}
		return fmt.Errorf("couldn't load your chats: %w", err)
	}
	c.self = user.GetFBID()
	c.handleTable(tbl)

	c.cli.SetEventHandler(c.onMeta)
	if err := c.cli.Connect(c.ctx); err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	return nil
}

// Creds is the login as it stands now: Messenger refreshes session
// cookies as it goes, so save this when quitting.
func (c *Conn) Creds() auth.Creds {
	if c.cli == nil {
		return c.creds
	}
	vals := map[string]string{}
	for k, v := range c.cli.GetCookies().GetAll() {
		vals[string(k)] = v
	}
	return auth.Creds{Platform: c.creds.Platform, Cookies: vals}
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

// fetchMoreThreads loads the chat list past the first page, for plain
// chats (sync group 1) and encrypted ones (95). Messenger only acknowledges
// each request; the chats arrive a moment later over the connection. So
// after each page we wait, and stop a group once a page brings nothing new.
func (c *Conn) fetchMoreThreads() {
	for _, group := range []int64{1, 95} {
		pages, got := 0, 0
		why := "page limit"
		for i := 0; i < maxThreadPages; i++ {
			before := c.threadsSeen.Load()
			ks, tbl, err := c.cli.FetchMoreThreads(c.ctx, group)
			if err != nil {
				why = "error: " + err.Error()
				break
			}
			if tbl == nil || ks == nil || !ks.HasMoreBefore {
				why = "no more"
				break
			}
			pages++
			c.handleTable(tbl)
			select { // the chats come over the connection
			case <-c.ctx.Done():
				return
			case <-time.After(3 * time.Second):
			}
			n := int(c.threadsSeen.Load() - before)
			got += n
			if n == 0 {
				why = "a page brought nothing new"
				break
			}
		}
		// counts only: how far the chat list got, and why it stopped
		c.log.Warn().Int64("group", group).Int("pages", pages).Int("chats", got).Str("stop", why).Msg("chat list paging")
	}
}

// countThreads notes chats we hadn't seen this run.
func (c *Conn) countThreads(tbl *table.LSTable) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.known == nil {
		c.known = map[int64]bool{}
	}
	add := func(k int64) {
		if !c.known[k] {
			c.known[k] = true
			c.threadsSeen.Add(1)
		}
	}
	for _, t := range tbl.LSDeleteThenInsertThread {
		add(t.ThreadKey)
	}
	for _, t := range tbl.LSUpdateOrInsertThread {
		add(t.ThreadKey)
	}
}

func keyMin(ks *socket.KeyStoreData) int64 {
	if ks == nil {
		return 0
	}
	return ks.MinThreadKey
}

// tableNames lists the non-empty record lists in a table (for diagnostics).
func tableNames(tbl *table.LSTable) []string {
	var out []string
	v := reflect.ValueOf(tbl).Elem()
	for i := 0; i < v.NumField(); i++ {
		if f := v.Field(i); f.Kind() == reflect.Slice && f.Len() > 0 {
			out = append(out, fmt.Sprintf("%s:%d", v.Type().Field(i).Name, f.Len()))
		}
	}
	return out
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
	for _, h := range tbl.LSVerifyHybridThreadExists {
		if h.ThreadJID != 0 && h.ThreadJID != h.ThreadKey {
			c.emit(connector.ChatAliasEvent{From: id(h.ThreadKey), To: id(h.ThreadJID)})
		}
	}
	for _, h := range tbl.LSUpdateThreadAuthorityAndMappingWithOTIDFromJID {
		if h.ThreadJID != 0 && h.ThreadJID != h.ThreadKey {
			c.emit(connector.ChatAliasEvent{From: id(h.ThreadKey), To: id(h.ThreadJID)})
		}
	}
	c.countThreads(tbl)
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
		// the full-size file, like the bridge uses; the preview is small and blurry
		m := connector.Media{Kind: kindOf(a.AttachmentType), Ref: a.PlayableUrl}
		if m.Kind == connector.File {
			m.Name = clean(a.Filename)
		}
		if m.Ref == "" {
			m.Ref = a.PreviewUrl
		}
		media[a.MessageId] = append(media[a.MessageId], m)
	}

	defer func() { // reactions after messages, so they have something to attach to
		for _, r := range tbl.LSUpsertReaction {
			c.emit(connector.ReactionEvent{ChatID: id(r.ThreadKey), MessageID: r.MessageId, ActorID: id(r.ActorId), Emoji: clean(r.Reaction)})
		}
		for _, r := range tbl.LSDeleteReaction {
			c.emit(connector.ReactionEvent{ChatID: id(r.ThreadKey), MessageID: r.MessageId, ActorID: id(r.ActorId)})
		}
	}()

	// a card's link usually comes in a separate "button" record
	ctaURL := map[string]string{}
	for _, cta := range tbl.LSInsertAttachmentCta {
		if u := firstNonEmpty(cta.ActionUrl, cta.NativeUrl); u != "" && ctaURL[cta.MessageId] == "" {
			ctaURL[cta.MessageId] = cleanURL(u)
		}
	}
	// shared posts and links ("XMA" cards)
	for _, x := range tbl.LSInsertXmaAttachment {
		card := connector.Media{
			Kind:   connector.Link,
			Title:  clean(oneLine(firstNonEmpty(x.TitleText, x.HeaderTitle, x.BodyText, x.DescriptionText, x.AccessibilitySummaryText))),
			Source: clean(oneLine(firstNonEmpty(x.SourceText, x.HeaderSubtitleText, x.AttributionAppName, x.SubtitleText))),
			URL:    firstNonEmpty(cleanURL(x.ActionUrl), ctaURL[x.MessageId]),
		}
		if card.Title == "" && card.URL == "" {
			continue
		}
		media[x.MessageId] = append(media[x.MessageId], card)
	}

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
			Text:     cleanBody(m.Text),
			Time:     time.UnixMilli(m.TimestampMs),
			FromMe:   m.SenderId == c.self,
			Media:    att,
		}
		if m.OfflineThreadingId != "" {
			msg.ReplacesID = pendingID(m.OfflineThreadingId)
		}
		msg.ReplyToID = m.ReplySourceId
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
		// older encrypted messages come from the PIN backup, if restored
		return c.loadEncryptedHistory(ctx, chatID, c.isEncryptedGroup(key), before)
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
	kdb := c.keyDB
	if kdb == nil {
		kdb = c.db
	}
	container := sqlstore.NewWithDB(kdb, "sqlite3", waLog.Zerolog(c.log.With().Str("c", "wastore").Logger()))
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
	c.mu.Lock()
	c.wadev = dev
	c.mu.Unlock()
	if dev.ID == nil && !c.AllowNewDevice {
		c.status("encrypted chats off", errors.New("this device's encryption keys aren't available here; not registering a new device"))
		return
	}
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
	switch raw.(type) {
	case *events.Disconnected:
		c.status("reconnecting encrypted chats…", nil)
		return
	case *events.Connected:
		c.status("connected", nil)
		return
	case *events.UndecryptableMessage:
		c.onUndecryptable(raw.(*events.UndecryptableMessage))
		return
	}
	evt, ok := raw.(*events.FBMessage)
	if !ok {
		return
	}
	chat := evt.Info.Chat
	var content *waConsumerApplication.ConsumerApplication_Content
	var text string
	var media []connector.Media
	switch app := evt.Message.(type) {
	case *waConsumerApplication.ConsumerApplication:
		content = app.GetPayload().GetContent()
		if content == nil {
			return
		}
	case *waArmadilloApplication.Armadillo: // shared links, posts, videos
		var ok bool
		if text, media, ok = armadilloContent(app); !ok {
			return
		}
	default:
		return // receipts, edits, etc. — not shown
	}
	if r := content.GetReactionMessage(); r != nil {
		c.emit(connector.ReactionEvent{
			ChatID: chat.User, MessageID: r.GetKey().GetID(),
			ActorID: evt.Info.Sender.User, Emoji: connector.SafeText(r.GetText(), false),
		})
		return
	}
	if content != nil {
		text, media = waContent(content)
	}
	text = connector.SafeText(text, true)
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
	tt := c.noteEncrypted(chat)
	c.decryptedFrom(evt.Info.Sender)

	msg := connector.Message{
		ID:        evt.Info.ID,
		ChatID:    chat.User,
		SenderID:  evt.Info.Sender.User,
		Text:      text,
		Time:      evt.Info.Timestamp,
		FromMe:    evt.Info.Sender.User == c.SelfID(),
		Media:     media,
		ReplyToID: evt.FBApplication.GetMetadata().GetQuotedMessage().GetStanzaID(),
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

// noteEncrypted records an encrypted chat's thread type, so sends and
// history take the encrypted path.
func (c *Conn) noteEncrypted(chat waTypes.JID) table.ThreadType {
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
	return tt
}

// onUndecryptable: a message arrived that this device couldn't decrypt.
// Show a placeholder instead of dropping it; the real message (same ID)
// replaces it if a retry works or the PIN backup has it.
func (c *Conn) onUndecryptable(evt *events.UndecryptableMessage) {
	// what kind of item failed (never its content), to tell reactions apart
	c.log.Warn().Str("device", evt.Info.Sender.SignalAddress().String()).Str("type", evt.Info.Type).
		Str("media_type", evt.Info.MediaType).Str("fail_mode", string(evt.DecryptFailMode)).Msg("couldn't decrypt an item")
	reaction := evt.Info.Type == "reaction" || evt.Info.MediaType == "reaction"
	if !evt.IsUnavailable {
		c.resetSession(evt.Info.Sender)
		if !reaction { // a missed reaction isn't worth a warning
			c.trackFailure(evt.Info.Sender, evt.Info.ID)
		}
	}
	if evt.DecryptFailMode == events.DecryptFailHide || reaction {
		return // reactions, receipts and the like: no "loading" line for them
	}
	chat := evt.Info.Chat
	tt := c.noteEncrypted(chat)
	msg := connector.Message{
		ID:       evt.Info.ID,
		ChatID:   chat.User,
		SenderID: evt.Info.Sender.User,
		Time:     evt.Info.Timestamp,
		FromMe:   evt.Info.Sender.User == c.SelfID(),
		Media:    []connector.Media{{Kind: connector.Missing}},
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

// troubleAfter: failed messages in a row from one device before the user
// hears about it.
const troubleAfter = 3

// trackFailure counts distinct failed messages per sender device (each
// failure is reported several times, once per retry) and tells the UI when
// a device reaches troubleAfter — once, until a message from it works.
func (c *Conn) trackFailure(sender waTypes.JID, msgID string) {
	addr := sender.SignalAddress().String()
	c.mu.Lock()
	if c.failed == nil {
		c.failed = map[string]map[string]bool{}
	}
	if c.failed[addr] == nil {
		c.failed[addr] = map[string]bool{}
	}
	seen := c.failed[addr][msgID]
	c.failed[addr][msgID] = true
	n := len(c.failed[addr])
	c.mu.Unlock()
	if !seen && n == troubleAfter {
		c.log.Warn().Str("device", addr).Int("failed", n).Msg("messages from this device keep failing to decrypt")
		c.emit(connector.KeysTroubleEvent{UserID: sender.User, Failed: n})
	}
}

// decryptedFrom clears the failure count once a device's message works,
// and logs the first success per device per run (device only, no content),
// so the log shows which of someone's devices work and which don't.
func (c *Conn) decryptedFrom(sender waTypes.JID) {
	addr := sender.SignalAddress().String()
	c.mu.Lock()
	warned := len(c.failed[addr]) >= troubleAfter
	delete(c.failed, addr)
	if c.okDevices == nil {
		c.okDevices = map[string]bool{}
	}
	first := !c.okDevices[addr]
	c.okDevices[addr] = true
	c.mu.Unlock()
	if first {
		c.log.Warn().Str("device", addr).Msg("decrypted a message from this device")
	}
	if warned { // the UI can take its warning down
		c.emit(connector.KeysTroubleEvent{UserID: sender.User, Failed: 0})
	}
}

// resetSession drops our stored session with a sender's device after a
// message from it failed to decrypt. The sender resends with the same
// starting key, and libsignal reuses a session it already has for that key
// instead of building a new one, so a broken copy fails every resend
// ("no valid sessions"). With it gone, the next resend builds a fresh
// session from our keys. At most once per device every 10 minutes.
func (c *Conn) resetSession(sender waTypes.JID) {
	addr := sender.SignalAddress().String()
	c.mu.Lock()
	wa := c.wa
	if c.resetAt == nil {
		c.resetAt = map[string]time.Time{}
	}
	recent := time.Since(c.resetAt[addr]) < 10*time.Minute
	if !recent {
		c.resetAt[addr] = time.Now()
	}
	c.mu.Unlock()
	if wa == nil || recent {
		return
	}
	if err := wa.Store.Sessions.DeleteSession(c.ctx, addr); err != nil {
		c.log.Warn().Err(err).Str("device", addr).Msg("couldn't reset encrypted session")
		return
	}
	c.log.Warn().Str("device", addr).Msg("reset encrypted session after a failed message")
}

// armadilloContent reads Messenger's "Armadillo" messages: shared links,
// Facebook posts and videos (an extended content card), as text plus a
// link card. Kinds tuon can't read become a short note, never nothing.
func armadilloContent(a *waArmadilloApplication.Armadillo) (string, []connector.Media, bool) {
	content := a.GetPayload().GetContent()
	if content == nil {
		return "", nil, false // signals, application data: not a message
	}
	x := content.GetExtendedContentMessage()
	if x == nil {
		return "Shared something tuon can't show yet. Open it on your phone.", nil, true
	}
	link := ""
	for _, cta := range x.GetCtas() {
		for _, u := range []string{cta.GetNativeURL(), cta.GetActionURL()} {
			if p, err := url.Parse(u); err == nil && (p.Scheme == "https" || p.Scheme == "http") && p.Host != "" {
				link = u
				break
			}
		}
		if link != "" {
			break
		}
	}
	text := x.GetMessageText()
	if link == "" {
		if text == "" {
			text = "Shared something tuon can't show yet. Open it on your phone."
		}
		return text, nil, true
	}
	title := connector.SafeText(x.GetTitleText(), false)
	if title == "" {
		title = connector.SafeText(x.GetHeaderTitle(), false)
	}
	return text, []connector.Media{{
		Kind: connector.Link, URL: cleanURL(link),
		Title: title, Source: connector.SafeText(x.GetSubtitleText(), false),
	}}, true
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
		doc := c.GetDocumentMessage()
		m := connector.Media{Kind: connector.File, Name: connector.SafeText(doc.GetFileName(), false)}
		if dec, err := doc.Decode(); err == nil {
			if b, err := proto.Marshal(dec.GetIntegral().GetTransport()); err == nil {
				m.Ref = waDocPrefix + base64.StdEncoding.EncodeToString(b)
			}
		}
		return "", []connector.Media{m}
	case c.GetStickerMessage() != nil:
		return "", []connector.Media{{Kind: connector.Sticker}}
	}
	return "", nil
}

func snippetOf(m connector.Message) string {
	s := m.Text
	if s == "" && len(m.Media) > 0 {
		s = "[" + string(m.Media[0].Kind) + "]"
		if m.Media[0].Kind == connector.Missing {
			s = connector.MissingSnippet
		}
	}
	if m.FromMe {
		s = "You: " + s
	}
	return oneLine(s)
}

// ---- sending ----

// readyWA makes sure the encrypted connection is up before using it. After
// sleep or a network change it drops; the library reconnects by itself, so
// wait for that first, then reconnect ourselves.
func (c *Conn) readyWA(ctx context.Context, wa *whatsmeow.Client) (*whatsmeow.Client, error) {
	if wa == nil {
		return nil, errors.New("encrypted chats are still starting, try again in a moment")
	}
	if wa.IsConnected() {
		return wa, nil
	}
	if wa.WaitForConnection(10 * time.Second) {
		return wa, nil
	}
	if err := wa.ConnectContext(ctx); err != nil && !wa.IsConnected() {
		c.log.Warn().Err(err).Msg("reconnect encrypted chats")
	}
	if wa.WaitForConnection(15 * time.Second) {
		return wa, nil
	}
	return nil, errors.New("encrypted chats are offline; check your connection and try again")
}

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

func (c *Conn) Send(ctx context.Context, chatID, text string, mentions []connector.Mention, reply *connector.ReplyRef) error {
	replyTo := ""
	if reply != nil {
		replyTo = reply.ID
	}
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
			Text: text, Time: time.Now(), FromMe: true, Pending: true, ReplyToID: replyTo,
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
		if reply != nil {
			task.ReplyMetaData = &socket.ReplyMetaData{ReplyMessageId: reply.ID, ReplySourceType: 1}
		}
		if _, err := c.cli.ExecuteTasks(ctx, task); err != nil {
			c.emit(connector.MessageRemovedEvent{ID: pend.ID})
			return err
		}
		return nil
	}

	if wa, err = c.readyWA(ctx, wa); err != nil {
		return err
	}
	jid := waTypes.JID{User: chatID, Server: waTypes.MessengerServer}
	if tt == table.ENCRYPTED_OVER_WA_GROUP {
		jid.Server = waTypes.GroupServer
	}
	msgID := strconv.FormatInt(methods.GenerateEpochID(), 10)
	pend := connector.Message{ID: msgID, ChatID: chatID, SenderID: c.SelfID(), Text: text, Time: time.Now(), FromMe: true, Pending: true, ReplyToID: replyTo}
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
	}), replyMeta(reply), whatsmeow.SendRequestExtra{ID: msgID})
	if err != nil {
		c.emit(connector.MessageRemovedEvent{ID: msgID})
		return err
	}
	// Encrypted sends don't echo back, so confirm our own message.
	msg := connector.Message{ID: msgID, ChatID: chatID, SenderID: c.SelfID(), Text: text, Time: resp.Timestamp, FromMe: true, ReplyToID: replyTo}
	c.emit(connector.ChatEvent{Chat: connector.Chat{ID: chatID, Snippet: snippetOf(msg), LastActivity: msg.Time, Encrypted: true, Group: tt == table.ENCRYPTED_OVER_WA_GROUP}})
	c.emit(connector.MessageEvent{Message: msg})
	return nil
}

// replyMeta quotes the replied-to message in an encrypted send.
func replyMeta(reply *connector.ReplyRef) *waMsgApplication.MessageApplication_Metadata {
	meta := &waMsgApplication.MessageApplication_Metadata{}
	if reply != nil {
		meta.QuotedMessage = &waMsgApplication.MessageApplication_Metadata_QuotedMessage{
			StanzaID:    proto.String(reply.ID),
			Participant: proto.String(waTypes.JID{User: reply.SenderID, Server: waTypes.MessengerServer}.String()),
		}
	}
	return meta
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
		if wa, err = c.readyWA(ctx, wa); err != nil {
			return err
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

	if wa, err = c.readyWA(ctx, wa); err != nil {
		return err
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

// KnowChat: after a restart the connector has forgotten which chats are
// encrypted until Messenger mentions them again. Sending an encrypted 1:1
// the plain way fails ("Thread Disabled"), so trust what the store knows.
func (c *Conn) KnowChat(ch connector.Chat) {
	key, err := strconv.ParseInt(ch.ID, 10, 64)
	if err != nil || !ch.Encrypted {
		return
	}
	tt := table.ENCRYPTED_OVER_WA_ONE_TO_ONE
	if ch.Group {
		tt = table.ENCRYPTED_OVER_WA_GROUP
	}
	c.mu.Lock()
	if !c.threadType[key].IsWhatsApp() {
		c.threadType[key] = tt
	}
	c.mu.Unlock()
}

// ---- people ----

// LookupPeople fetches names for senders we only know by ID (for example
// in a group opened from search, whose member list never synced).
func (c *Conn) LookupPeople(ctx context.Context, userIDs []string) {
	for _, u := range userIDs {
		uid, err := strconv.ParseInt(u, 10, 64)
		if err != nil || uid == 0 {
			continue
		}
		resp, err := c.cli.ExecuteTasks(ctx, &socket.GetContactsFullTask{ContactID: uid})
		if err != nil {
			c.log.Warn().Err(err).Msg("couldn't look up a name")
			continue
		}
		c.handleTable(resp)
	}
}

func (c *Conn) SearchPeople(ctx context.Context, query string) ([]connector.Person, error) {
	resp, err := c.cli.ExecuteTasks(ctx, &socket.SearchUserTask{
		Query: query,
		SupportedTypes: []table.SearchType{
			table.SearchTypeContact, table.SearchTypeNonContact, table.SearchTypeGroup, table.SearchTypePage,
		},
		SurfaceType: 5,
	})
	if err != nil {
		return nil, err
	}
	var out []connector.Person
	seen := map[int64]bool{}
	for _, r := range resp.LSInsertSearchResult {
		if !r.ThreadType.IsOneToOne() { // a group chat you're in
			key, _ := strconv.ParseInt(r.ResultId, 10, 64)
			if key == 0 || seen[key] || r.DisplayName == "" {
				continue
			}
			seen[key] = true
			c.mu.Lock()
			if _, known := c.threadType[key]; !known {
				c.threadType[key] = r.ThreadType
			}
			c.mu.Unlock()
			out = append(out, connector.Person{ID: id(key), Name: clean(r.DisplayName), Group: true,
				Encrypted: r.ThreadType.IsWhatsApp()})
			continue
		}
		fbid := r.GetFBID()
		canMessage := r.CanViewerMessage || (r.OtherUserId != 0 && r.BlockedByViewerStatus == 0)
		if !r.ThreadType.IsOneToOne() || !canMessage || fbid == 0 || fbid == c.self || seen[fbid] {
			continue
		}
		seen[fbid] = true
		c.contact(fbid, r.DisplayName)
		out = append(out, connector.Person{ID: id(fbid), Name: clean(r.DisplayName)})
	}
	return out, nil
}

// OpenDirect: 1:1 chats on Messenger are encrypted, and the person's user
// ID is the chat ID. Sending to it works straight away; nothing needs to
// be created on Messenger first.
func (c *Conn) OpenDirect(ctx context.Context, userID string) (string, error) {
	key, err := strconv.ParseInt(userID, 10, 64)
	if err != nil {
		return "", err
	}
	c.mu.Lock()
	if _, known := c.threadType[key]; !known {
		c.threadType[key] = table.ENCRYPTED_OVER_WA_ONE_TO_ONE
	}
	c.mu.Unlock()
	return userID, nil
}

// ---- media ----

func (c *Conn) FetchMedia(ctx context.Context, m connector.Media) ([]byte, error) {
	if m.Ref == "" {
		return nil, errors.New("no media to fetch")
	}
	if prefix, kind, ok := waMedia(m.Ref); ok {
		c.mu.Lock()
		wa := c.wa
		c.mu.Unlock()
		var err error
		if wa, err = c.readyWA(ctx, wa); err != nil {
			return nil, err
		}
		b, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(m.Ref, prefix))
		if err != nil {
			return nil, err
		}
		var t waMediaTransport.WAMediaTransport
		if err := proto.Unmarshal(b, &t); err != nil {
			return nil, err
		}
		if size := t.GetAncillary().GetFileLength(); size > maxDownload(m.Kind) {
			return nil, fmt.Errorf("it's %s, over tuon's %s limit: open it on your phone", mb(int64(size)), mb(int64(maxDownload(m.Kind))))
		}
		return wa.DownloadFB(ctx, t.GetIntegral(), kind)
	}
	if err := checkMediaURL(m.Ref); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.Ref, nil)
	if err != nil {
		return nil, err
	}
	resp, err := mediaHTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("media: HTTP %d (link may have expired)", resp.StatusCode)
	}
	return readCapped(resp.Body, int64(maxDownload(m.Kind)))
}

// waMedia: the prefix and whatsmeow media type of an encrypted media ref.
func waMedia(ref string) (prefix string, kind whatsmeow.MediaType, ok bool) {
	switch {
	case strings.HasPrefix(ref, waImagePrefix):
		return waImagePrefix, whatsmeow.MediaImage, true
	case strings.HasPrefix(ref, waDocPrefix):
		return waDocPrefix, whatsmeow.MediaDocument, true
	}
	return "", "", false
}

// maxDownload: photos stay small (SECURITY.md promises 25 MB); files may be
// bigger, but never unbounded.
func maxDownload(k connector.MediaKind) uint64 {
	if k == connector.File {
		return 100 << 20
	}
	return 25 << 20
}

// readCapped reads all of r, or refuses if it's bigger than limit: never a
// cut-off file.
func readCapped(r io.Reader, limit int64) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("it's over tuon's %s limit: open it on your phone", mb(limit))
	}
	return b, nil
}

func mb(n int64) string { return fmt.Sprintf("%d MB", n>>20) }

// mediaHTTP downloads photos: bounded time, and redirects must stay on
// Meta's https media hosts.
var mediaHTTP = &http.Client{
	Timeout: 60 * time.Second,
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("too many redirects")
		}
		return checkMediaURL(req.URL.String())
	},
}

// checkMediaURL: photos only come from Meta's media servers, over https.
func checkMediaURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" {
		return errors.New("media link isn't https")
	}
	host := strings.ToLower(u.Hostname())
	for _, ok := range []string{"fbcdn.net", "facebook.com", "fbsbx.com", "messenger.com", "fbcdn.com"} {
		if host == ok || strings.HasSuffix(host, "."+ok) {
			return nil
		}
	}
	return fmt.Errorf("media from %s isn't from Messenger, so it wasn't loaded", host)
}

// ---- helpers ----

func pendingID(otid string) string { return "pending:" + otid }

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// cleanURL: Facebook links sometimes come without the host, and outside
// links come wrapped in Facebook's redirect (l.php?u=...).
func cleanURL(u string) string {
	if strings.HasPrefix(u, "/") {
		u = "https://www.facebook.com" + u
	}
	if p, err := url.Parse(u); err == nil && p.Path == "/l.php" {
		if real := p.Query().Get("u"); real != "" {
			return real
		}
	}
	return u
}

func id(n int64) string { return strconv.FormatInt(n, 10) }

// clean makes a one-line field (name, title, snippet) safe to show.
func clean(s string) string { return connector.SafeText(html.UnescapeString(s), false) }

// cleanBody makes a message body safe to show, keeping its line breaks.
func cleanBody(s string) string { return connector.SafeText(html.UnescapeString(s), true) }

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }
