package matrix

import (
	"context"
	"errors"
	"strings"

	"github.com/greatgoodtiver/matrixrss/internal/domain"
)

const (
	denied        = "You are not allowed to do this."
	botAdminsOnly = "Only the bot's admins can publish or unpublish rooms."
)

// Commands interprets chat commands such as "!rss on" and produces the reply text.
//
// Bot admins (the configured list) may do everything. Admins of a room, users with a high enough
// power level in it, may manage the feed tokens and API keys of that room, and nothing else.
// Commands that hand out a new secret work in a private chat only, because everything said in a
// shared room is visible to all its members.
type Commands struct {
	prefix    string
	admins    map[string]struct{}
	feeds     FeedManager
	access    AccessManager
	rooms     RoomNamer
	chats     PrivateChats
	authority RoomAuthority
	feedURL   func(token string) string
}

// CommandDeps are the collaborators of Commands.
type CommandDeps struct {
	Feeds     FeedManager
	Access    AccessManager
	Rooms     RoomNamer
	Chats     PrivateChats
	Authority RoomAuthority
	FeedURL   func(token string) string
}

func NewCommands(prefix string, admins []string, deps CommandDeps) *Commands {
	allowed := make(map[string]struct{}, len(admins))
	for _, admin := range admins {
		allowed[admin] = struct{}{}
	}
	return &Commands{
		prefix: prefix, admins: allowed,
		feeds: deps.Feeds, access: deps.Access, rooms: deps.Rooms, chats: deps.Chats,
		authority: deps.Authority, feedURL: deps.FeedURL,
	}
}

// IsCommand reports whether the text is addressed to the bot, whoever wrote it.
func (c *Commands) IsCommand(text string) bool {
	_, ok := c.parse(text)
	return ok
}

// IsAdmin reports whether the user is one of the bot's admins.
func (c *Commands) IsAdmin(userID string) bool {
	_, ok := c.admins[userID]
	return ok
}

// Handle returns the reply and true when the text is a command, and false when it is an ordinary message.
func (c *Commands) Handle(ctx context.Context, roomID, sender, text string) (string, bool) {
	args, ok := c.parse(text)
	if !ok {
		return "", false
	}
	call := &call{Commands: c, ctx: ctx, roomID: roomID, sender: sender, rights: map[string]bool{}}

	switch args[0] {
	case "on":
		return call.enable(), true
	case "off":
		return call.disable(), true
	case "rotate":
		return call.rotate(), true
	case "token":
		return call.token(args[1:]), true
	case "key":
		return call.key(args[1:]), true
	default:
		return c.help(), true
	}
}

// parse splits "<prefix> <command> ..." into the command and its arguments.
func (c *Commands) parse(text string) ([]string, bool) {
	fields := strings.Fields(text)
	if len(fields) == 0 || !strings.EqualFold(fields[0], c.prefix) {
		return nil, false
	}
	if len(fields) == 1 {
		return []string{"help"}, true
	}
	fields[1] = strings.ToLower(fields[1])
	return fields[1:], true
}

func (c *Commands) help() string {
	return strings.Join([]string{
		"Commands:",
		c.prefix + " on / off                publish this room as a feed / stop recording and hide it (bot admins)",
		c.prefix + " rotate                  revoke all feed URLs of this room, show a new one",
		c.prefix + " token list [room_id]    list the feed tokens of a room",
		c.prefix + " token revoke <id>       revoke a feed token",
		c.prefix + " token create <room_id> <name>",
		c.prefix + " key list                list API keys",
		c.prefix + " key revoke <id>         revoke an API key",
		c.prefix + " key create <name> [rooms=!a:hs,!b:hs] [expires=30d]",
		"",
		"Bot admins manage everything. Admins of a room manage the tokens and keys of that room;",
		"their keys must be limited to rooms they administer (rooms=...).",
		"Creating a token or key shows its secret once and works only in a private chat with me.",
	}, "\n")
}

// call is one command invocation.
type call struct {
	*Commands
	ctx    context.Context
	roomID string
	sender string
	// rights caches the room admin checks, which cost requests to the homeserver.
	rights map[string]bool
}

// canManage reports whether the sender may manage the credentials of the room.
func (c *call) canManage(roomID string) bool {
	if c.IsAdmin(c.sender) {
		return true
	}
	allowed, known := c.rights[roomID]
	if !known {
		allowed = c.authority.IsRoomAdmin(c.ctx, roomID, c.sender)
		c.rights[roomID] = allowed
	}
	return allowed
}

func (c *call) enable() string {
	if !c.IsAdmin(c.sender) {
		return botAdminsOnly
	}
	feed, err := c.feeds.Enable(c.ctx, c.roomID, c.rooms.RoomName(c.ctx, c.roomID))
	if err != nil {
		return explain(err)
	}
	token, secret, err := c.access.EnsureDefaultFeedToken(c.ctx, feed.RoomID, c.sender)
	if err != nil {
		return explain(err)
	}
	if secret == "" {
		return "Feed enabled, new messages are recorded from now on. Its URLs are unchanged, see \"" + c.prefix + " token list\"."
	}
	return "Feed enabled, new messages are recorded from now on.\n" + c.feedURL(secret) + "\n(token " + token.Name + ", shown only now)"
}

func (c *call) disable() string {
	if !c.IsAdmin(c.sender) {
		return botAdminsOnly
	}
	if err := c.feeds.Disable(c.ctx, c.roomID); err != nil {
		return explain(err)
	}
	return "Feed disabled. Stored messages are kept, new ones are not recorded."
}

func (c *call) rotate() string {
	if !c.canManage(c.roomID) {
		return denied
	}
	_, secret, err := c.access.RotateFeedTokens(c.ctx, c.roomID, c.sender)
	if err != nil {
		return explain(err)
	}
	return "All earlier feed URLs of this room no longer work. New URL:\n" + c.feedURL(secret)
}

var errNoSuchCommand = errors.New("no such command")

func explain(err error) string {
	switch {
	case errors.Is(err, domain.ErrFeedNotFound):
		return "This room has no feed yet."
	case errors.Is(err, domain.ErrCredentialNotFound):
		return "There is no key or token with this ID."
	case errors.Is(err, domain.ErrInvalid), errors.Is(err, errNoSuchCommand):
		return err.Error()
	}
	return "Something went wrong, see the bot log."
}
