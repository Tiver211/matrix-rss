package usecase

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/greatgoodtiver/matrixrss/internal/domain"
)

const (
	apiKeyPrefix = "mxk_"
	defaultToken = "default"
	maxNameRunes = 64
	hintLength   = 8
	maxKeyRooms  = 100
)

// AccessService issues, checks and revokes the credentials: API keys for the HTTP API and the
// tokens in feed URLs. Secrets are returned once, on creation; only their hashes are stored.
type AccessService struct {
	keys    APIKeyRepository
	tokens  FeedTokenRepository
	feeds   FeedRepository
	secrets TokenGenerator
	now     func() time.Time
}

func NewAccessService(keys APIKeyRepository, tokens FeedTokenRepository, feeds FeedRepository, secrets TokenGenerator) *AccessService {
	return &AccessService{keys: keys, tokens: tokens, feeds: feeds, secrets: secrets, now: time.Now}
}

// CreateAPIKey issues a read-only key, optionally limited to rooms and to a validity period.
// It returns the secret, which is not retrievable later.
func (s *AccessService) CreateAPIKey(ctx context.Context, req domain.KeyRequest) (domain.APIKey, string, error) {
	if err := validateName(req.Name); err != nil {
		return domain.APIKey{}, "", err
	}
	if len(req.Rooms) > maxKeyRooms {
		return domain.APIKey{}, "", fmt.Errorf("%w: at most %d rooms per key", domain.ErrInvalid, maxKeyRooms)
	}
	for _, room := range req.Rooms {
		if _, err := s.feeds.FindByRoom(ctx, room); errors.Is(err, domain.ErrFeedNotFound) {
			return domain.APIKey{}, "", fmt.Errorf("%w: room %s has no feed", domain.ErrInvalid, room)
		} else if err != nil {
			return domain.APIKey{}, "", err
		}
	}
	now := s.now()
	if req.ExpiresAt != nil && !req.ExpiresAt.After(now) {
		return domain.APIKey{}, "", fmt.Errorf("%w: expiry is in the past", domain.ErrInvalid)
	}

	secret, err := s.newSecret(apiKeyPrefix)
	if err != nil {
		return domain.APIKey{}, "", err
	}
	key := domain.APIKey{
		Name: req.Name, Hint: hint(secret, apiKeyPrefix), Rooms: req.Rooms, ExpiresAt: req.ExpiresAt,
		CreatedAt: now, CreatedBy: req.CreatedBy,
	}
	if err := s.keys.Create(ctx, &key, domain.HashSecret(secret)); err != nil {
		return domain.APIKey{}, "", err
	}
	return key, secret, nil
}

func (s *AccessService) ListAPIKeys(ctx context.Context) ([]domain.APIKey, error) {
	return s.keys.List(ctx)
}

// FindAPIKey returns a key by ID, e.g. to check whose it is before revoking it.
func (s *AccessService) FindAPIKey(ctx context.Context, id uint64) (domain.APIKey, error) {
	return s.keys.Get(ctx, id)
}

func (s *AccessService) RevokeAPIKey(ctx context.Context, id uint64) error {
	return s.keys.Revoke(ctx, id, s.now())
}

// AuthenticateKey returns the key behind the secret, or domain.ErrInvalidCredentials when the
// secret is unknown, revoked or expired.
func (s *AccessService) AuthenticateKey(ctx context.Context, secret string) (domain.APIKey, error) {
	if !strings.HasPrefix(secret, apiKeyPrefix) {
		return domain.APIKey{}, domain.ErrInvalidCredentials
	}
	key, err := s.keys.FindByHash(ctx, domain.HashSecret(secret))
	if errors.Is(err, domain.ErrCredentialNotFound) {
		return domain.APIKey{}, domain.ErrInvalidCredentials
	}
	if err != nil {
		return domain.APIKey{}, err
	}
	if !key.Active(s.now()) {
		return domain.APIKey{}, domain.ErrInvalidCredentials
	}
	return key, nil
}

// IssueFeedToken creates another token for the feed of the room, e.g. for one more reader.
func (s *AccessService) IssueFeedToken(ctx context.Context, roomID, name, createdBy string) (domain.FeedToken, string, error) {
	if err := validateName(name); err != nil {
		return domain.FeedToken{}, "", err
	}
	if _, err := s.feeds.FindByRoom(ctx, roomID); err != nil {
		return domain.FeedToken{}, "", err
	}
	secret, err := s.newSecret("")
	if err != nil {
		return domain.FeedToken{}, "", err
	}
	token := domain.FeedToken{RoomID: roomID, Name: name, Hint: hint(secret, ""), CreatedAt: s.now(), CreatedBy: createdBy}
	if err := s.tokens.Create(ctx, &token, domain.HashSecret(secret)); err != nil {
		return domain.FeedToken{}, "", err
	}
	return token, secret, nil
}

// EnsureDefaultFeedToken issues the first token of a feed. If the feed already has an active
// token nothing is created and the returned secret is empty.
func (s *AccessService) EnsureDefaultFeedToken(ctx context.Context, roomID, createdBy string) (domain.FeedToken, string, error) {
	tokens, err := s.tokens.ListByRoom(ctx, roomID)
	if err != nil {
		return domain.FeedToken{}, "", err
	}
	for _, token := range tokens {
		if token.Active() {
			return token, "", nil
		}
	}
	return s.IssueFeedToken(ctx, roomID, defaultToken, createdBy)
}

// RotateFeedTokens revokes every token of the feed and issues a new default one.
func (s *AccessService) RotateFeedTokens(ctx context.Context, roomID, createdBy string) (domain.FeedToken, string, error) {
	if _, err := s.feeds.FindByRoom(ctx, roomID); err != nil {
		return domain.FeedToken{}, "", err
	}
	if err := s.tokens.RevokeAllForRoom(ctx, roomID, s.now()); err != nil {
		return domain.FeedToken{}, "", err
	}
	return s.IssueFeedToken(ctx, roomID, defaultToken, createdBy)
}

func (s *AccessService) ListFeedTokens(ctx context.Context, roomID string) ([]domain.FeedToken, error) {
	return s.tokens.ListByRoom(ctx, roomID)
}

// FindFeedToken returns a token by ID, e.g. to check which room it belongs to before revoking it.
func (s *AccessService) FindFeedToken(ctx context.Context, id uint64) (domain.FeedToken, error) {
	return s.tokens.Get(ctx, id)
}

func (s *AccessService) RevokeFeedToken(ctx context.Context, id uint64) error {
	return s.tokens.Revoke(ctx, id, s.now())
}

// FeedByToken returns the published feed a token opens. Unknown, revoked and disabled all look alike.
func (s *AccessService) FeedByToken(ctx context.Context, secret string) (domain.Feed, error) {
	token, err := s.tokens.FindByHash(ctx, domain.HashSecret(secret))
	if errors.Is(err, domain.ErrCredentialNotFound) {
		return domain.Feed{}, domain.ErrFeedNotFound
	}
	if err != nil {
		return domain.Feed{}, err
	}
	if !token.Active() {
		return domain.Feed{}, domain.ErrFeedNotFound
	}
	feed, err := s.feeds.FindByRoom(ctx, token.RoomID)
	if err != nil {
		return domain.Feed{}, err
	}
	if !feed.Enabled {
		return domain.Feed{}, domain.ErrFeedNotFound
	}
	return feed, nil
}

func (s *AccessService) newSecret(prefix string) (string, error) {
	secret, err := s.secrets.New()
	if err != nil {
		return "", fmt.Errorf("generate secret: %w", err)
	}
	return prefix + secret, nil
}

// hint returns the recognisable beginning of a secret: its prefix and a few characters.
func hint(secret, prefix string) string {
	return secret[:min(len(secret), len(prefix)+hintLength)]
}

func validateName(name string) error {
	switch {
	case strings.TrimSpace(name) == "":
		return fmt.Errorf("%w: name must not be empty", domain.ErrInvalid)
	case utf8.RuneCountInString(name) > maxNameRunes:
		return fmt.Errorf("%w: name must be at most %d characters", domain.ErrInvalid, maxNameRunes)
	}
	return nil
}
