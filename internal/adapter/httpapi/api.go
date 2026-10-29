package httpapi

import (
	"net/http"
	"strconv"
	"time"

	"github.com/greatgoodtiver/matrixrss/internal/domain"
)

type roomDTO struct {
	RoomID string `json:"room_id"`
	Title  string `json:"title"`
}

type messageDTO struct {
	Seq        uint64 `json:"seq"`
	EventID    string `json:"event_id"`
	RoomID     string `json:"room_id"`
	Sender     string `json:"sender"`
	SenderName string `json:"sender_name"`
	Kind       string `json:"kind"`
	Body       string `json:"body"`
	// FormattedBody is the sanitized HTML of a formatted message.
	FormattedBody string         `json:"formatted_body,omitempty"`
	Attachment    *attachmentDTO `json:"attachment,omitempty"`
	Location      *locationDTO   `json:"location,omitempty"`
	ReplyTo       string         `json:"reply_to,omitempty"`
	ThreadRoot    string         `json:"thread_root,omitempty"`
	SentAt        time.Time      `json:"sent_at"`
	EditedAt      *time.Time     `json:"edited_at,omitempty"`
	Deleted       *deletionDTO   `json:"deleted,omitempty"`
}

type attachmentDTO struct {
	Name       string `json:"name,omitempty"`
	MIMEType   string `json:"mime_type,omitempty"`
	Size       int64  `json:"size,omitempty"`
	Width      int    `json:"width,omitempty"`
	Height     int    `json:"height,omitempty"`
	DurationMS int64  `json:"duration_ms,omitempty"`
	Voice      bool   `json:"voice,omitempty"`
	// URL downloads the file with the same credentials as the API; absent when the file cannot be served.
	URL string `json:"url,omitempty"`
}

type locationDTO struct {
	GeoURI string `json:"geo_uri"`
	MapURL string `json:"map_url,omitempty"`
}

type deletionDTO struct {
	At     time.Time `json:"at"`
	By     string    `json:"by"`
	Reason string    `json:"reason,omitempty"`
}

type messagesDTO struct {
	Messages []messageDTO `json:"messages"`
	// NextBefore is the "before" value of the next, older page; absent when the page is empty.
	NextBefore string `json:"next_before,omitempty"`
}

type revisionDTO struct {
	EventID   string    `json:"event_id"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"created_at"`
}

type revisionsDTO struct {
	EventID   string        `json:"event_id"`
	Revisions []revisionDTO `json:"revisions"`
}

func (h *handler) listRooms(w http.ResponseWriter, r *http.Request, who principal) {
	feeds, err := h.Feeds.ListEnabled(r.Context())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	rooms := make([]roomDTO, 0, len(feeds))
	for _, feed := range feeds {
		if who.allowsRoom(feed.RoomID) {
			rooms = append(rooms, roomDTO{RoomID: feed.RoomID, Title: feed.Title})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"rooms": rooms})
}

func (h *handler) listMessages(w http.ResponseWriter, r *http.Request, who principal) {
	roomID := r.PathValue("roomID")
	if !who.allowsRoom(roomID) {
		writeError(w, http.StatusForbidden, "this key has no access to the room")
		return
	}
	query, err := parseQuery(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	messages, err := h.History.History(r.Context(), roomID, query)
	if err != nil {
		h.fail(w, r, err)
		return
	}

	body := messagesDTO{Messages: make([]messageDTO, len(messages))}
	for i, msg := range messages {
		body.Messages[i] = h.toMessageDTO(msg)
	}
	if len(messages) > 0 {
		body.NextBefore = domain.NewCursor(messages[len(messages)-1]).String()
	}
	writeJSON(w, http.StatusOK, body)
}

func (h *handler) messageHistory(w http.ResponseWriter, r *http.Request, who principal) {
	roomID, eventID := r.PathValue("roomID"), r.PathValue("eventID")
	if !who.allowsRoom(roomID) {
		writeError(w, http.StatusForbidden, "this key has no access to the room")
		return
	}
	revisions, err := h.Messages.Revisions(r.Context(), roomID, eventID)
	if err != nil {
		h.fail(w, r, err)
		return
	}

	body := revisionsDTO{EventID: eventID, Revisions: make([]revisionDTO, len(revisions))}
	for i, revision := range revisions {
		body.Revisions[i] = revisionDTO{EventID: revision.EventID, Body: revision.Body, CreatedAt: revision.CreatedAt}
	}
	writeJSON(w, http.StatusOK, body)
}

func parseQuery(r *http.Request) (domain.MessageQuery, error) {
	var (
		query domain.MessageQuery
		err   error
		args  = r.URL.Query()
	)
	if raw := args.Get("limit"); raw != "" {
		if query.Limit, err = strconv.Atoi(raw); err != nil || query.Limit < 1 {
			return query, errBadLimit
		}
	}
	if raw := args.Get("before"); raw != "" {
		if query.Before, err = domain.ParseCursor(raw); err != nil {
			return query, errBadCursor
		}
	}
	if query.Deleted, err = domain.ParseDeletedFilter(args.Get("deleted")); err != nil {
		return query, errBadDeleted
	}
	return query, nil
}

func (h *handler) toMessageDTO(msg domain.Message) messageDTO {
	dto := messageDTO{
		Seq: msg.Seq, EventID: msg.EventID, RoomID: msg.RoomID, Sender: msg.Sender, SenderName: msg.SenderName,
		Kind: string(msg.Kind), Body: msg.Body, FormattedBody: msg.Formatted, ReplyTo: msg.ReplyTo, ThreadRoot: msg.ThreadRoot,
		SentAt: msg.SentAt, EditedAt: msg.EditedAt,
	}
	if a := msg.Attachment; a != nil {
		dto.Attachment = &attachmentDTO{
			Name: a.Name, MIMEType: a.MIMEType, Size: a.Size, Width: a.Width, Height: a.Height,
			DurationMS: a.Duration.Milliseconds(), Voice: a.Voice,
		}
		if h.Media.Serves(a) {
			dto.Attachment.URL = h.apiMediaURL(msg)
		}
	}
	if msg.Location != "" {
		dto.Location = &locationDTO{GeoURI: msg.Location, MapURL: mapURL(msg.Location)}
	}
	if msg.Deletion != nil {
		dto.Deleted = &deletionDTO{At: msg.Deletion.At, By: msg.Deletion.By, Reason: msg.Deletion.Reason}
	}
	return dto
}
