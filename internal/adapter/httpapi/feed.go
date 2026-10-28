package httpapi

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/gorilla/feeds"

	"github.com/greatgoodtiver/matrixrss/internal/domain"
)

const (
	feedMaxAgeSeconds = 60
	maxTitleRunes     = 80
	matrixToBase      = "https://matrix.to/#/"
)

func (h *handler) feed(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	feed, err := h.Access.FeedByToken(r.Context(), r.PathValue("token"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	messages, err := h.Messages.Recent(r.Context(), feed.RoomID, limit)
	if err != nil {
		h.fail(w, r, err)
		return
	}

	rendered := buildFeed(feed, messages)
	var (
		body        string
		contentType string
	)
	switch r.URL.Query().Get("format") {
	case "atom":
		body, err = rendered.ToAtom()
		contentType = "application/atom+xml; charset=utf-8"
	case "json":
		body, err = rendered.ToJSON()
		contentType = "application/feed+json; charset=utf-8"
	default:
		body, err = rendered.ToRss()
		contentType = "application/rss+xml; charset=utf-8"
	}
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeCacheable(w, r, contentType, body)
}

// writeCacheable answers with an ETag derived from the body, so pollers get 304 for an unchanged feed.
func writeCacheable(w http.ResponseWriter, r *http.Request, contentType, body string) {
	sum := sha256.Sum256([]byte(body))
	etag := `"` + hex.EncodeToString(sum[:16]) + `"`
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "private, max-age="+strconv.Itoa(feedMaxAgeSeconds))
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", contentType)
	_, _ = w.Write([]byte(body))
}

func buildFeed(feed domain.Feed, messages []domain.Message) *feeds.Feed {
	result := &feeds.Feed{
		Title:       feed.Title,
		Link:        &feeds.Link{Href: matrixToBase + url.PathEscape(feed.RoomID)},
		Description: "Messages of the Matrix room " + feed.Title,
		Created:     feed.CreatedAt.UTC(),
		Items:       make([]*feeds.Item, len(messages)),
	}
	for i, msg := range messages {
		result.Items[i] = buildItem(msg)
	}
	if len(messages) > 0 {
		result.Updated = messages[0].SentAt
	}
	return result
}

func buildItem(msg domain.Message) *feeds.Item {
	name := msg.SenderName
	if name == "" {
		name = msg.Sender
	}
	return &feeds.Item{
		Id:          msg.EventID,
		Title:       name + ": " + shorten(msg.Body),
		Link:        &feeds.Link{Href: matrixToBase + url.PathEscape(msg.RoomID) + "/" + url.PathEscape(msg.EventID)},
		Author:      &feeds.Author{Name: name},
		Description: itemDescription(msg),
		Created:     msg.SentAt,
	}
}

func itemDescription(msg domain.Message) string {
	if msg.MediaURL == "" {
		return msg.Body
	}
	return msg.Body + "\n" + msg.MediaURL
}

// shorten keeps the first line of the text and cuts it to a headline length.
func shorten(text string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(text), "\n")
	if utf8.RuneCountInString(line) <= maxTitleRunes {
		return line
	}
	return string([]rune(line)[:maxTitleRunes]) + "…"
}
