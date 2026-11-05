package messenger

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"
	zlog "github.com/rs/zerolog/log"
	"go.mau.fi/mautrix-meta/pkg/messagix"
	"go.mau.fi/mautrix-meta/pkg/messagix/cookies"
	"go.mau.fi/mautrix-meta/pkg/messagix/table"
	"go.mau.fi/mautrix-meta/pkg/messagix/types"

	"github.com/ralph/tuon/internal/auth"
	"github.com/ralph/tuon/internal/connector"
)

// TestLiveThreadTypes counts which kinds of chats Messenger sends, page by
// page. Read-only.
//
//	TUON_LIVE=1 go test ./internal/connector/messenger -run TestLiveThreadTypes -v
func TestLiveThreadTypes(t *testing.T) {
	if os.Getenv("TUON_LIVE") == "" {
		t.Skip("set TUON_LIVE=1")
	}
	zerolog.SetGlobalLevel(zerolog.ErrorLevel)
	zlog.Logger = zerolog.Nop()
	ck := &cookies.Cookies{Platform: types.Messenger}
	ck.UpdateValues(toCookieMap(liveCreds(t).Cookies))
	cli := messagix.NewClient(ck, zerolog.Nop(), &messagix.Config{})
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	_, tbl, err := cli.LoadMessagesPage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	count := func(label string, tbl *table.LSTable) {
		types := map[table.ThreadType]int{}
		for _, th := range tbl.LSDeleteThenInsertThread {
			types[th.ThreadType]++
		}
		t.Logf("%s: threads by type %v | updateOrInsert=%d verifyHybrid=%d mappingFromJID=%d verifyExists=%d",
			label, types, len(tbl.LSUpdateOrInsertThread), len(tbl.LSVerifyHybridThreadExists),
			len(tbl.LSUpdateThreadAuthorityAndMappingWithOTIDFromJID), len(tbl.LSVerifyThreadExists))
	}
	count("initial", tbl)

	connected := make(chan struct{}, 1)
	cli.SetEventHandler(func(ctx context.Context, evt any) {
		if _, ok := evt.(*messagix.ConnectedEvent); ok {
			select {
			case connected <- struct{}{}:
			default:
			}
		}
	})
	if err := cli.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	defer cli.Disconnect()
	select {
	case <-connected:
	case <-time.After(30 * time.Second):
		t.Fatal("no connect")
	}
	for _, group := range []int64{1, 95, 104} {
		for page := 0; page < 4; page++ {
			ks, tb, err := cli.FetchMoreThreads(ctx, group)
			if err != nil {
				t.Logf("group %d page %d: %v", group, page, err)
				break
			}
			if tb == nil {
				t.Logf("group %d page %d: no more", group, page)
				break
			}
			count("group "+itoa(group)+" page "+itoa(int64(page)), tb)
			if ks == nil || !ks.HasMoreBefore {
				break
			}
		}
	}
}

func itoa(n int64) string { return id(n) }

// TestLiveSearchPeople runs a real people search. Read-only.
//
//	TUON_LIVE=1 TUON_Q=name go test ./internal/connector/messenger -run TestLiveSearchPeople -v
func TestLiveSearchPeople(t *testing.T) {
	if os.Getenv("TUON_LIVE") == "" {
		t.Skip("set TUON_LIVE=1")
	}
	zerolog.SetGlobalLevel(zerolog.ErrorLevel)
	zlog.Logger = zerolog.Nop()
	c := New(liveCreds(t), nil, zerolog.Nop())
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	c.ctx, c.cancel = context.WithCancel(ctx)
	ck := &cookies.Cookies{Platform: types.Messenger}
	ck.UpdateValues(toCookieMap(c.creds.Cookies))
	c.cli = messagix.NewClient(ck, zerolog.Nop(), &messagix.Config{})
	if _, _, err := c.cli.LoadMessagesPage(ctx); err != nil {
		t.Fatal(err)
	}
	connected := make(chan struct{}, 1)
	c.cli.SetEventHandler(func(_ context.Context, e any) {
		if _, ok := e.(*messagix.ConnectedEvent); ok {
			select {
			case connected <- struct{}{}:
			default:
			}
		}
	})
	go func() { // drain events so emit never blocks
		for range c.events {
		}
	}()
	if err := c.cli.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	defer c.cli.Disconnect()
	<-connected
	people, err := c.SearchPeople(ctx, os.Getenv("TUON_Q"))
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%d people found; first names: %v", len(people), firstNames(people))
}

func firstNames(ps []connector.Person) []string {
	var out []string
	for _, p := range ps {
		out = append(out, strings.Fields(p.Name)[0])
	}
	return out
}

func liveCreds(t *testing.T) auth.Creds {
	home, _ := os.UserHomeDir()
	c, err := auth.Load(filepath.Join(home, ".config", "tuon"))
	if err != nil {
		t.Skip("not logged in")
	}
	return c
}

func toCookieMap(m map[string]string) map[cookies.MetaCookieName]string {
	out := map[cookies.MetaCookieName]string{}
	for k, v := range m {
		out[cookies.MetaCookieName(k)] = v
	}
	return out
}
