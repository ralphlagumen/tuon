// Command spike checks that messagix can log in with browser cookies,
// list chats, and send a message. Throwaway code for milestone 1.
//
// Usage:
//
//	./bin/spike                              # reads ~/.config/tuon/cookies.txt
//	./bin/spike -send <threadID> -text "hi from tuon"
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/rs/zerolog"
	"go.mau.fi/mautrix-meta/pkg/messagix"
	"go.mau.fi/mautrix-meta/pkg/messagix/cookies"
	"go.mau.fi/mautrix-meta/pkg/messagix/methods"
	"go.mau.fi/mautrix-meta/pkg/messagix/socket"
	"go.mau.fi/mautrix-meta/pkg/messagix/table"
	"go.mau.fi/mautrix-meta/pkg/messagix/types"
)

func main() {
	sendTo := flag.Int64("send", 0, "thread ID to send a message to")
	text := flag.String("text", "hi from tuon", "message text")
	fbSite := flag.Bool("facebook", false, "cookies came from facebook.com (default: messenger.com)")
	debug := flag.Bool("debug", false, "verbose logs")
	e2ee := flag.Bool("e2ee", false, "connect encrypted 1:1 chats")
	sendUser := flag.Int64("send-user", 0, "with -e2ee: person's user ID to send an encrypted message to")
	listen := flag.Duration("listen", 60*time.Second, "with -e2ee: how long to listen")
	flag.Parse()

	raw := os.Getenv("TUON_COOKIES")
	if raw == "" {
		home, _ := os.UserHomeDir()
		b, err := os.ReadFile(home + "/.config/tuon/cookies.txt")
		if err != nil {
			fail("put your messenger.com cookie string in ~/.config/tuon/cookies.txt (or set TUON_COOKIES)")
		}
		raw = strings.TrimSpace(string(b))
	}

	platform := types.Messenger
	if *fbSite {
		platform = types.Facebook
	}
	c := &cookies.Cookies{Platform: platform}
	c.UpdateValues(parseCookies(raw))
	if missing := c.GetMissingCookieNames(); len(missing) > 0 {
		fail(fmt.Sprintf("missing cookies: %v", missing))
	}

	level := zerolog.ErrorLevel
	if *debug {
		level = zerolog.DebugLevel
	}
	zerolog.SetGlobalLevel(level) // messagix also logs through the global logger
	log := zerolog.New(zerolog.ConsoleWriter{Out: os.Stderr}).Level(level).With().Timestamp().Logger()

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second+*listen)
	defer cancel()
	ctx = log.WithContext(ctx)

	cli := messagix.NewClient(c, log, &messagix.Config{})
	user, tbl, err := cli.LoadMessagesPage(ctx)
	if err != nil {
		fail("load inbox: " + err.Error())
	}
	fmt.Printf("logged in as %s (id %d)\n\n", user.GetName(), user.GetFBID())

	printThreads(tbl, user.GetFBID())

	if *e2ee {
		runE2EE(ctx, cli, user.GetFBID(), *sendUser, *text, *listen, log)
		return
	}
	if *sendTo == 0 {
		return
	}

	cli.SetEventHandler(func(ctx context.Context, evt any) {
		if t, ok := evt.(*table.LSTable); ok {
			for _, m := range t.LSInsertMessage {
				fmt.Printf("  <- [%d] %d: %s\n", m.ThreadKey, m.SenderId, m.Text)
			}
		}
	})
	if err := cli.Connect(ctx); err != nil {
		fail("connect: " + err.Error())
	}
	defer cli.Disconnect()
	if err := cli.WaitUntilCanSendMessages(ctx, 30*time.Second); err != nil {
		fail("socket not ready: " + err.Error())
	}
	_, err = cli.ExecuteTasks(ctx, &socket.SendMessageTask{
		ThreadId:         *sendTo,
		Otid:             methods.GenerateEpochID(),
		Source:           table.MESSENGER_INBOX_IN_THREAD,
		InitiatingSource: table.FACEBOOK_INBOX,
		SendType:         table.TEXT,
		SyncGroup:        1,
		Text:             *text,
	})
	if err != nil {
		fail("send: " + err.Error())
	}
	fmt.Printf("\nsent %q to thread %d\n", *text, *sendTo)
	fmt.Println("listening 20s for incoming messages...")
	select {
	case <-time.After(20 * time.Second):
	case <-ctx.Done():
	}
}

func printThreads(tbl *table.LSTable, me int64) {
	names := map[int64]string{}
	for _, ct := range tbl.LSVerifyContactRowExists {
		names[ct.ContactId] = ct.Name
	}
	threads := tbl.LSDeleteThenInsertThread
	sort.Slice(threads, func(i, j int) bool {
		return threads[i].LastActivityTimestampMs > threads[j].LastActivityTimestampMs
	})
	fmt.Printf("%d chats in first page:\n", len(threads))
	for _, t := range threads {
		name := t.ThreadName
		if name == "" {
			name = names[t.ThreadKey] // 1:1 chats: thread key = other person's ID
		}
		if name == "" {
			name = "(unnamed)"
		}
		unread := " "
		if t.LastReadWatermarkTimestampMs < t.LastActivityTimestampMs {
			unread = "●"
		}
		ago := time.Since(time.UnixMilli(t.LastActivityTimestampMs)).Round(time.Minute)
		fmt.Printf(" %s %-28.28s %-40.40s %8s  id=%d type=%d\n",
			unread, name, oneLine(t.Snippet), ago, t.ThreadKey, t.ThreadType)
	}
	fmt.Printf("\n%d messages in first page\n", len(tbl.LSInsertMessage)+len(tbl.LSUpsertMessage))
}

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

func oneLine(s string) string { return strings.ReplaceAll(s, "\n", " ") }

func fail(msg string) {
	fmt.Fprintln(os.Stderr, "error:", msg)
	os.Exit(1)
}
