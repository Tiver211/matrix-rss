//go:build sqlite || !postgres

package gormstore

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/greatgoodtiver/matrixrss/internal/domain"
)

// openTestStore uses a temporary SQLite file, or the PostgreSQL server named by TEST_POSTGRES_DSN
// (its tables are emptied first) to run the same tests against both databases.
func testTarget(t *testing.T) (driverName, dsn string) {
	t.Helper()
	if pg := os.Getenv("TEST_POSTGRES_DSN"); pg != "" {
		return "postgres", pg
	}
	return "sqlite", filepath.Join(t.TempDir(), "test.db")
}

func openTestStore(t *testing.T) *Store {
	t.Helper()
	driverName, dsn := testTarget(t)
	store, err := Open(driverName, dsn)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	for _, table := range []string{"message_revisions", "messages", "feed_tokens", "api_keys", "feeds"} {
		if err := store.db.Exec("DELETE FROM " + table).Error; err != nil {
			t.Fatalf("clean %s: %v", table, err)
		}
	}
	return store
}

func TestOpenRejectsUnknownDriver(t *testing.T) {
	if _, err := Open("oracle", "dsn"); err == nil {
		t.Fatal("expected error for unknown driver")
	}
}

func TestFeedRepository(t *testing.T) {
	ctx := context.Background()
	repo := openTestStore(t).Feeds()

	if _, err := repo.FindByRoom(ctx, "!missing:hs"); !errors.Is(err, domain.ErrFeedNotFound) {
		t.Fatalf("want ErrFeedNotFound, got %v", err)
	}

	feed := domain.Feed{RoomID: "!a:hs", Title: "A", Enabled: true, CreatedAt: time.Now()}
	if err := repo.Save(ctx, feed); err != nil {
		t.Fatalf("save: %v", err)
	}
	feed.Enabled = false
	feed.Title = "A2"
	if err := repo.Save(ctx, feed); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	got, err := repo.FindByRoom(ctx, "!a:hs")
	if err != nil || got.Title != "A2" || got.Enabled {
		t.Fatalf("upsert did not update the feed: %+v, %v", got, err)
	}
	if enabled, err := repo.ListEnabled(ctx); err != nil || len(enabled) != 0 {
		t.Fatalf("want no enabled feeds, got %v, %v", enabled, err)
	}
}

func TestAPIKeyRepository(t *testing.T) {
	ctx := context.Background()
	repo := openTestStore(t).APIKeys()
	expires := time.Now().Add(time.Hour).UTC()

	key := domain.APIKey{Name: "grafana", Hint: "mxk_abcd", Rooms: []string{"!a:hs", "!b:hs"}, ExpiresAt: &expires, CreatedAt: time.Now(), CreatedBy: "@admin:hs"}
	if err := repo.Create(ctx, &key, "hash1"); err != nil || key.ID == 0 {
		t.Fatalf("create: id=%d, %v", key.ID, err)
	}
	open := domain.APIKey{Name: "all", Hint: "mxk_wxyz", CreatedAt: time.Now(), CreatedBy: "cli"}
	if err := repo.Create(ctx, &open, "hash2"); err != nil {
		t.Fatal(err)
	}

	found, err := repo.FindByHash(ctx, "hash1")
	if err != nil || found.Name != "grafana" || len(found.Rooms) != 2 || found.ExpiresAt == nil {
		t.Fatalf("find: %+v, %v", found, err)
	}
	if all, _ := repo.FindByHash(ctx, "hash2"); len(all.Rooms) != 0 {
		t.Fatalf("a key without rooms must stay unrestricted: %+v", all)
	}
	if _, err := repo.FindByHash(ctx, "nope"); !errors.Is(err, domain.ErrCredentialNotFound) {
		t.Fatalf("want ErrCredentialNotFound, got %v", err)
	}
	if byID, err := repo.Get(ctx, key.ID); err != nil || byID.Name != "grafana" {
		t.Fatalf("get: %+v, %v", byID, err)
	}
	if _, err := repo.Get(ctx, 9999); !errors.Is(err, domain.ErrCredentialNotFound) {
		t.Fatalf("want ErrCredentialNotFound, got %v", err)
	}

	first, second := time.Now().Add(-time.Minute).UTC(), time.Now().UTC()
	if err := repo.Revoke(ctx, key.ID, first); err != nil {
		t.Fatal(err)
	}
	if err := repo.Revoke(ctx, key.ID, second); err != nil {
		t.Fatalf("revoking twice must be fine: %v", err)
	}
	revoked, _ := repo.FindByHash(ctx, "hash1")
	if revoked.RevokedAt == nil || revoked.RevokedAt.Sub(first).Abs() > time.Second {
		t.Fatalf("the first revocation time must be kept: %+v", revoked.RevokedAt)
	}
	if err := repo.Revoke(ctx, 9999, second); !errors.Is(err, domain.ErrCredentialNotFound) {
		t.Fatalf("want ErrCredentialNotFound, got %v", err)
	}
	if keys, err := repo.List(ctx); err != nil || len(keys) != 2 {
		t.Fatalf("list: %v, %v", keys, err)
	}
}

func TestFeedTokenRepository(t *testing.T) {
	ctx := context.Background()
	repo := openTestStore(t).FeedTokens()
	now := time.Now().UTC()

	var ids []uint64
	for i, hash := range []string{"h1", "h2"} {
		token := domain.FeedToken{RoomID: "!a:hs", Name: "t" + hash, Hint: "x", CreatedAt: now, CreatedBy: "cli"}
		if err := repo.Create(ctx, &token, hash); err != nil {
			t.Fatalf("create %d: %v", i, err)
		}
		ids = append(ids, token.ID)
	}
	other := domain.FeedToken{RoomID: "!b:hs", Name: "other", CreatedAt: now, CreatedBy: "cli"}
	_ = repo.Create(ctx, &other, "h3")

	if byID, err := repo.Get(ctx, ids[1]); err != nil || byID.RoomID != "!a:hs" {
		t.Fatalf("get: %+v, %v", byID, err)
	}
	if _, err := repo.Get(ctx, 9999); !errors.Is(err, domain.ErrCredentialNotFound) {
		t.Fatalf("want ErrCredentialNotFound, got %v", err)
	}
	if err := repo.Revoke(ctx, ids[0], now); err != nil {
		t.Fatal(err)
	}
	tokens, err := repo.ListByRoom(ctx, "!a:hs")
	if err != nil || len(tokens) != 2 || tokens[0].Active() || !tokens[1].Active() {
		t.Fatalf("unexpected tokens: %+v, %v", tokens, err)
	}

	if err := repo.RevokeAllForRoom(ctx, "!a:hs", now); err != nil {
		t.Fatal(err)
	}
	if found, _ := repo.FindByHash(ctx, "h2"); found.Active() {
		t.Fatal("RevokeAllForRoom must revoke every token of the room")
	}
	if found, _ := repo.FindByHash(ctx, "h3"); !found.Active() {
		t.Fatal("tokens of other rooms must stay active")
	}
	if err := repo.Revoke(ctx, 9999, now); !errors.Is(err, domain.ErrCredentialNotFound) {
		t.Fatalf("want ErrCredentialNotFound, got %v", err)
	}
}

func TestLegacyFeedTokensAreMigrated(t *testing.T) {
	driverName, dsn := testTarget(t)
	drv, err := lookup(driverName)
	if err != nil {
		t.Fatal(err)
	}
	dialector, err := drv.dialector(dsn)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := gorm.Open(dialector, &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"message_revisions", "messages", "feed_tokens", "api_keys", "feeds"} {
		raw.Exec("DROP TABLE IF EXISTS " + table)
	}
	// The schema of the version in which a feed had a single plain token.
	for _, statement := range []string{
		"CREATE TABLE feeds (room_id VARCHAR(255) PRIMARY KEY, title TEXT NOT NULL, token VARCHAR(64) NOT NULL, enabled BOOLEAN NOT NULL, created_at TIMESTAMP)",
		"CREATE UNIQUE INDEX idx_feeds_token ON feeds(token)",
		"INSERT INTO feeds (room_id, title, token, enabled) VALUES ('!old:hs', 'Old', 'oldsecret', true)",
	} {
		if err := raw.Exec(statement).Error; err != nil {
			t.Fatalf("%s: %v", statement, err)
		}
	}
	rawSQL, _ := raw.DB()
	_ = rawSQL.Close()

	store, err := Open(driverName, dsn)
	if err != nil {
		t.Fatalf("open must migrate the old schema: %v", err)
	}
	defer store.Close()

	token, err := store.FeedTokens().FindByHash(context.Background(), domain.HashSecret("oldsecret"))
	if err != nil || token.RoomID != "!old:hs" || !token.Active() {
		t.Fatalf("the old feed URL must keep working: %+v, %v", token, err)
	}
	if store.db.Migrator().HasColumn("feeds", "token") {
		t.Fatal("the legacy column must be dropped")
	}
	if feed, err := store.Feeds().FindByRoom(context.Background(), "!old:hs"); err != nil || feed.Title != "Old" {
		t.Fatalf("feed lost: %+v, %v", feed, err)
	}
	// A second start finds nothing to migrate.
	if err := migrate(store.db); err != nil {
		t.Fatalf("migrate twice: %v", err)
	}
}

func at(minutes int) time.Time {
	return time.Date(2026, 1, 1, 10, minutes, 0, 0, time.UTC)
}

func TestMessageRepositoryOrdersChronologicallyAndPages(t *testing.T) {
	ctx := context.Background()
	repo := openTestStore(t).Messages()

	// Inserted out of order, as backfilled history is.
	for _, m := range []struct {
		id string
		at int
	}{{"$2", 2}, {"$3", 3}, {"$1", 1}} {
		if err := repo.Add(ctx, domain.Message{EventID: m.id, RoomID: "!a:hs", Body: m.id, SentAt: at(m.at)}); err != nil {
			t.Fatalf("add %s: %v", m.id, err)
		}
	}
	if err := repo.Add(ctx, domain.Message{EventID: "$2", RoomID: "!a:hs", Body: "dup", SentAt: at(2)}); err != nil {
		t.Fatalf("duplicate add must be ignored: %v", err)
	}

	page, err := repo.List(ctx, "!a:hs", domain.MessageQuery{Limit: 2})
	if err != nil || len(page) != 2 || page[0].EventID != "$3" || page[1].EventID != "$2" || page[1].Body != "$2" {
		t.Fatalf("unexpected first page: %+v, %v", page, err)
	}
	older, err := repo.List(ctx, "!a:hs", domain.MessageQuery{Limit: 10, Before: domain.NewCursor(page[1])})
	if err != nil || len(older) != 1 || older[0].EventID != "$1" {
		t.Fatalf("unexpected second page: %+v, %v", older, err)
	}

	oldest, err := repo.Oldest(ctx, "!a:hs")
	if err != nil || oldest.EventID != "$1" {
		t.Fatalf("oldest = %+v, %v", oldest, err)
	}
	if _, err := repo.Oldest(ctx, "!none:hs"); !errors.Is(err, domain.ErrMessageNotFound) {
		t.Fatalf("want ErrMessageNotFound, got %v", err)
	}
}

func TestMessageRepositoryEdits(t *testing.T) {
	ctx := context.Background()
	repo := openTestStore(t).Messages()
	_ = repo.Add(ctx, domain.Message{EventID: "$m", RoomID: "!a:hs", Sender: "@u:hs", Body: "v1", SentAt: at(0)})

	edit := func(id, body string, minute int) domain.Edit {
		return domain.Edit{EventID: id, TargetEventID: "$m", RoomID: "!a:hs", Sender: "@u:hs", Body: body, At: at(minute)}
	}
	// The newer edit arrives first; the older one must not overwrite it.
	for _, e := range []domain.Edit{edit("$e2", "v3", 2), edit("$e1", "v2", 1), edit("$e1", "v2", 1)} {
		if err := repo.SaveEdit(ctx, e); err != nil {
			t.Fatalf("save edit: %v", err)
		}
	}

	msg, _ := repo.Get(ctx, "!a:hs", "$m")
	if msg.Body != "v3" || msg.EditedAt == nil || !msg.EditedAt.Equal(at(2)) {
		t.Fatalf("current version wrong: %+v", msg)
	}
	revisions, err := repo.Revisions(ctx, "!a:hs", "$m")
	if err != nil || len(revisions) != 3 || revisions[0].Body != "v1" || revisions[1].Body != "v2" || revisions[2].Body != "v3" {
		t.Fatalf("unexpected revisions: %+v, %v", revisions, err)
	}

	err = repo.SaveEdit(ctx, domain.Edit{EventID: "$x", TargetEventID: "$unknown", RoomID: "!a:hs", At: at(5)})
	if !errors.Is(err, domain.ErrMessageNotFound) {
		t.Fatalf("want ErrMessageNotFound, got %v", err)
	}
}

func TestMessageRepositoryDeletions(t *testing.T) {
	ctx := context.Background()
	repo := openTestStore(t).Messages()
	for _, id := range []string{"$keep", "$wipe", "$live"} {
		_ = repo.Add(ctx, domain.Message{EventID: id, RoomID: "!a:hs", Body: "text of " + id, MediaURL: "http://m", SentAt: at(1)})
	}
	_ = repo.SaveEdit(ctx, domain.Edit{EventID: "$e", TargetEventID: "$wipe", RoomID: "!a:hs", Body: "edited", At: at(2)})
	deletion := domain.Deletion{At: at(9), By: "@mod:hs", Reason: "spam"}

	if err := repo.MarkDeleted(ctx, "!a:hs", "$keep", deletion, false); err != nil {
		t.Fatal(err)
	}
	if err := repo.MarkDeleted(ctx, "!a:hs", "$wipe", deletion, true); err != nil {
		t.Fatal(err)
	}
	if err := repo.MarkDeleted(ctx, "!a:hs", "$unknown", deletion, true); err != nil {
		t.Fatalf("unknown message must be a no-op: %v", err)
	}

	kept, _ := repo.Get(ctx, "!a:hs", "$keep")
	if kept.Deletion == nil || kept.Deletion.By != "@mod:hs" || kept.Body != "text of $keep" {
		t.Fatalf("content must survive: %+v", kept)
	}
	wiped, _ := repo.Get(ctx, "!a:hs", "$wipe")
	if wiped.Deletion == nil || wiped.Body != "" || wiped.MediaURL != "" {
		t.Fatalf("content must be erased: %+v", wiped)
	}
	if revisions, _ := repo.Revisions(ctx, "!a:hs", "$wipe"); len(revisions) != 0 {
		t.Fatalf("revisions must be erased: %+v", revisions)
	}

	live, _ := repo.List(ctx, "!a:hs", domain.MessageQuery{Limit: 10})
	only, _ := repo.List(ctx, "!a:hs", domain.MessageQuery{Limit: 10, Deleted: domain.OnlyDeleted})
	all, _ := repo.List(ctx, "!a:hs", domain.MessageQuery{Limit: 10, Deleted: domain.IncludeDeleted})
	if len(live) != 1 || len(only) != 2 || len(all) != 3 {
		t.Fatalf("filters wrong: live=%d only=%d all=%d", len(live), len(only), len(all))
	}
}

func TestMessageRepositoryRetention(t *testing.T) {
	ctx := context.Background()
	repo := openTestStore(t).Messages()
	_ = repo.Add(ctx, domain.Message{EventID: "$old", RoomID: "!a:hs", Body: "old", SentAt: at(1)})
	_ = repo.Add(ctx, domain.Message{EventID: "$new", RoomID: "!a:hs", Body: "new", SentAt: at(30)})
	_ = repo.SaveEdit(ctx, domain.Edit{EventID: "$e", TargetEventID: "$old", RoomID: "!a:hs", Body: "x", At: at(2)})

	removed, err := repo.DeleteOlderThan(ctx, at(10))
	if err != nil || removed != 1 {
		t.Fatalf("want 1 removed, got %d, %v", removed, err)
	}
	if revisions, _ := repo.Revisions(ctx, "!a:hs", "$old"); len(revisions) != 0 {
		t.Fatalf("revisions of expired message must go: %+v", revisions)
	}
	if _, err := repo.Get(ctx, "!a:hs", "$new"); err != nil {
		t.Fatalf("recent message must stay: %v", err)
	}
}
