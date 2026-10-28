//go:build e2e

// End-to-end tests: real Matrix clients (with E2EE) against a local Conduit homeserver
// and a running bot. Run them as described in e2e/README.md.
package e2e

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	_ "github.com/glebarez/go-sqlite"
	"github.com/rs/zerolog"
	"go.mau.fi/util/dbutil"
	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/crypto/cryptohelper"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

const (
	homeserver = "http://localhost:6167"
	apiBase    = "http://127.0.0.1:8080"
	rootToken  = "root-token-for-e2e-0123456789"
	botID      = id.UserID("@rssbot:localhost")
)

var feedURLRe = regexp.MustCompile(`http://127\.0\.0\.1:8080/feeds/[A-Za-z0-9_-]+`)

type received struct {
	room   id.RoomID
	sender id.UserID
	id     id.EventID
	body   string
}

type user struct {
	t      *testing.T
	name   string
	id     id.UserID
	client *mautrix.Client
	mu     sync.Mutex
	msgs   []received
}

func newUser(t *testing.T, name string) *user {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	client, err := mautrix.NewClient(homeserver, "", "")
	if err != nil {
		t.Fatal(err)
	}
	client.Log = zerolog.Nop()

	dbPath := filepath.Join(os.Getenv("E2E_DIR"), "users", name+".db")
	_ = os.MkdirAll(filepath.Dir(dbPath), 0o755)
	raw, err := sql.Open("sqlite", "file:"+dbPath+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)")
	if err != nil {
		t.Fatal(err)
	}
	db, err := dbutil.NewWithDB(raw, "sqlite3")
	if err != nil {
		t.Fatal(err)
	}
	helper, err := cryptohelper.NewCryptoHelper(client, []byte("pickle-key-"+name), db)
	if err != nil {
		t.Fatal(err)
	}
	helper.LoginAs = &mautrix.ReqLogin{
		Type:       mautrix.AuthTypePassword,
		Identifier: mautrix.UserIdentifier{Type: mautrix.IdentifierTypeUser, User: name},
		Password:   name + "-pass",
	}
	if err := helper.Init(ctx); err != nil {
		t.Fatalf("init %s: %v", name, err)
	}
	client.Crypto = helper

	u := &user{t: t, name: name, id: client.UserID, client: client}
	client.Syncer.(*mautrix.DefaultSyncer).OnEventType(event.EventMessage, func(_ context.Context, evt *event.Event) {
		if m := evt.Content.AsMessage(); m != nil {
			u.mu.Lock()
			u.msgs = append(u.msgs, received{evt.RoomID, evt.Sender, evt.ID, m.Body})
			u.mu.Unlock()
		}
	})
	go func() { _ = client.SyncWithContext(ctx) }()
	time.Sleep(1500 * time.Millisecond) // let the first sync settle
	return u
}

func eventually(t *testing.T, what string, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for: %s", what)
}

func (u *user) mark() int {
	u.mu.Lock()
	defer u.mu.Unlock()
	return len(u.msgs)
}

// waitFrom waits for a message after the mark that contains substr (from anyone but this user).
func (u *user) waitFrom(mark int, room id.RoomID, substr string) string {
	u.t.Helper()
	var found string
	eventually(u.t, fmt.Sprintf("%s to receive %q in %s", u.name, substr, room), 20*time.Second, func() bool {
		u.mu.Lock()
		defer u.mu.Unlock()
		for _, m := range u.msgs[mark:] {
			if m.room == room && m.sender != u.id && strings.Contains(m.body, substr) {
				found = m.body
				return true
			}
		}
		return false
	})
	return found
}

// ask sends a command and returns the bot's reply.
func (u *user) ask(room id.RoomID, command, replyContains string) string {
	u.t.Helper()
	mark := u.mark()
	u.say(room, command)
	return u.waitFrom(mark, room, replyContains)
}

// replyTo sends a command and returns the first reply of the bot, whatever it says.
func (u *user) replyTo(room id.RoomID, command string) string {
	u.t.Helper()
	mark := u.mark()
	u.say(room, command)
	var reply string
	eventually(u.t, "a reply to "+command, 20*time.Second, func() bool {
		u.mu.Lock()
		defer u.mu.Unlock()
		for _, m := range u.msgs[mark:] {
			if m.room == room && m.sender == botID {
				reply = m.body
				return true
			}
		}
		return false
	})
	return reply
}

func (u *user) say(room id.RoomID, text string) id.EventID {
	u.t.Helper()
	resp, err := u.client.SendText(context.Background(), room, text)
	if err != nil {
		u.t.Fatalf("%s says %q: %v", u.name, text, err)
	}
	return resp.EventID
}

func (u *user) edit(room id.RoomID, target id.EventID, newBody string) id.EventID {
	u.t.Helper()
	resp, err := u.client.SendMessageEvent(context.Background(), room, event.EventMessage, &event.MessageEventContent{
		MsgType:    event.MsgText,
		Body:       "* " + newBody,
		NewContent: &event.MessageEventContent{MsgType: event.MsgText, Body: newBody},
		RelatesTo:  &event.RelatesTo{Type: event.RelReplace, EventID: target},
	})
	if err != nil {
		u.t.Fatalf("%s edits: %v", u.name, err)
	}
	return resp.EventID
}

func (u *user) redact(room id.RoomID, target id.EventID, reason string) {
	u.t.Helper()
	if _, err := u.client.RedactEvent(context.Background(), room, target, mautrix.ReqRedact{Reason: reason}); err != nil {
		u.t.Fatalf("%s redacts: %v", u.name, err)
	}
}

func (u *user) createRoom(name string, encrypted bool, invite ...id.UserID) id.RoomID {
	u.t.Helper()
	req := &mautrix.ReqCreateRoom{Name: name, Invite: invite, Preset: "private_chat"}
	if encrypted {
		req.InitialState = []*event.Event{{
			Type:    event.StateEncryption,
			Content: event.Content{Parsed: &event.EncryptionEventContent{Algorithm: id.AlgorithmMegolmV1}},
		}}
	}
	resp, err := u.client.CreateRoom(context.Background(), req)
	if err != nil {
		u.t.Fatalf("%s creates room: %v", u.name, err)
	}
	if encrypted {
		eventually(u.t, "room to be known as encrypted", 10*time.Second, func() bool {
			ok, _ := u.client.StateStore.IsEncrypted(context.Background(), resp.RoomID)
			return ok
		})
	}
	return resp.RoomID
}

func (u *user) join(room id.RoomID) {
	u.t.Helper()
	eventually(u.t, u.name+" to join "+string(room), 15*time.Second, func() bool {
		_, err := u.client.JoinRoomByID(context.Background(), room)
		return err == nil
	})
}

func (u *user) invite(room id.RoomID, who id.UserID) {
	u.t.Helper()
	if _, err := u.client.InviteUser(context.Background(), room, &mautrix.ReqInviteUser{UserID: who}); err != nil {
		u.t.Fatalf("%s invites %s: %v", u.name, who, err)
	}
}

func (u *user) members(room id.RoomID) map[id.UserID]mautrix.JoinedMember {
	resp, err := u.client.JoinedMembers(context.Background(), room)
	if err != nil {
		return nil
	}
	return resp.Joined
}

func (u *user) waitJoined(room id.RoomID, who id.UserID) {
	u.t.Helper()
	eventually(u.t, string(who)+" to be in "+string(room), 20*time.Second, func() bool {
		_, ok := u.members(room)[who]
		return ok
	})
}

func (u *user) makeAdmin(room id.RoomID, who id.UserID, level int) {
	u.t.Helper()
	var pl event.PowerLevelsEventContent
	if err := u.client.StateEvent(context.Background(), room, event.StatePowerLevels, "", &pl); err != nil {
		u.t.Fatal(err)
	}
	if pl.Users == nil {
		pl.Users = map[id.UserID]int{}
	}
	pl.Users[who] = level
	if _, err := u.client.SendStateEvent(context.Background(), room, event.StatePowerLevels, "", &pl); err != nil {
		u.t.Fatal(err)
	}
}

func (u *user) directChat(with id.UserID) id.RoomID {
	u.t.Helper()
	resp, err := u.client.CreateRoom(context.Background(), &mautrix.ReqCreateRoom{
		Invite: []id.UserID{with}, IsDirect: true, Preset: "trusted_private_chat",
	})
	if err != nil {
		u.t.Fatal(err)
	}
	return resp.RoomID
}

// ---- HTTP helpers

func httpGet(t *testing.T, path, bearer string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, apiBase+path, nil)
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body)
}

func feedURL(t *testing.T, text string) string {
	t.Helper()
	url := feedURLRe.FindString(text)
	if url == "" {
		t.Fatalf("no feed URL in %q", text)
	}
	return url
}

func fetch(t *testing.T, url string) (int, string) {
	t.Helper()
	return httpGet(t, strings.TrimPrefix(url, apiBase), "")
}

func roomPath(room id.RoomID) string {
	return strings.NewReplacer("!", "%21", ":", "%3A").Replace(string(room))
}

func messagesOf(t *testing.T, room id.RoomID, query string) string {
	t.Helper()
	status, body := httpGet(t, "/api/v1/rooms/"+roomPath(room)+"/messages"+query, rootToken)
	if status != 200 {
		t.Fatalf("messages %s: status %d: %s", room, status, body)
	}
	return body
}
