//go:build sqlite || !postgres

package httpapi

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/greatgoodtiver/matrixrss/internal/adapter/storage/gormstore"
	"github.com/greatgoodtiver/matrixrss/internal/adapter/token"
	"github.com/greatgoodtiver/matrixrss/internal/domain"
	"github.com/greatgoodtiver/matrixrss/internal/usecase"
)

const (
	rootToken = "root-token-0123456789"
	roomID    = "!room:hs"
	otherRoom = "!other:hs"
)

type server struct {
	t       *testing.T
	handler http.Handler
	// feedSecret opens the feed of the main room.
	feedSecret string
}

func minute(n int) time.Time { return time.Date(2026, 1, 1, 10, n, 0, 0, time.UTC) }

// newServer wires the real use cases and a SQLite database behind the HTTP handler.
// The main room has three messages: "hello <b>world</b>\nsecond line" (edited), "fine" and a deleted one.
func newServer(t *testing.T) *server {
	t.Helper()
	store, err := gormstore.Open("sqlite", filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	feeds := usecase.NewFeedService(store.Feeds())
	access := usecase.NewAccessService(store.APIKeys(), store.FeedTokens(), store.Feeds(), token.Generator{})
	messages := usecase.NewMessageService(store.Feeds(), store.Messages(), usecase.MessageOptions{})
	history := usecase.NewHistoryService(store.Feeds(), store.Messages(), messages, nil, log)

	ctx := t.Context()
	for _, room := range []string{roomID, otherRoom} {
		if _, err := feeds.Enable(ctx, room, "Room "+room); err != nil {
			t.Fatal(err)
		}
	}
	_, secret, err := access.EnsureDefaultFeedToken(ctx, roomID, "test")
	if err != nil {
		t.Fatal(err)
	}
	for _, msg := range []domain.Message{
		{EventID: "$1", Sender: "@alice:hs", SenderName: "Alice", Kind: domain.KindText, Body: "hello <b>world</b>\nsecond line", SentAt: minute(1)},
		{EventID: "$2", Sender: "@bob:hs", SenderName: "Bob", Kind: domain.KindText, Body: "fine", SentAt: minute(2)},
		{EventID: "$3", Sender: "@bob:hs", SenderName: "Bob", Kind: domain.KindText, Body: "oops", SentAt: minute(3)},
	} {
		msg.RoomID = roomID
		if err := messages.Record(ctx, msg); err != nil {
			t.Fatal(err)
		}
	}
	edit := domain.Edit{EventID: "$e", TargetEventID: "$1", RoomID: roomID, Sender: "@alice:hs", Body: "hello <b>world</b> (edited)\nsecond line", At: minute(5)}
	if err := messages.Edit(ctx, edit); err != nil {
		t.Fatal(err)
	}
	if err := messages.Delete(ctx, roomID, "$3", domain.Deletion{At: minute(6), By: "@mod:hs", Reason: "spam"}); err != nil {
		t.Fatal(err)
	}

	handler := New(Deps{Feeds: feeds, History: history, Messages: messages, Access: access},
		Config{RootToken: rootToken, PublicBaseURL: "https://rss.example.org/"}, log)
	return &server{t: t, handler: handler, feedSecret: secret}
}

func (s *server) do(method, path, bearer, body string) *httptest.ResponseRecorder {
	s.t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	rec := httptest.NewRecorder()
	s.handler.ServeHTTP(rec, req)
	return rec
}

func (s *server) get(path, bearer string) *httptest.ResponseRecorder {
	s.t.Helper()
	return s.do(http.MethodGet, path, bearer, "")
}

func decode[T any](t *testing.T, rec *httptest.ResponseRecorder, wantStatus int) T {
	t.Helper()
	var value T
	if rec.Code != wantStatus {
		t.Fatalf("status %d, want %d, body %s", rec.Code, wantStatus, rec.Body)
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &value); err != nil {
		t.Fatalf("decode %s: %v", rec.Body, err)
	}
	return value
}

func messagesPath(room string) string {
	return "/api/v1/rooms/" + strings.NewReplacer("!", "%21", ":", "%3A").Replace(room) + "/messages"
}

func TestAPIRequiresCredentials(t *testing.T) {
	s := newServer(t)
	for _, auth := range []string{"", "wrong", "mxk_unknown", rootToken + "x"} {
		if rec := s.get("/api/v1/rooms", auth); rec.Code != http.StatusUnauthorized {
			t.Errorf("auth %q: status %d, want 401", auth, rec.Code)
		}
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/rooms", nil)
	req.Header.Set("Authorization", "Basic "+rootToken)
	rec := httptest.NewRecorder()
	s.handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("Basic auth: status %d, want 401", rec.Code)
	}
}

func TestListRoomsAndMessages(t *testing.T) {
	s := newServer(t)

	rooms := decode[struct{ Rooms []roomDTO }](t, s.get("/api/v1/rooms", rootToken), http.StatusOK)
	if len(rooms.Rooms) != 2 {
		t.Fatalf("unexpected rooms: %+v", rooms)
	}

	page := decode[messagesDTO](t, s.get(messagesPath(roomID)+"?limit=1", rootToken), http.StatusOK)
	if len(page.Messages) != 1 || page.Messages[0].EventID != "$2" || page.NextBefore == "" {
		t.Fatalf("first page: %+v", page)
	}
	next := decode[messagesDTO](t, s.get(messagesPath(roomID)+"?before="+page.NextBefore, rootToken), http.StatusOK)
	if len(next.Messages) != 1 || next.Messages[0].EventID != "$1" {
		t.Fatalf("second page: %+v", next)
	}
	edited := next.Messages[0]
	if edited.EditedAt == nil || !strings.Contains(edited.Body, "(edited)") {
		t.Fatalf("the edit must show: %+v", edited)
	}

	only := decode[messagesDTO](t, s.get(messagesPath(roomID)+"?deleted=only", rootToken), http.StatusOK)
	if len(only.Messages) != 1 || only.Messages[0].Deleted == nil || only.Messages[0].Deleted.By != "@mod:hs" || only.Messages[0].Deleted.Reason != "spam" {
		t.Fatalf("deletion record missing: %+v", only)
	}
	all := decode[messagesDTO](t, s.get(messagesPath(roomID)+"?deleted=include", rootToken), http.StatusOK)
	if len(all.Messages) != 3 {
		t.Fatalf("want 3 messages including the deleted one, got %+v", all)
	}

	for _, bad := range []string{"limit=abc", "before=10", "deleted=maybe"} {
		if rec := s.get(messagesPath(roomID)+"?"+bad, rootToken); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400", bad, rec.Code)
		}
	}
	if rec := s.get(messagesPath("!ghost:hs"), rootToken); rec.Code != http.StatusNotFound {
		t.Errorf("unknown room: status %d, want 404", rec.Code)
	}
}

func TestMessageRevisions(t *testing.T) {
	s := newServer(t)
	path := messagesPath(roomID) + "/$1/history"

	body := decode[revisionsDTO](t, s.get(path, rootToken), http.StatusOK)
	if len(body.Revisions) != 2 || body.Revisions[0].Body != "hello <b>world</b>\nsecond line" || !strings.Contains(body.Revisions[1].Body, "(edited)") {
		t.Fatalf("unexpected revisions: %+v", body)
	}
	if rec := s.get(messagesPath(roomID)+"/$nope/history", rootToken); rec.Code != http.StatusNotFound {
		t.Errorf("unknown message: status %d, want 404", rec.Code)
	}
	if rec := s.get(path, ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("history needs credentials, got %d", rec.Code)
	}
}

func TestAPIKeyLifecycleOverHTTP(t *testing.T) {
	s := newServer(t)

	created := decode[keyDTO](t, s.do(http.MethodPost, "/api/v1/admin/keys", rootToken,
		fmt.Sprintf(`{"name":"grafana","rooms":[%q],"expires_in":"30d"}`, roomID)), http.StatusCreated)
	if !strings.HasPrefix(created.Secret, "mxk_") || created.ExpiresAt == nil || len(created.Rooms) != 1 {
		t.Fatalf("unexpected key: %+v", created)
	}

	// The key reads its room but nothing else.
	if rec := s.get(messagesPath(roomID), created.Secret); rec.Code != http.StatusOK {
		t.Fatalf("own room: status %d, body %s", rec.Code, rec.Body)
	}
	if rec := s.get(messagesPath(otherRoom), created.Secret); rec.Code != http.StatusForbidden {
		t.Errorf("other room: status %d, want 403", rec.Code)
	}
	if rec := s.get(messagesPath(roomID)+"/$1/history", created.Secret); rec.Code != http.StatusOK {
		t.Errorf("history of own room: status %d", rec.Code)
	}
	if rec := s.get(messagesPath(otherRoom)+"/$1/history", created.Secret); rec.Code != http.StatusForbidden {
		t.Errorf("history of other room: status %d, want 403", rec.Code)
	}
	rooms := decode[struct{ Rooms []roomDTO }](t, s.get("/api/v1/rooms", created.Secret), http.StatusOK)
	if len(rooms.Rooms) != 1 || rooms.Rooms[0].RoomID != roomID {
		t.Fatalf("the listing must be limited to the key's rooms: %+v", rooms)
	}

	// Keys cannot manage credentials.
	for _, call := range [][2]string{{http.MethodGet, "/api/v1/admin/keys"}, {http.MethodPost, "/api/v1/admin/keys"}, {http.MethodDelete, "/api/v1/admin/keys/1"}} {
		if rec := s.do(call[0], call[1], created.Secret, `{"name":"x"}`); rec.Code != http.StatusForbidden {
			t.Errorf("%s %s with a key: status %d, want 403", call[0], call[1], rec.Code)
		}
	}

	// The listing never contains secrets.
	list := s.get("/api/v1/admin/keys", rootToken)
	if strings.Contains(list.Body.String(), created.Secret) || strings.Contains(list.Body.String(), `"secret"`) {
		t.Fatalf("the listing must not expose secrets: %s", list.Body)
	}
	keys := decode[struct{ Keys []keyDTO }](t, list, http.StatusOK)
	if len(keys.Keys) != 1 || keys.Keys[0].Name != "grafana" || keys.Keys[0].Hint == "" {
		t.Fatalf("unexpected keys: %+v", keys)
	}

	path := fmt.Sprintf("/api/v1/admin/keys/%d", created.ID)
	if rec := s.do(http.MethodDelete, path, rootToken, ""); rec.Code != http.StatusNoContent {
		t.Fatalf("revoke: status %d", rec.Code)
	}
	if rec := s.get(messagesPath(roomID), created.Secret); rec.Code != http.StatusUnauthorized {
		t.Errorf("a revoked key: status %d, want 401", rec.Code)
	}
	if rec := s.do(http.MethodDelete, "/api/v1/admin/keys/999", rootToken, ""); rec.Code != http.StatusNotFound {
		t.Errorf("unknown key: status %d, want 404", rec.Code)
	}
	if rec := s.do(http.MethodDelete, "/api/v1/admin/keys/abc", rootToken, ""); rec.Code != http.StatusBadRequest {
		t.Errorf("bad id: status %d, want 400", rec.Code)
	}
}

func TestCreateKeyValidation(t *testing.T) {
	s := newServer(t)
	for name, body := range map[string]string{
		"not json":      `nope`,
		"unknown field": `{"name":"k","admin":true}`,
		"empty name":    `{"name":""}`,
		"unknown room":  `{"name":"k","rooms":["!ghost:hs"]}`,
		"bad lifetime":  `{"name":"k","expires_in":"soon"}`,
	} {
		if rec := s.do(http.MethodPost, "/api/v1/admin/keys", rootToken, body); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400 (body %s)", name, rec.Code, rec.Body)
		}
	}
}

func TestFeedTokensOverHTTP(t *testing.T) {
	s := newServer(t)
	tokensPath := "/api/v1/admin/rooms/%21room%3Ahs/tokens"

	if rec := s.get("/feeds/"+s.feedSecret, ""); rec.Code != http.StatusOK {
		t.Fatalf("default token: status %d", rec.Code)
	}

	created := decode[tokenDTO](t, s.do(http.MethodPost, tokensPath, rootToken, `{"name":"bob"}`), http.StatusCreated)
	if created.Secret == "" || created.FeedURL != "https://rss.example.org/feeds/"+created.Secret {
		t.Fatalf("unexpected token: %+v", created)
	}
	if rec := s.get("/feeds/"+created.Secret, ""); rec.Code != http.StatusOK {
		t.Fatalf("new token: status %d", rec.Code)
	}

	list := s.get(tokensPath, rootToken)
	if strings.Contains(list.Body.String(), created.Secret) || strings.Contains(list.Body.String(), s.feedSecret) {
		t.Fatalf("the listing must not expose secrets: %s", list.Body)
	}
	tokens := decode[struct{ Tokens []tokenDTO }](t, list, http.StatusOK)
	if len(tokens.Tokens) != 2 {
		t.Fatalf("want 2 tokens, got %+v", tokens)
	}

	// Revoking one token leaves the other one working.
	if rec := s.do(http.MethodDelete, fmt.Sprintf("/api/v1/admin/tokens/%d", created.ID), rootToken, ""); rec.Code != http.StatusNoContent {
		t.Fatalf("revoke: status %d", rec.Code)
	}
	if rec := s.get("/feeds/"+created.Secret, ""); rec.Code != http.StatusNotFound {
		t.Errorf("revoked token: status %d, want 404", rec.Code)
	}
	if rec := s.get("/feeds/"+s.feedSecret, ""); rec.Code != http.StatusOK {
		t.Errorf("other token: status %d, want 200", rec.Code)
	}

	if rec := s.do(http.MethodPost, "/api/v1/admin/rooms/%21ghost%3Ahs/tokens", rootToken, `{"name":"x"}`); rec.Code != http.StatusNotFound {
		t.Errorf("room without feed: status %d, want 404", rec.Code)
	}
	if rec := s.do(http.MethodPost, tokensPath, rootToken, `{"name":""}`); rec.Code != http.StatusBadRequest {
		t.Errorf("empty name: status %d, want 400", rec.Code)
	}
}

func TestFeedFormats(t *testing.T) {
	s := newServer(t)
	path := "/feeds/" + s.feedSecret

	rec := s.get(path, "")
	if rec.Code != http.StatusOK || !strings.HasPrefix(rec.Header().Get("Content-Type"), "application/rss+xml") {
		t.Fatalf("rss: status %d, type %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	body := rec.Body.String()
	if !strings.Contains(body, "<title>Bob: fine</title>") || !strings.Contains(body, "<title>Alice: hello &lt;b&gt;world&lt;/b&gt; (edited)</title>") {
		t.Fatalf("titles must be the escaped first line of the current text:\n%s", body)
	}
	if strings.Contains(body, "oops") || strings.Contains(body, "<b>") {
		t.Fatalf("deleted messages and raw HTML must not appear:\n%s", body)
	}

	if rec := s.get(path+"?format=atom", ""); !strings.HasPrefix(rec.Header().Get("Content-Type"), "application/atom+xml") {
		t.Errorf("atom content type: %q", rec.Header().Get("Content-Type"))
	}
	if rec := s.get(path+"?format=json", ""); !strings.HasPrefix(rec.Header().Get("Content-Type"), "application/feed+json") {
		t.Errorf("json content type: %q", rec.Header().Get("Content-Type"))
	}
	if rec := s.get("/feeds/unknown", ""); rec.Code != http.StatusNotFound {
		t.Errorf("unknown token: status %d, want 404", rec.Code)
	}
	if rec := s.get(path, rootToken); rec.Code != http.StatusOK {
		t.Errorf("a stray Authorization header must not matter: %d", rec.Code)
	}
}

func TestFeedSupportsConditionalRequests(t *testing.T) {
	s := newServer(t)
	first := s.get("/feeds/"+s.feedSecret, "")
	etag := first.Header().Get("ETag")
	if etag == "" || first.Header().Get("Cache-Control") == "" {
		t.Fatalf("missing cache headers: %v", first.Header())
	}

	req := httptest.NewRequest(http.MethodGet, "/feeds/"+s.feedSecret, nil)
	req.Header.Set("If-None-Match", etag)
	rec := httptest.NewRecorder()
	s.handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotModified || rec.Body.Len() != 0 {
		t.Fatalf("want empty 304, got %d with %d bytes", rec.Code, rec.Body.Len())
	}
}

func TestHealth(t *testing.T) {
	if rec := newServer(t).get("/healthz", ""); rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
}
