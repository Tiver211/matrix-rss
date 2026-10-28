package matrix

import (
	"context"
	"errors"
	"fmt"

	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"github.com/greatgoodtiver/matrixrss/internal/usecase"
)

// historyFilter limits the fetched history to what can hold messages; encrypted events are
// decrypted afterwards.
var historyFilter = &mautrix.FilterPart{Types: []event.Type{event.EventMessage, event.EventEncrypted}}

// History reads room history from the homeserver. It implements usecase.HistorySource.
type History struct {
	bot *Bot
}

var _ usecase.HistorySource = (*History)(nil)

// History returns the homeserver history reader of the bot.
func (b *Bot) History() *History {
	return &History{bot: b}
}

// Older returns events older than the cursor, converted to domain objects. Events that cannot be
// used, such as messages the bot has no decryption keys for, are skipped.
func (h *History) Older(ctx context.Context, roomID string, from usecase.HistoryCursor, limit int) (usecase.HistoryPage, error) {
	chunk, next, err := h.fetch(ctx, id.RoomID(roomID), from, limit)
	if err != nil {
		return usecase.HistoryPage{}, err
	}

	page := usecase.HistoryPage{Next: usecase.HistoryCursor{Token: next}}
	for _, evt := range chunk {
		evt.RoomID = id.RoomID(roomID)
		evt, ok := h.usable(ctx, evt)
		if !ok || h.bot.isBotTraffic(evt) {
			continue
		}
		if edit, isEdit := toEdit(evt); isEdit {
			page.Edits = append(page.Edits, edit)
		} else if msg, isMessage := toMessage(evt, h.bot.displayName(ctx, evt), h.bot.mediaURL); isMessage {
			page.Messages = append(page.Messages, msg)
		}
	}
	return page, nil
}

// fetch reads one batch. The first call anchors at a stored event through the context endpoint,
// whose events before the anchor are the first batch; later calls follow the pagination token.
func (h *History) fetch(ctx context.Context, roomID id.RoomID, from usecase.HistoryCursor, limit int) ([]*event.Event, string, error) {
	if from.Token == "" && from.EventID != "" {
		resp, err := h.bot.client.Context(ctx, roomID, id.EventID(from.EventID), historyFilter, limit)
		if err != nil {
			return nil, "", fmt.Errorf("get context: %w", err)
		}
		return resp.EventsBefore, resp.Start, nil
	}
	resp, err := h.bot.client.Messages(ctx, roomID, from.Token, "", mautrix.DirectionBackward, historyFilter, limit)
	if err != nil {
		return nil, "", fmt.Errorf("get messages: %w", err)
	}
	return resp.Chunk, resp.End, nil
}

// usable parses and, if needed, decrypts the event.
func (h *History) usable(ctx context.Context, evt *event.Event) (*event.Event, bool) {
	if evt.Unsigned.RedactedBecause != nil {
		return evt, true // the content is gone, nothing to parse or decrypt
	}
	if err := evt.Content.ParseRaw(evt.Type); err != nil && !errors.Is(err, event.ErrContentAlreadyParsed) {
		return nil, false
	}
	if evt.Type != event.EventEncrypted {
		return evt, true
	}
	decrypted, err := h.bot.client.Crypto.Decrypt(ctx, evt)
	if err != nil {
		h.bot.log.Debug().Err(err).Stringer("event_id", evt.ID).Msg("skipping history event that cannot be decrypted")
		return nil, false
	}
	return decrypted, true
}
