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

	rendered := h.buildFeed(r.PathValue("token"), feed, messages)
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

func (h *handler) buildFeed(token string, feed domain.Feed, messages []domain.Message) *feeds.Feed {
	result := &feeds.Feed{
		Title:       feed.Title,
		Link:        &feeds.Link{Href: matrixToBase + url.PathEscape(feed.RoomID)},
		Description: "Messages of the Matrix room " + feed.Title,
		Created:     feed.CreatedAt.UTC(),
		Items:       make([]*feeds.Item, len(messages)),
	}
	for i, msg := range messages {
		result.Items[i] = h.buildItem(token, msg)
	}
	if len(messages) > 0 {
		result.Updated = messages[0].SentAt
	}
	return result
}

// kindLabels mark messages that are not plain text in the title of a feed item.
var kindLabels = map[domain.MessageKind]string{
	domain.KindImage:    "[image] ",
	domain.KindVideo:    "[video] ",
	domain.KindAudio:    "[audio] ",
	domain.KindFile:     "[file] ",
	domain.KindSticker:  "[sticker] ",
	domain.KindLocation: "[location] ",
	domain.KindPoll:     "[poll] ",
}

func (h *handler) buildItem(token string, msg domain.Message) *feeds.Item {
	name := msg.SenderName
	if name == "" {
		name = msg.Sender
	}
	label := kindLabels[msg.Kind]
	if msg.Attachment != nil && msg.Attachment.Voice {
		label = "[voice message] "
	}
	item := &feeds.Item{
		Id:          msg.EventID,
		Title:       name + ": " + label + shorten(msg.Body),
		Link:        &feeds.Link{Href: matrixToBase + url.PathEscape(msg.RoomID) + "/" + url.PathEscape(msg.EventID)},
		Author:      &feeds.Author{Name: name},
		Description: msg.Body,
		Content:     msg.Formatted,
		Created:     msg.SentAt,
	}

	if a := msg.Attachment; h.Media.Serves(a) {
		fileURL := h.feedMediaURL(token, msg)
		item.Description += "\n" + fileURL
		item.Enclosure = &feeds.Enclosure{Url: fileURL, Length: strconv.FormatInt(a.Size, 10), Type: enclosureType(a)}
	}
	if msg.Location != "" {
		item.Description += "\n" + msg.Location
		if link := mapURL(msg.Location); link != "" {
			item.Description += "\n" + link
		}
	}
	return item
}

func enclosureType(a *domain.Attachment) string {
	if a.MIMEType != "" {
		return a.MIMEType
	}
	return "application/octet-stream"
}

// shorten keeps the first line of the text and cuts it to a headline length.
func shorten(text string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(text), "\n")
	if utf8.RuneCountInString(line) <= maxTitleRunes {
		return line
	}
	return string([]rune(line)[:maxTitleRunes]) + "…"
}
