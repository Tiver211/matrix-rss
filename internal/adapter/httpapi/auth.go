package httpapi

import (
	"crypto/subtle"
	"errors"
	"net/http"
	"strings"

	"github.com/greatgoodtiver/matrixrss/internal/domain"
)

const (
	bearerPrefix = "Bearer "
	apiKeyPrefix = "mxk_"
)

// principal is who made an authenticated request.
type principal struct {
	root bool
	key  domain.APIKey
}

func (p principal) allowsRoom(roomID string) bool {
	return p.root || p.key.AllowsRoom(roomID)
}

type principalHandler func(w http.ResponseWriter, r *http.Request, who principal)

// authenticated accepts the root token and API keys.
func (h *handler) authenticated(next principalHandler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		who, err := h.authenticate(r)
		if errors.Is(err, domain.ErrInvalidCredentials) {
			w.Header().Set("WWW-Authenticate", "Bearer")
			writeError(w, http.StatusUnauthorized, "invalid or missing bearer token")
			return
		}
		if err != nil {
			h.fail(w, r, err)
			return
		}
		next(w, r, who)
	})
}

// adminOnly accepts the root token only; API keys get 403.
func (h *handler) adminOnly(next principalHandler) http.Handler {
	return h.authenticated(func(w http.ResponseWriter, r *http.Request, who principal) {
		if !who.root {
			writeError(w, http.StatusForbidden, "this endpoint needs the root token")
			return
		}
		next(w, r, who)
	})
}

func (h *handler) authenticate(r *http.Request) (principal, error) {
	header := r.Header.Get("Authorization")
	if !strings.HasPrefix(header, bearerPrefix) {
		return principal{}, domain.ErrInvalidCredentials
	}
	secret := strings.TrimPrefix(header, bearerPrefix)

	if subtle.ConstantTimeCompare([]byte(secret), []byte(h.cfg.RootToken)) == 1 {
		return principal{root: true}, nil
	}
	if !strings.HasPrefix(secret, apiKeyPrefix) {
		return principal{}, domain.ErrInvalidCredentials
	}
	key, err := h.Access.AuthenticateKey(r.Context(), secret)
	if err != nil {
		return principal{}, err
	}
	return principal{key: key}, nil
}
