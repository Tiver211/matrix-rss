//go:build sqlite || !postgres

package usecase_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/greatgoodtiver/matrixrss/internal/domain"
)

func TestAPIKeyLifecycle(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t, usecaseOptions)
	e.enable(t)

	key, secret, err := e.access.CreateAPIKey(ctx, domain.KeyRequest{Name: "grafana", Rooms: []string{room}, CreatedBy: "@a:hs"})
	if err != nil || !strings.HasPrefix(secret, "mxk_") || key.ID == 0 || !strings.HasPrefix(secret, key.Hint) {
		t.Fatalf("create: %+v %q %v", key, secret, err)
	}

	got, err := e.access.AuthenticateKey(ctx, secret)
	if err != nil || got.ID != key.ID || !got.AllowsRoom(room) || got.AllowsRoom("!other:hs") {
		t.Fatalf("authenticate: %+v, %v", got, err)
	}
	for _, bad := range []string{"", "mxk_wrong", "s1", strings.ToUpper(secret)} {
		if _, err := e.access.AuthenticateKey(ctx, bad); !errors.Is(err, domain.ErrInvalidCredentials) {
			t.Errorf("%q: want ErrInvalidCredentials, got %v", bad, err)
		}
	}

	if err := e.access.RevokeAPIKey(ctx, key.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.access.AuthenticateKey(ctx, secret); !errors.Is(err, domain.ErrInvalidCredentials) {
		t.Fatalf("a revoked key must stop working, got %v", err)
	}
	if err := e.access.RevokeAPIKey(ctx, 999); !errors.Is(err, domain.ErrCredentialNotFound) {
		t.Fatalf("want ErrCredentialNotFound, got %v", err)
	}
	if keys, _ := e.access.ListAPIKeys(ctx); len(keys) != 1 || keys[0].RevokedAt == nil {
		t.Fatalf("revoked keys stay listed for audit: %+v", keys)
	}
}

func TestAPIKeyExpiresAndIsValidated(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t, usecaseOptions)
	e.enable(t)

	soon := time.Now().Add(80 * time.Millisecond)
	_, secret, err := e.access.CreateAPIKey(ctx, domain.KeyRequest{Name: "short", ExpiresAt: &soon})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.access.AuthenticateKey(ctx, secret); err != nil {
		t.Fatalf("a fresh key must work: %v", err)
	}
	time.Sleep(120 * time.Millisecond)
	if _, err := e.access.AuthenticateKey(ctx, secret); !errors.Is(err, domain.ErrInvalidCredentials) {
		t.Fatalf("an expired key must stop working, got %v", err)
	}

	past := time.Now().Add(-time.Hour)
	for name, req := range map[string]domain.KeyRequest{
		"empty name":    {Name: "  "},
		"long name":     {Name: strings.Repeat("x", 65)},
		"unknown room":  {Name: "k", Rooms: []string{"!ghost:hs"}},
		"expired start": {Name: "k", ExpiresAt: &past},
	} {
		if _, _, err := e.access.CreateAPIKey(ctx, req); !errors.Is(err, domain.ErrInvalid) {
			t.Errorf("%s: want ErrInvalid, got %v", name, err)
		}
	}
}

func TestFeedTokensAreIndependentlyRevocable(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t, usecaseOptions)
	e.enable(t)

	first, firstSecret, err := e.access.EnsureDefaultFeedToken(ctx, room, "@a:hs")
	if err != nil || firstSecret == "" || first.Name != "default" {
		t.Fatalf("first token: %+v %q %v", first, firstSecret, err)
	}
	if _, again, err := e.access.EnsureDefaultFeedToken(ctx, room, "@a:hs"); err != nil || again != "" {
		t.Fatalf("an existing token must not be reissued: %q, %v", again, err)
	}
	bob, bobSecret, err := e.access.IssueFeedToken(ctx, room, "bob", "@a:hs")
	if err != nil || bobSecret == firstSecret {
		t.Fatalf("second token: %+v %q %v", bob, bobSecret, err)
	}

	for _, secret := range []string{firstSecret, bobSecret} {
		if feed, err := e.access.FeedByToken(ctx, secret); err != nil || feed.RoomID != room {
			t.Fatalf("token must open the feed: %+v, %v", feed, err)
		}
	}

	if err := e.access.RevokeFeedToken(ctx, bob.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.access.FeedByToken(ctx, bobSecret); !errors.Is(err, domain.ErrFeedNotFound) {
		t.Fatalf("a revoked token must stop working, got %v", err)
	}
	if _, err := e.access.FeedByToken(ctx, firstSecret); err != nil {
		t.Fatalf("the other token must keep working: %v", err)
	}

	_ = e.feeds.Disable(ctx, room)
	if _, err := e.access.FeedByToken(ctx, firstSecret); !errors.Is(err, domain.ErrFeedNotFound) {
		t.Fatalf("a disabled feed must look missing, got %v", err)
	}
	if _, err := e.access.FeedByToken(ctx, "unknown"); !errors.Is(err, domain.ErrFeedNotFound) {
		t.Fatalf("unknown tokens must look missing, got %v", err)
	}
	if _, _, err := e.access.IssueFeedToken(ctx, "!none:hs", "x", "@a:hs"); !errors.Is(err, domain.ErrFeedNotFound) {
		t.Fatalf("a room without a feed has no tokens, got %v", err)
	}
}

func TestRotateRevokesEveryTokenOfTheRoom(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t, usecaseOptions)
	e.enable(t)
	_, oldSecret, _ := e.access.EnsureDefaultFeedToken(ctx, room, "@a:hs")
	_, otherSecret, _ := e.access.IssueFeedToken(ctx, room, "bob", "@a:hs")

	_, newSecret, err := e.access.RotateFeedTokens(ctx, room, "@a:hs")
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{oldSecret, otherSecret} {
		if _, err := e.access.FeedByToken(ctx, secret); !errors.Is(err, domain.ErrFeedNotFound) {
			t.Fatalf("rotate must revoke %q, got %v", secret, err)
		}
	}
	if _, err := e.access.FeedByToken(ctx, newSecret); err != nil {
		t.Fatalf("the new token must work: %v", err)
	}

	// After a rotation a later "on" must not add yet another token.
	if _, again, _ := e.access.EnsureDefaultFeedToken(ctx, room, "@a:hs"); again != "" {
		t.Fatalf("an active token exists, nothing to issue: %q", again)
	}
}
