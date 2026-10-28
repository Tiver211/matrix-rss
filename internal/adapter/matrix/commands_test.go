package matrix

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/greatgoodtiver/matrixrss/internal/domain"
)

type fakeFeeds struct{ err error }

func (f fakeFeeds) Enable(_ context.Context, roomID, title string) (domain.Feed, error) {
	return domain.Feed{RoomID: roomID, Title: title, Enabled: true}, f.err
}
func (f fakeFeeds) Disable(context.Context, string) error { return f.err }

type fakeAccess struct {
	err      error
	hasToken bool // the room already has an active token

	tokens map[uint64]domain.FeedToken
	keys   []domain.APIKey

	issued     []string
	revoked    []string
	rotated    int
	keyRequest domain.KeyRequest
}

func (f *fakeAccess) EnsureDefaultFeedToken(context.Context, string, string) (domain.FeedToken, string, error) {
	if f.hasToken {
		return domain.FeedToken{Name: "default"}, "", f.err
	}
	return domain.FeedToken{Name: "default"}, "secret-default", f.err
}
func (f *fakeAccess) RotateFeedTokens(context.Context, string, string) (domain.FeedToken, string, error) {
	f.rotated++
	return domain.FeedToken{}, "secret-rotated", f.err
}
func (f *fakeAccess) IssueFeedToken(_ context.Context, roomID, name, by string) (domain.FeedToken, string, error) {
	f.issued = append(f.issued, roomID+"/"+name+"/"+by)
	return domain.FeedToken{ID: 7, Name: name}, "secret-issued", f.err
}
func (f *fakeAccess) ListFeedTokens(context.Context, string) ([]domain.FeedToken, error) {
	revoked := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	return []domain.FeedToken{
		{ID: 1, Name: "default", Hint: "abcd1234", CreatedBy: "@a:hs"},
		{ID: 2, Name: "bob", Hint: "wxyz9876", CreatedBy: "@a:hs", RevokedAt: &revoked},
	}, f.err
}
func (f *fakeAccess) FindFeedToken(_ context.Context, id uint64) (domain.FeedToken, error) {
	if token, ok := f.tokens[id]; ok {
		return token, nil
	}
	return domain.FeedToken{}, domain.ErrCredentialNotFound
}
func (f *fakeAccess) RevokeFeedToken(_ context.Context, id uint64) error {
	f.revoked = append(f.revoked, "token")
	return f.err
}
func (f *fakeAccess) CreateAPIKey(_ context.Context, req domain.KeyRequest) (domain.APIKey, string, error) {
	f.keyRequest = req
	return domain.APIKey{ID: 3, Name: req.Name, Rooms: req.Rooms, ExpiresAt: req.ExpiresAt}, "mxk_secret", f.err
}
func (f *fakeAccess) ListAPIKeys(context.Context) ([]domain.APIKey, error) {
	if f.keys == nil {
		return []domain.APIKey{{ID: 3, Name: "grafana", Hint: "mxk_abcd"}}, f.err
	}
	return f.keys, f.err
}
func (f *fakeAccess) FindAPIKey(_ context.Context, id uint64) (domain.APIKey, error) {
	for _, key := range f.keys {
		if key.ID == id {
			return key, nil
		}
	}
	return domain.APIKey{}, domain.ErrCredentialNotFound
}
func (f *fakeAccess) RevokeAPIKey(_ context.Context, id uint64) error {
	f.revoked = append(f.revoked, "key")
	return f.err
}

type fakeRooms struct{}

func (fakeRooms) RoomName(context.Context, string) string { return "Room" }

type fakeChats struct{ private bool }

func (f fakeChats) IsPrivateChat(context.Context, string, string) bool { return f.private }

// fakeAuthority knows which users administer which rooms and counts its lookups.
type fakeAuthority struct {
	admins  map[string]bool // "room|user"
	lookups int
}

func (f *fakeAuthority) IsRoomAdmin(_ context.Context, roomID, userID string) bool {
	f.lookups++
	return f.admins[roomID+"|"+userID]
}

const (
	botAdmin  = "@admin:hs"
	roomAdmin = "@roomadmin:hs" // administers !r:hs only
	stranger  = "@stranger:hs"
)

type harness struct {
	cmds      *Commands
	access    *fakeAccess
	authority *fakeAuthority
}

func newHarness(access *fakeAccess, private bool) harness {
	authority := &fakeAuthority{admins: map[string]bool{"!r:hs|" + roomAdmin: true}}
	return harness{
		access:    access,
		authority: authority,
		cmds: NewCommands("!rss", []string{botAdmin}, CommandDeps{
			Feeds: fakeFeeds{}, Access: access, Rooms: fakeRooms{}, Chats: fakeChats{private: private}, Authority: authority,
			FeedURL: func(token string) string { return "https://rss/feeds/" + token },
		}),
	}
}

func (h harness) say(sender, text string) string {
	reply, _ := h.cmds.Handle(context.Background(), "!r:hs", sender, text)
	return reply
}

func TestCommandsIgnoreOrdinaryMessages(t *testing.T) {
	h := newHarness(&fakeAccess{}, false)
	for _, text := range []string{"", "hello", "!rssfoo on", "please !rss on"} {
		if _, ok := h.cmds.Handle(context.Background(), "!r:hs", botAdmin, text); ok {
			t.Errorf("%q must not be treated as a command", text)
		}
	}
}

func TestStrangersCanDoNothing(t *testing.T) {
	for _, private := range []bool{false, true} {
		access := &fakeAccess{keys: []domain.APIKey{{ID: 5, Name: "k", Rooms: []string{"!r:hs"}}}, tokens: map[uint64]domain.FeedToken{1: {ID: 1, RoomID: "!r:hs"}}}
		h := newHarness(access, private)

		for text, want := range map[string]string{
			"!rss on":                       botAdminsOnly,
			"!rss off":                      botAdminsOnly,
			"!rss rotate":                   denied,
			"!rss token list !r:hs":         denied,
			"!rss token create !r:hs bob":   denied,
			"!rss key create k rooms=!r:hs": denied,
			"!rss token revoke 1":           "no key or token",
			"!rss key revoke 5":             "no key or token",
		} {
			if reply := h.say(stranger, text); !strings.Contains(reply, want) {
				t.Errorf("private=%v %q: want %q, got %q", private, text, want, reply)
			}
		}
		if reply := h.say(stranger, "!rss key list"); !strings.Contains(reply, "No API keys") {
			t.Errorf("a stranger must not see keys: %q", reply)
		}
		if len(access.issued) != 0 || access.keyRequest.Name != "" || len(access.revoked) != 0 || access.rotated != 0 {
			t.Fatalf("nothing may change for a stranger: %+v", access)
		}
		if reply := h.say(stranger, "!rss"); !strings.Contains(reply, "Commands") {
			t.Errorf("help is open to everyone, got %q", reply)
		}
	}
}

func TestBotAdminsNeverNeedRoomRights(t *testing.T) {
	h := newHarness(&fakeAccess{}, true)
	reply := h.say(botAdmin, "!rss key create everything expires=30d")
	if !strings.Contains(reply, "mxk_secret") || len(h.access.keyRequest.Rooms) != 0 {
		t.Fatalf("a bot admin may issue an unrestricted key: %q / %+v", reply, h.access.keyRequest)
	}
	h.say(botAdmin, "!rss token create !any:hs bob")
	if h.authority.lookups != 0 || len(h.access.issued) != 1 {
		t.Fatalf("bot admins must not trigger room lookups: %d lookups, issued %v", h.authority.lookups, h.access.issued)
	}
}

func TestEnableShowsTheFeedURLOnlyWhenATokenWasIssued(t *testing.T) {
	reply := newHarness(&fakeAccess{}, false).say(botAdmin, "!rss on")
	if !strings.Contains(reply, "https://rss/feeds/secret-default") {
		t.Fatalf("first enable must show the URL: %q", reply)
	}
	reply = newHarness(&fakeAccess{hasToken: true}, false).say(botAdmin, "!rss on")
	if strings.Contains(reply, "https://") || !strings.Contains(reply, "token list") {
		t.Fatalf("existing tokens must not be shown again: %q", reply)
	}
}

func TestRoomAdminCanRotateAndListOwnRoomOnly(t *testing.T) {
	h := newHarness(&fakeAccess{}, false)

	if reply := h.say(roomAdmin, "!rss rotate"); !strings.Contains(reply, "secret-rotated") {
		t.Fatalf("a room admin may rotate the feed of their room: %q", reply)
	}
	if reply := h.say(roomAdmin, "!rss token list"); !strings.Contains(reply, "#1  default  abcd1234…") || !strings.Contains(reply, "REVOKED") {
		t.Fatalf("unexpected list:\n%s", reply)
	}
	if reply := h.say(roomAdmin, "!rss token list !other:hs"); reply != denied {
		t.Fatalf("another room must be refused, got %q", reply)
	}
	if reply := h.say(roomAdmin, "!rss on"); reply != botAdminsOnly {
		t.Fatalf("publishing stays with bot admins, got %q", reply)
	}
}

func TestRoomAdminTokensAreScopedToTheirRoom(t *testing.T) {
	access := &fakeAccess{tokens: map[uint64]domain.FeedToken{1: {ID: 1, RoomID: "!r:hs"}, 2: {ID: 2, RoomID: "!other:hs"}}}
	private := newHarness(access, true)

	if reply := private.say(roomAdmin, "!rss token create !r:hs bob"); !strings.Contains(reply, "https://rss/feeds/secret-issued") {
		t.Fatalf("own room in a private chat: %q", reply)
	}
	if reply := private.say(roomAdmin, "!rss token create !other:hs bob"); reply != denied {
		t.Fatalf("other room must be refused: %q", reply)
	}
	if len(access.issued) != 1 || access.issued[0] != "!r:hs/bob/"+roomAdmin {
		t.Fatalf("unexpected tokens issued: %v", access.issued)
	}

	if reply := private.say(roomAdmin, "!rss token revoke 2"); !strings.Contains(reply, "no key or token") || len(access.revoked) != 0 {
		t.Fatalf("a token of another room must look unknown: %q, revoked %v", reply, access.revoked)
	}
	if reply := private.say(roomAdmin, "!rss token revoke 1"); !strings.Contains(reply, "revoked") || len(access.revoked) != 1 {
		t.Fatalf("own token: %q, revoked %v", reply, access.revoked)
	}
}

func TestRoomAdminKeysAreLimitedToTheirRooms(t *testing.T) {
	access := &fakeAccess{}
	shared := newHarness(access, false)

	// In the shared room the instruction already contains the room limit.
	reply := shared.say(roomAdmin, "!rss key create grafana expires=7d")
	if !strings.Contains(reply, "private chat") || !strings.Contains(reply, "!rss key create grafana expires=7d rooms=!r:hs") || access.keyRequest.Name != "" {
		t.Fatalf("unexpected reply %q", reply)
	}

	private := newHarness(access, true)
	if reply := private.say(roomAdmin, "!rss key create grafana"); !strings.Contains(reply, "limit the key") || access.keyRequest.Name != "" {
		t.Fatalf("an unrestricted key must be refused: %q", reply)
	}
	if reply := private.say(roomAdmin, "!rss key create grafana rooms=!r:hs,!other:hs"); reply != denied || access.keyRequest.Name != "" {
		t.Fatalf("a key reaching another room must be refused: %q", reply)
	}
	if reply := private.say(roomAdmin, "!rss key create grafana rooms=!r:hs"); !strings.Contains(reply, "mxk_secret") || len(access.keyRequest.Rooms) != 1 || access.keyRequest.CreatedBy != roomAdmin {
		t.Fatalf("own room: %q / %+v", reply, access.keyRequest)
	}
}

func TestRoomAdminSeesAndRevokesOnlyKeysOfTheirRooms(t *testing.T) {
	access := &fakeAccess{keys: []domain.APIKey{
		{ID: 1, Name: "global", CreatedBy: botAdmin},                  // all rooms
		{ID: 2, Name: "mine", Rooms: []string{"!r:hs"}},               // fully theirs
		{ID: 3, Name: "mixed", Rooms: []string{"!r:hs", "!other:hs"}}, // reaches another room
		{ID: 4, Name: "elsewhere", Rooms: []string{"!other:hs"}},      // not theirs
	}}
	h := newHarness(access, false)

	list := h.say(roomAdmin, "!rss key list")
	if !strings.Contains(list, "mine") || strings.Contains(list, "global") || strings.Contains(list, "mixed") || strings.Contains(list, "elsewhere") {
		t.Fatalf("a room admin sees only keys limited to their rooms:\n%s", list)
	}
	if all := h.say(botAdmin, "!rss key list"); !strings.Contains(all, "global") || !strings.Contains(all, "elsewhere") {
		t.Fatalf("a bot admin sees every key:\n%s", all)
	}

	for _, id := range []string{"1", "3", "4"} {
		if reply := h.say(roomAdmin, "!rss key revoke "+id); !strings.Contains(reply, "no key or token") {
			t.Errorf("key %s must look unknown to the room admin, got %q", id, reply)
		}
	}
	if len(access.revoked) != 0 {
		t.Fatalf("nothing may be revoked yet: %v", access.revoked)
	}
	if reply := h.say(roomAdmin, "!rss key revoke 2"); !strings.Contains(reply, "revoked") || len(access.revoked) != 1 {
		t.Fatalf("own key: %q, revoked %v", reply, access.revoked)
	}
	if reply := h.say(botAdmin, "!rss key revoke 4"); !strings.Contains(reply, "revoked") || len(access.revoked) != 2 {
		t.Fatalf("a bot admin may revoke any key: %q", reply)
	}
}

func TestInRoomTokenCreateNamesTheRoomInTheInstruction(t *testing.T) {
	for _, sender := range []string{botAdmin, roomAdmin} {
		reply := newHarness(&fakeAccess{}, false).say(sender, "!rss token create bob")
		if !strings.Contains(reply, "private chat") || !strings.Contains(reply, "!rss token create !r:hs bob") {
			t.Fatalf("%s: the instruction must contain the room ID: %q", sender, reply)
		}
	}
	reply := newHarness(&fakeAccess{}, true).say(botAdmin, "!rss token create bob")
	if !strings.Contains(reply, "which room") {
		t.Fatalf("a private chat has no room of its own: %q", reply)
	}
}

func TestSecretsAreOnlyGivenInPrivateChats(t *testing.T) {
	for _, text := range []string{"!rss token create !other:hs bob", "!rss key create grafana rooms=!a:hs"} {
		access := &fakeAccess{}
		reply := newHarness(access, false).say(botAdmin, text)
		if strings.Contains(reply, "mxk_secret") || strings.Contains(reply, "secret-issued") || len(access.issued) != 0 || access.keyRequest.Name != "" {
			t.Fatalf("%q: nothing may be created in a shared room: %q", text, reply)
		}
		if !strings.Contains(reply, "private chat") || !strings.Contains(reply, text) {
			t.Fatalf("%q: the reply must tell what to send privately, got %q", text, reply)
		}
	}
}

func TestCreateKeyParsesOptions(t *testing.T) {
	access := &fakeAccess{}
	reply := newHarness(access, true).say(botAdmin, "!rss key create grafana rooms=!a:hs,!b:hs expires=30d")
	req := access.keyRequest
	if !strings.Contains(reply, "mxk_secret") || req.Name != "grafana" || len(req.Rooms) != 2 || req.CreatedBy != botAdmin {
		t.Fatalf("unexpected request %+v, reply %q", req, reply)
	}
	if req.ExpiresAt == nil || time.Until(*req.ExpiresAt) < 29*24*time.Hour {
		t.Fatalf("expiry not set: %v", req.ExpiresAt)
	}

	for _, bad := range []string{"!rss key create k expires=soon", "!rss key create k colour=red", "!rss key create k rooms="} {
		access := &fakeAccess{}
		reply := newHarness(access, true).say(botAdmin, bad)
		if access.keyRequest.Name != "" || strings.Contains(reply, "mxk_") {
			t.Errorf("%q must be rejected, got %q", bad, reply)
		}
	}
}

func TestListingsNeverContainSecrets(t *testing.T) {
	h := newHarness(&fakeAccess{}, false)
	if reply := h.say(botAdmin, "!rss key list"); !strings.Contains(reply, "grafana") || !strings.Contains(reply, "rooms: all") || !strings.Contains(reply, "expires: never") || strings.Contains(reply, "mxk_secret") {
		t.Fatalf("unexpected key list:\n%s", reply)
	}
	if reply := h.say(botAdmin, "!rss key revoke abc"); !strings.Contains(reply, "Usage") {
		t.Fatalf("unexpected reply %q", reply)
	}
	if reply := newHarness(&fakeAccess{}, true).say(botAdmin, "!rss token list"); !strings.Contains(reply, "which room") {
		t.Fatalf("unexpected reply %q", reply)
	}
}

func TestErrorsAreExplained(t *testing.T) {
	h := newHarness(&fakeAccess{err: domain.ErrCredentialNotFound, tokens: map[uint64]domain.FeedToken{9: {ID: 9, RoomID: "!r:hs"}}}, false)
	if reply := h.say(botAdmin, "!rss token revoke 9"); !strings.Contains(reply, "no key or token") {
		t.Fatalf("unexpected reply %q", reply)
	}
	cmds := NewCommands("!rss", []string{botAdmin}, CommandDeps{Feeds: fakeFeeds{err: domain.ErrFeedNotFound}, Access: &fakeAccess{}, Rooms: fakeRooms{}, Chats: fakeChats{}, Authority: &fakeAuthority{}})
	if reply, _ := cmds.Handle(context.Background(), "!r:hs", botAdmin, "!rss off"); !strings.Contains(reply, "no feed yet") {
		t.Fatalf("unexpected reply %q", reply)
	}
}

func TestHelp(t *testing.T) {
	h := newHarness(&fakeAccess{}, false)
	for _, text := range []string{"!rss", "!rss nope", "!rss key", "!rss token"} {
		if reply := h.say(botAdmin, text); !strings.Contains(reply, "Commands") {
			t.Errorf("%q: want help, got %q", text, reply)
		}
	}
}
