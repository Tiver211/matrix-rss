// Package httpapi exposes the REST API and the public RSS feeds.
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/greatgoodtiver/matrixrss/internal/domain"
)

// FeedLister lists the published rooms.
type FeedLister interface {
	ListEnabled(ctx context.Context) ([]domain.Feed, error)
}

// HistoryReader returns a page of room history, fetching what is missing from Matrix.
type HistoryReader interface {
	History(ctx context.Context, roomID string, query domain.MessageQuery) ([]domain.Message, error)
}

// MessageReader reads stored messages.
type MessageReader interface {
	Recent(ctx context.Context, roomID string, limit int) ([]domain.Message, error)
	Revisions(ctx context.Context, roomID, eventID string) ([]domain.Revision, error)
}

// MediaReader hands out the files of attachments.
type MediaReader interface {
	Serves(attachment *domain.Attachment) bool
	Open(ctx context.Context, roomID, eventID string) (domain.Message, []byte, error)
}

// noMedia is used when files are not served.
type noMedia struct{}

func (noMedia) Serves(*domain.Attachment) bool { return false }
func (noMedia) Open(context.Context, string, string) (domain.Message, []byte, error) {
	return domain.Message{}, nil, domain.ErrMediaUnavailable
}

// Access checks and manages the credentials: feed tokens and API keys.
type Access interface {
	FeedByToken(ctx context.Context, secret string) (domain.Feed, error)
	AuthenticateKey(ctx context.Context, secret string) (domain.APIKey, error)

	CreateAPIKey(ctx context.Context, req domain.KeyRequest) (domain.APIKey, string, error)
	ListAPIKeys(ctx context.Context) ([]domain.APIKey, error)
	RevokeAPIKey(ctx context.Context, id uint64) error
	IssueFeedToken(ctx context.Context, roomID, name, createdBy string) (domain.FeedToken, string, error)
	ListFeedTokens(ctx context.Context, roomID string) ([]domain.FeedToken, error)
	RevokeFeedToken(ctx context.Context, id uint64) error
}

// Deps are the use cases the handlers call.
type Deps struct {
	Feeds    FeedLister
	History  HistoryReader
	Messages MessageReader
	Media    MediaReader
	Access   Access
}

var (
	errBadLimit   = errors.New("limit must be a positive integer")
	errBadCursor  = errors.New("before must be a next_before value of an earlier response")
	errBadDeleted = errors.New("deleted must be exclude, include or only")
	errBadBody    = errors.New("request body must be a JSON object with known fields")
)

// Config holds what the handlers need besides the use cases.
type Config struct {
	// RootToken is the bearer token of the administrator. It may use everything, including the
	// admin endpoints that manage API keys and feed tokens. API keys are read-only.
	RootToken     string
	PublicBaseURL string
}

type handler struct {
	Deps
	cfg Config
	log *slog.Logger
}

// New builds the HTTP handler with all routes.
func New(deps Deps, cfg Config, log *slog.Logger) http.Handler {
	if deps.Media == nil {
		deps.Media = noMedia{}
	}
	h := &handler{Deps: deps, cfg: cfg, log: log}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", h.health)
	mux.HandleFunc("GET /feeds/{token}", h.feed)
	mux.HandleFunc("GET /feeds/{token}/media/{eventID}", h.feedMedia)

	mux.Handle("GET /api/v1/rooms", h.authenticated(h.listRooms))
	mux.Handle("GET /api/v1/rooms/{roomID}/messages", h.authenticated(h.listMessages))
	mux.Handle("GET /api/v1/rooms/{roomID}/messages/{eventID}/history", h.authenticated(h.messageHistory))
	mux.Handle("GET /api/v1/rooms/{roomID}/messages/{eventID}/media", h.authenticated(h.apiMedia))

	mux.Handle("GET /api/v1/admin/keys", h.adminOnly(h.listKeys))
	mux.Handle("POST /api/v1/admin/keys", h.adminOnly(h.createKey))
	mux.Handle("DELETE /api/v1/admin/keys/{id}", h.adminOnly(h.revokeKey))
	mux.Handle("GET /api/v1/admin/rooms/{roomID}/tokens", h.adminOnly(h.listTokens))
	mux.Handle("POST /api/v1/admin/rooms/{roomID}/tokens", h.adminOnly(h.createToken))
	mux.Handle("DELETE /api/v1/admin/tokens/{id}", h.adminOnly(h.revokeToken))
	return mux
}

// NewServer wraps the handler into an http.Server with sane timeouts.
func NewServer(addr string, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      120 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
}

// FeedURL returns the builder of public feed links.
func FeedURL(publicBaseURL string) func(token string) string {
	base := strings.TrimRight(publicBaseURL, "/")
	return func(token string) string { return base + "/feeds/" + token }
}

func (h *handler) health(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

func (h *handler) fail(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, domain.ErrFeedNotFound), errors.Is(err, domain.ErrMessageNotFound), errors.Is(err, domain.ErrCredentialNotFound):
		writeError(w, http.StatusNotFound, "not found")
	case errors.Is(err, domain.ErrMediaUnavailable):
		writeError(w, http.StatusNotFound, "media not available")
	case errors.Is(err, domain.ErrMediaTooLarge):
		writeError(w, http.StatusRequestEntityTooLarge, "media too large")
	case errors.Is(err, domain.ErrInvalid):
		writeError(w, http.StatusBadRequest, err.Error())
	default:
		h.log.ErrorContext(r.Context(), "request failed", "path", r.URL.Path, "error", err)
		writeError(w, http.StatusInternalServerError, "internal error")
	}
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
