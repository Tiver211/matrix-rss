package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/greatgoodtiver/matrixrss/internal/domain"
)

const (
	maxBodyBytes = 64 << 10
	adminActor   = "api"
)

type keyDTO struct {
	ID        uint64     `json:"id"`
	Name      string     `json:"name"`
	Hint      string     `json:"hint"`
	Rooms     []string   `json:"rooms"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
	CreatedBy string     `json:"created_by"`
	RevokedAt *time.Time `json:"revoked_at,omitempty"`
	// Secret is present only in the response to the creation.
	Secret string `json:"secret,omitempty"`
}

type tokenDTO struct {
	ID        uint64     `json:"id"`
	RoomID    string     `json:"room_id"`
	Name      string     `json:"name"`
	Hint      string     `json:"hint"`
	CreatedAt time.Time  `json:"created_at"`
	CreatedBy string     `json:"created_by"`
	RevokedAt *time.Time `json:"revoked_at,omitempty"`
	// Secret and FeedURL are present only in the response to the creation.
	Secret  string `json:"secret,omitempty"`
	FeedURL string `json:"feed_url,omitempty"`
}

func (h *handler) createKey(w http.ResponseWriter, r *http.Request, _ principal) {
	var body struct {
		Name  string   `json:"name"`
		Rooms []string `json:"rooms"`
		// ExpiresIn is a duration such as "720h" or "30d"; empty means the key never expires.
		ExpiresIn string `json:"expires_in"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	req := domain.KeyRequest{Name: body.Name, Rooms: body.Rooms, CreatedBy: adminActor}
	if body.ExpiresIn != "" {
		lifetime, err := domain.ParseLifetime(body.ExpiresIn)
		if err != nil {
			h.fail(w, r, err)
			return
		}
		expiresAt := time.Now().Add(lifetime).UTC()
		req.ExpiresAt = &expiresAt
	}

	key, secret, err := h.Access.CreateAPIKey(r.Context(), req)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	dto := toKeyDTO(key)
	dto.Secret = secret
	writeJSON(w, http.StatusCreated, dto)
}

func (h *handler) listKeys(w http.ResponseWriter, r *http.Request, _ principal) {
	keys, err := h.Access.ListAPIKeys(r.Context())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	dtos := make([]keyDTO, len(keys))
	for i, key := range keys {
		dtos[i] = toKeyDTO(key)
	}
	writeJSON(w, http.StatusOK, map[string]any{"keys": dtos})
}

func (h *handler) revokeKey(w http.ResponseWriter, r *http.Request, _ principal) {
	h.revoke(w, r, h.Access.RevokeAPIKey)
}

func (h *handler) createToken(w http.ResponseWriter, r *http.Request, _ principal) {
	var body struct {
		Name string `json:"name"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	token, secret, err := h.Access.IssueFeedToken(r.Context(), r.PathValue("roomID"), body.Name, adminActor)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	dto := toTokenDTO(token)
	dto.Secret = secret
	dto.FeedURL = FeedURL(h.cfg.PublicBaseURL)(secret)
	writeJSON(w, http.StatusCreated, dto)
}

func (h *handler) listTokens(w http.ResponseWriter, r *http.Request, _ principal) {
	tokens, err := h.Access.ListFeedTokens(r.Context(), r.PathValue("roomID"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	dtos := make([]tokenDTO, len(tokens))
	for i, token := range tokens {
		dtos[i] = toTokenDTO(token)
	}
	writeJSON(w, http.StatusOK, map[string]any{"tokens": dtos})
}

func (h *handler) revokeToken(w http.ResponseWriter, r *http.Request, _ principal) {
	h.revoke(w, r, h.Access.RevokeFeedToken)
}

func (h *handler) revoke(w http.ResponseWriter, r *http.Request, revoke func(ctx context.Context, id uint64) error) {
	id, err := strconv.ParseUint(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "id must be a number")
		return
	}
	if err := revoke(r.Context(), id); err != nil {
		h.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func decodeBody(w http.ResponseWriter, r *http.Request, into any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(into); err != nil {
		writeError(w, http.StatusBadRequest, errBadBody.Error())
		return false
	}
	return true
}

func toKeyDTO(key domain.APIKey) keyDTO {
	rooms := key.Rooms
	if rooms == nil {
		rooms = []string{}
	}
	return keyDTO{
		ID: key.ID, Name: key.Name, Hint: key.Hint, Rooms: rooms, ExpiresAt: key.ExpiresAt,
		CreatedAt: key.CreatedAt, CreatedBy: key.CreatedBy, RevokedAt: key.RevokedAt,
	}
}

func toTokenDTO(token domain.FeedToken) tokenDTO {
	return tokenDTO{
		ID: token.ID, RoomID: token.RoomID, Name: token.Name, Hint: token.Hint,
		CreatedAt: token.CreatedAt, CreatedBy: token.CreatedBy, RevokedAt: token.RevokedAt,
	}
}
