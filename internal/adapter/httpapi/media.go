package httpapi

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/greatgoodtiver/matrixrss/internal/domain"
)

const mediaMaxAgeSeconds = 3600

// apiMedia serves an attachment to the credentials of the API.
func (h *handler) apiMedia(w http.ResponseWriter, r *http.Request, who principal) {
	roomID := r.PathValue("roomID")
	if !who.allowsRoom(roomID) {
		writeError(w, http.StatusForbidden, "this key has no access to the room")
		return
	}
	h.serveMedia(w, r, roomID, r.PathValue("eventID"))
}

// feedMedia serves an attachment of a feed to whoever holds a token of that feed.
func (h *handler) feedMedia(w http.ResponseWriter, r *http.Request) {
	feed, err := h.Access.FeedByToken(r.Context(), r.PathValue("token"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.serveMedia(w, r, feed.RoomID, r.PathValue("eventID"))
}

func (h *handler) serveMedia(w http.ResponseWriter, r *http.Request, roomID, eventID string) {
	msg, data, err := h.Media.Open(r.Context(), roomID, eventID)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	attachment := msg.Attachment

	header := w.Header()
	if mimeType := attachment.MIMEType; mimeType != "" {
		if _, _, err := mime.ParseMediaType(mimeType); err == nil {
			header.Set("Content-Type", mimeType)
		}
	}
	header.Set("Content-Disposition", dispositionOf(attachment))
	// Files come from other people and are served from this origin: never let a browser run them.
	header.Set("X-Content-Type-Options", "nosniff")
	header.Set("Content-Security-Policy", "sandbox; default-src 'none'")
	header.Set("Cache-Control", "private, max-age="+strconv.Itoa(mediaMaxAgeSeconds))
	sum := sha256.Sum256([]byte(attachment.Ref))
	header.Set("ETag", `"`+hex.EncodeToString(sum[:16])+`"`)

	http.ServeContent(w, r, attachment.Name, msg.SentAt, bytes.NewReader(data))
}

// dispositionOf lets browsers show pictures, audio and video in place and makes them download
// everything else, SVG included, which can carry scripts.
func dispositionOf(attachment *domain.Attachment) string {
	kind := "attachment"
	if inlineSafe(attachment.MIMEType) {
		kind = "inline"
	}
	if disposition := mime.FormatMediaType(kind, map[string]string{"filename": attachment.Name}); disposition != "" {
		return disposition
	}
	return kind
}

func inlineSafe(mimeType string) bool {
	mimeType = strings.ToLower(mimeType)
	switch {
	case mimeType == "image/svg+xml":
		return false
	case strings.HasPrefix(mimeType, "image/"), strings.HasPrefix(mimeType, "audio/"), strings.HasPrefix(mimeType, "video/"):
		return true
	}
	return false
}

// apiMediaURL is where API clients download the attachment of a message.
func (h *handler) apiMediaURL(msg domain.Message) string {
	return strings.TrimRight(h.cfg.PublicBaseURL, "/") + "/api/v1/rooms/" + url.PathEscape(msg.RoomID) +
		"/messages/" + url.PathEscape(msg.EventID) + "/media"
}

// feedMediaURL is where readers of a feed download the attachment of a message.
func (h *handler) feedMediaURL(token string, msg domain.Message) string {
	return FeedURL(h.cfg.PublicBaseURL)(token) + "/media/" + url.PathEscape(msg.EventID)
}

// mapURL turns a geo: URI into a link to an OpenStreetMap view; it is empty if the URI is not understood.
func mapURL(geoURI string) string {
	coordinates, ok := strings.CutPrefix(geoURI, "geo:")
	if !ok {
		return ""
	}
	coordinates, _, _ = strings.Cut(coordinates, ";")
	lat, lon, ok := strings.Cut(coordinates, ",")
	if !ok {
		return ""
	}
	for _, value := range []string{lat, lon} {
		if _, err := strconv.ParseFloat(value, 64); err != nil {
			return ""
		}
	}
	return fmt.Sprintf("https://www.openstreetmap.org/?mlat=%s&mlon=%s#map=16/%s/%s", lat, lon, lat, lon)
}
