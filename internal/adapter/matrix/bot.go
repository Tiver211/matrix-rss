package matrix

import (
	"context"
	"errors"
	"fmt"

	"github.com/rs/zerolog"
	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"github.com/greatgoodtiver/matrixrss/internal/domain"
)

// Bot listens to Matrix events, records messages of published rooms and answers commands.
type Bot struct {
	client   *mautrix.Client
	commands *Commands
	recorder MessageRecorder
	log      zerolog.Logger

	roomAdminLevel int
	feedList       FeedLister
}

// Config groups the bot's collaborators and settings.
// Admins may invite the bot and manage feeds; FeedURL builds the feed links the bot posts.
type Config struct {
	Credentials   Credentials
	Database      Database
	Admins        []string
	CommandPrefix string
	FeedURL       func(token string) string
	// RoomAdminLevel is the power level from which a room member counts as an admin of the room and
	// may manage its tokens and keys; 0 turns this off, leaving it to the bot's admins.
	RoomAdminLevel int
	Feeds          FeedManager
	FeedList       FeedLister
	Access         AccessManager
	Recorder       MessageRecorder
	Log            zerolog.Logger
}

// New logs in and wires the event handlers. Call Run to start processing events.
func New(ctx context.Context, cfg Config) (*Bot, error) {
	client, err := connect(ctx, cfg.Credentials, cfg.Database, cfg.Log)
	if err != nil {
		return nil, err
	}
	bot := &Bot{client: client, recorder: cfg.Recorder, log: cfg.Log, roomAdminLevel: cfg.RoomAdminLevel, feedList: cfg.FeedList}
	bot.commands = NewCommands(cfg.CommandPrefix, cfg.Admins, CommandDeps{
		Feeds: cfg.Feeds, Access: cfg.Access, Rooms: bot, Chats: bot, Authority: bot, FeedURL: cfg.FeedURL,
	})

	syncer, ok := client.Syncer.(*mautrix.DefaultSyncer)
	if !ok {
		return nil, errors.New("unexpected syncer type")
	}
	syncer.OnEventType(event.EventMessage, bot.onMessage)
	syncer.OnEventType(event.EventRedaction, bot.onRedaction)
	syncer.OnEventType(event.StateMember, bot.onMember)
	return bot, nil
}

// Run syncs with the homeserver until the context is cancelled.
func (b *Bot) Run(ctx context.Context) error {
	b.log.Info().Stringer("user_id", b.client.UserID).Stringer("device_id", b.client.DeviceID).Msg("bot started")
	if err := b.client.SyncWithContext(ctx); err != nil && !errors.Is(err, context.Canceled) {
		return fmt.Errorf("sync: %w", err)
	}
	return nil
}

// RoomName implements RoomNamer; it falls back to the room ID when the room has no name.
func (b *Bot) RoomName(ctx context.Context, roomID string) string {
	var name event.RoomNameEventContent
	err := b.client.StateEvent(ctx, id.RoomID(roomID), event.StateRoomName, "", &name)
	if err != nil || name.Name == "" {
		return roomID
	}
	return name.Name
}

// IsPrivateChat implements PrivateChats: the room has exactly two members, the bot and the user.
// If the members cannot be read the room counts as shared, which is the safe answer.
func (b *Bot) IsPrivateChat(ctx context.Context, roomID, userID string) bool {
	members, err := b.client.JoinedMembers(ctx, id.RoomID(roomID))
	if err != nil {
		b.log.Warn().Err(err).Str("room_id", roomID).Msg("cannot read room members")
		return false
	}
	_, hasUser := members.Joined[id.UserID(userID)]
	_, hasBot := members.Joined[b.client.UserID]
	return len(members.Joined) == 2 && hasUser && hasBot
}

// IsRoomAdmin implements RoomAuthority: the user is a member of the room and has at least the
// configured power level there. Any failure to find out counts as "no".
func (b *Bot) IsRoomAdmin(ctx context.Context, roomID, userID string) bool {
	if b.roomAdminLevel <= 0 {
		return false
	}
	log := b.log.With().Str("room_id", roomID).Str("user_id", userID).Logger()

	var levels event.PowerLevelsEventContent
	if err := b.client.StateEvent(ctx, id.RoomID(roomID), event.StatePowerLevels, "", &levels); err != nil {
		log.Debug().Err(err).Msg("cannot read power levels")
		return false
	}
	// In rooms of version 12 and later the creators have unlimited power without being listed in the
	// power levels, so the create event is needed to recognise them.
	if create, err := b.client.FullStateEvent(ctx, id.RoomID(roomID), event.StateCreate, ""); err == nil {
		_ = create.Content.ParseRaw(create.Type)
		levels.CreateEvent = create
	} else {
		log.Debug().Err(err).Msg("cannot read create event")
	}
	if levels.GetUserLevel(id.UserID(userID)) < b.roomAdminLevel {
		return false
	}
	// A user who left the room can still be listed in its power levels.
	members, err := b.client.JoinedMembers(ctx, id.RoomID(roomID))
	if err != nil {
		log.Debug().Err(err).Msg("cannot read room members")
		return false
	}
	_, joined := members.Joined[id.UserID(userID)]
	return joined
}

// isBotTraffic reports whether the event is the bot talking or being talked to: its own replies, which
// can contain secrets, and the commands people send it. Neither belongs in a feed.
func (b *Bot) isBotTraffic(evt *event.Event) bool {
	if evt.Sender == b.client.UserID {
		return true
	}
	content := evt.Content.AsMessage()
	return content != nil && b.commands.IsCommand(content.Body)
}

func (b *Bot) onMessage(ctx context.Context, evt *event.Event) {
	if evt.Sender == b.client.UserID {
		return
	}
	log := b.log.With().Stringer("room_id", evt.RoomID).Stringer("event_id", evt.ID).Logger()

	if content := evt.Content.AsMessage(); content != nil {
		if reply, isCommand := b.commands.Handle(ctx, evt.RoomID.String(), evt.Sender.String(), content.Body); isCommand {
			b.reply(ctx, evt.RoomID, reply, log)
			return
		}
	}

	if edit, ok := toEdit(evt); ok {
		if err := b.recorder.Edit(ctx, edit); err != nil {
			log.Error().Err(err).Msg("cannot record edit")
		}
		return
	}
	msg, ok := toMessage(evt, b.displayName(ctx, evt), b.mediaURL)
	if !ok {
		return
	}
	if err := b.recorder.Record(ctx, msg); err != nil {
		log.Error().Err(err).Msg("cannot record message")
	}
}

func (b *Bot) onRedaction(ctx context.Context, evt *event.Event) {
	target := evt.Redacts
	if content := evt.Content.AsRedaction(); content != nil && content.Redacts != "" {
		target = content.Redacts
	}
	if target == "" {
		return
	}
	deletion := domain.Deletion{At: toTime(evt.Timestamp), By: evt.Sender.String(), Reason: redactionReason(evt)}
	if err := b.recorder.Delete(ctx, evt.RoomID.String(), target.String(), deletion); err != nil {
		b.log.Error().Err(err).Stringer("event_id", target).Msg("cannot record deletion")
	}
}

// onMember accepts invitations, but only from people the bot works for.
func (b *Bot) onMember(ctx context.Context, evt *event.Event) {
	member := evt.Content.AsMember()
	if member == nil || member.Membership != event.MembershipInvite || evt.GetStateKey() != b.client.UserID.String() {
		return
	}
	log := b.log.With().Stringer("room_id", evt.RoomID).Stringer("inviter", evt.Sender).Logger()
	log.Debug().Bool("direct", member.IsDirect).Msg("invited")
	if !b.shouldJoin(ctx, evt, member) {
		log.Warn().Bool("direct", member.IsDirect).Msg("ignoring invitation")
		return
	}
	if _, err := b.client.JoinRoomByID(ctx, evt.RoomID); err != nil {
		log.Error().Err(err).Msg("cannot join room")
		return
	}
	log.Info().Msg("joined room")
}

// shouldJoin accepts invitations of bot admins to any room, and private chats started by admins of a
// published room, because that is where they receive the secrets they ask for.
func (b *Bot) shouldJoin(ctx context.Context, evt *event.Event, member *event.MemberEventContent) bool {
	inviter := evt.Sender.String()
	if b.commands.IsAdmin(inviter) {
		return true
	}
	if !member.IsDirect || b.feedList == nil {
		return false
	}
	feeds, err := b.feedList.ListEnabled(ctx)
	if err != nil {
		b.log.Error().Err(err).Msg("cannot list feeds")
		return false
	}
	for _, feed := range feeds {
		if b.IsRoomAdmin(ctx, feed.RoomID, inviter) {
			return true
		}
	}
	return false
}

func (b *Bot) reply(ctx context.Context, roomID id.RoomID, text string, log zerolog.Logger) {
	if _, err := b.client.SendNotice(ctx, roomID, text); err != nil {
		log.Error().Err(err).Msg("cannot send reply")
	}
}

func (b *Bot) displayName(ctx context.Context, evt *event.Event) string {
	member, err := b.client.StateStore.GetMember(ctx, evt.RoomID, evt.Sender)
	if err != nil || member == nil || member.Displayname == "" {
		return evt.Sender.String()
	}
	return member.Displayname
}

// mediaURL returns a download link for unencrypted attachments and an empty string otherwise.
func (b *Bot) mediaURL(content event.MessageEventContent) string {
	uri, err := content.URL.Parse()
	if err != nil || uri.IsEmpty() {
		return ""
	}
	return b.client.BuildURL(mautrix.MediaURLPath{"v3", "download", uri.Homeserver, uri.FileID})
}
