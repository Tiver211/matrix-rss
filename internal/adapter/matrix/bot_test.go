package matrix

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"github.com/greatgoodtiver/matrixrss/internal/domain"
)

// fakeHomeserver answers the two requests the bot makes to judge who is who in a room.
type fakeHomeserver struct {
	createSender string // who created the room; with roomVersion it feeds the create event
	roomVersion  string
	powerLevels  map[string]int // user ID -> level; nil: the room has no power levels event
	joined       []string
	failMembers  bool
}

func (f *fakeHomeserver) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	switch {
	case strings.Contains(r.URL.Path, "/state/m.room.create"):
		if f.createSender == "" {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"errcode":"M_NOT_FOUND","error":"no such state"}`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"type": "m.room.create", "state_key": "", "sender": f.createSender,
			"content": map[string]any{"room_version": f.roomVersion},
		})
	case strings.Contains(r.URL.Path, "/state/m.room.power_levels"):
		if f.powerLevels == nil {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"errcode":"M_NOT_FOUND","error":"no such state"}`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"users": f.powerLevels, "users_default": 0})
	case strings.HasSuffix(r.URL.Path, "/joined_members"):
		if f.failMembers {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"errcode":"M_FORBIDDEN","error":"not in room"}`))
			return
		}
		members := map[string]any{}
		for _, user := range f.joined {
			members[user] = map[string]any{}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"joined": members})
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func newTestBot(t *testing.T, homeserver *fakeHomeserver, level int) *Bot {
	t.Helper()
	server := httptest.NewServer(homeserver)
	t.Cleanup(server.Close)
	client, err := mautrix.NewClient(server.URL, "@bot:hs", "token")
	if err != nil {
		t.Fatal(err)
	}
	return &Bot{client: client, log: zerolog.Nop(), roomAdminLevel: level, now: time.Now}
}

func TestIsRoomAdmin(t *testing.T) {
	room := "!r:hs"
	levels := map[string]int{"@admin:hs": 100, "@mod:hs": 50, "@gone:hs": 100}
	members := []string{"@admin:hs", "@mod:hs", "@member:hs", "@bot:hs"} // @gone:hs left the room

	for _, tc := range []struct {
		name  string
		level int
		user  string
		want  bool
	}{
		{"admin at the default level", 100, "@admin:hs", true},
		{"moderator below the default level", 100, "@mod:hs", false},
		{"moderator when moderators count", 50, "@mod:hs", true},
		{"plain member", 50, "@member:hs", false},
		{"admin who left the room", 100, "@gone:hs", false},
		{"unknown user", 100, "@nobody:hs", false},
		{"feature turned off", 0, "@admin:hs", false},
	} {
		bot := newTestBot(t, &fakeHomeserver{powerLevels: levels, joined: members}, tc.level)
		if got := bot.IsRoomAdmin(t.Context(), room, tc.user); got != tc.want {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}

// In room version 12 the creator has unlimited power and is not listed among the power levels.
func TestIsRoomAdminRecognisesCreatorsOfRoomVersion12(t *testing.T) {
	members := []string{"@creator:hs", "@other:hs"}
	v12 := &fakeHomeserver{createSender: "@creator:hs", roomVersion: "12", powerLevels: map[string]int{"@other:hs": 50}, joined: members}
	bot := newTestBot(t, v12, 100)
	if !bot.IsRoomAdmin(t.Context(), "!r:hs", "@creator:hs") {
		t.Error("the creator of a v12 room is an admin although the power levels do not list them")
	}
	if bot.IsRoomAdmin(t.Context(), "!r:hs", "@other:hs") {
		t.Error("other members keep their listed level")
	}

	v10 := &fakeHomeserver{createSender: "@creator:hs", roomVersion: "10", powerLevels: map[string]int{"@other:hs": 50}, joined: members}
	if newTestBot(t, v10, 100).IsRoomAdmin(t.Context(), "!r:hs", "@creator:hs") {
		t.Error("before v12 the creator has only what the power levels say")
	}
}

func TestIsRoomAdminSaysNoWhenTheHomeserverCannotTell(t *testing.T) {
	noLevels := newTestBot(t, &fakeHomeserver{joined: []string{"@admin:hs"}}, 100)
	if noLevels.IsRoomAdmin(t.Context(), "!r:hs", "@admin:hs") {
		t.Error("a room without readable power levels must not grant rights")
	}
	noMembers := newTestBot(t, &fakeHomeserver{powerLevels: map[string]int{"@admin:hs": 100}, failMembers: true}, 100)
	if noMembers.IsRoomAdmin(t.Context(), "!r:hs", "@admin:hs") {
		t.Error("unreadable membership must not grant rights")
	}
}

func TestIsPrivateChat(t *testing.T) {
	for name, tc := range map[string]struct {
		joined      []string
		failMembers bool
		want        bool
	}{
		"bot and the user":       {joined: []string{"@bot:hs", "@admin:hs"}, want: true},
		"a third member":         {joined: []string{"@bot:hs", "@admin:hs", "@other:hs"}},
		"the user is not inside": {joined: []string{"@bot:hs", "@other:hs"}},
		"the bot is not inside":  {joined: []string{"@admin:hs", "@other:hs"}},
		"alone with the user":    {joined: []string{"@admin:hs"}},
		"members unreadable":     {failMembers: true},
	} {
		bot := newTestBot(t, &fakeHomeserver{joined: tc.joined, failMembers: tc.failMembers}, 100)
		if got := bot.IsPrivateChat(t.Context(), "!dm:hs", "@admin:hs"); got != tc.want {
			t.Errorf("%s: got %v, want %v", name, got, tc.want)
		}
	}
}

func TestBotTrafficIsNotRecordedFromHistory(t *testing.T) {
	bot := newTestBot(t, &fakeHomeserver{}, 100)
	bot.commands = NewCommands("!rss", []string{"@admin:hs"}, CommandDeps{})

	message := func(sender id.UserID, body string) *event.Event {
		return &event.Event{Sender: sender, Type: event.EventMessage, Content: event.Content{Parsed: &event.MessageEventContent{MsgType: event.MsgText, Body: body}}}
	}
	for name, tc := range map[string]struct {
		evt  *event.Event
		want bool
	}{
		"the bot's own reply with a secret": {message("@bot:hs", "Feed enabled.\nhttps://rss/feeds/secret"), true},
		"a command":                         {message("@admin:hs", "!rss key list"), true},
		"a command by a stranger":           {message("@carol:hs", "!RSS on"), true},
		"an ordinary message":               {message("@admin:hs", "deploy is green"), false},
		"a message that merely mentions it": {message("@admin:hs", "try !rss on later"), false},
	} {
		if got := bot.isBotTraffic(tc.evt); got != tc.want {
			t.Errorf("%s: got %v, want %v", name, got, tc.want)
		}
	}
}

type fakeFeedList []string

func (f fakeFeedList) ListEnabled(context.Context) ([]domain.Feed, error) {
	feeds := make([]domain.Feed, len(f))
	for i, room := range f {
		feeds[i] = domain.Feed{RoomID: room, Enabled: true}
	}
	return feeds, nil
}

func TestWhoseInvitationsTheBotAccepts(t *testing.T) {
	homeserver := &fakeHomeserver{
		powerLevels: map[string]int{"@roomadmin:hs": 100, "@mod:hs": 50},
		joined:      []string{"@roomadmin:hs", "@mod:hs", "@stranger:hs", "@bot:hs"},
	}
	bot := newTestBot(t, homeserver, 100)
	bot.commands = NewCommands("!rss", []string{"@admin:hs"}, CommandDeps{})
	bot.feedList = fakeFeedList{"!published:hs"}

	invite := func(inviter id.UserID, direct bool) bool {
		evt := &event.Event{Sender: inviter, RoomID: "!new:hs"}
		return bot.shouldJoin(t.Context(), evt, &event.MemberEventContent{Membership: event.MembershipInvite, IsDirect: direct})
	}
	for name, tc := range map[string]struct {
		inviter id.UserID
		direct  bool
		want    bool
	}{
		"a bot admin, any room":                  {"@admin:hs", false, true},
		"a bot admin, private chat":              {"@admin:hs", true, true},
		"a room admin starting a private chat":   {"@roomadmin:hs", true, true},
		"a room admin inviting to a shared room": {"@roomadmin:hs", false, false},
		"a moderator below the required level":   {"@mod:hs", true, false},
		"a stranger starting a private chat":     {"@stranger:hs", true, false},
		"a stranger inviting to a room":          {"@stranger:hs", false, false},
	} {
		if got := invite(tc.inviter, tc.direct); got != tc.want {
			t.Errorf("%s: got %v, want %v", name, got, tc.want)
		}
	}

	bot.feedList = fakeFeedList{} // nothing is published, so there is nothing to administer
	if invite("@roomadmin:hs", true) {
		t.Error("with no published rooms nobody is a room admin")
	}
}

func TestOldCommandsAreNotCarriedOut(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	bot := newTestBot(t, &fakeHomeserver{}, 100)
	bot.now = func() time.Time { return now }

	at := func(age time.Duration) *event.Event {
		return &event.Event{Timestamp: now.Add(-age).UnixMilli()}
	}
	for name, tc := range map[string]struct {
		age  time.Duration
		want bool
	}{
		"just sent":                {0, false},
		"a minute ago":             {time.Minute, false},
		"just inside the limit":    {commandMaxAge - time.Second, false},
		"just outside the limit":   {commandMaxAge + time.Second, true},
		"yesterday":                {24 * time.Hour, true},
		"slightly from the future": {-10 * time.Second, false}, // clock differences between servers
	} {
		if got := bot.isStale(at(tc.age)); got != tc.want {
			t.Errorf("%s: got %v, want %v", name, got, tc.want)
		}
	}
}
