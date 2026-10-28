package matrix

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/greatgoodtiver/matrixrss/internal/domain"
)

const dateFormat = "2006-01-02"

func (c *call) token(args []string) string {
	if len(args) == 0 {
		return c.help()
	}
	switch args[0] {
	case "list":
		return c.listTokens(args[1:])
	case "revoke":
		return c.revokeToken(args[1:])
	case "create":
		return c.createToken(args[1:])
	}
	return c.help()
}

func (c *call) key(args []string) string {
	if len(args) == 0 {
		return c.help()
	}
	switch args[0] {
	case "list":
		return c.listKeys()
	case "revoke":
		return c.revokeKey(args[1:])
	case "create":
		return c.createKey(args[1:])
	}
	return c.help()
}

func (c *call) listTokens(args []string) string {
	roomID := c.roomID
	if len(args) > 0 {
		roomID = args[0]
	} else if c.chats.IsPrivateChat(c.ctx, c.roomID, c.sender) {
		return "In a private chat, say which room: " + c.prefix + " token list <room_id>"
	}
	if !c.canManage(roomID) {
		return denied
	}

	tokens, err := c.access.ListFeedTokens(c.ctx, roomID)
	if err != nil {
		return explain(err)
	}
	if len(tokens) == 0 {
		return "No feed tokens for " + roomID + "."
	}
	lines := make([]string, len(tokens))
	for i, token := range tokens {
		lines[i] = fmt.Sprintf("#%d  %s  %s…  created %s by %s%s",
			token.ID, token.Name, token.Hint, token.CreatedAt.Format(dateFormat), token.CreatedBy, revokedNote(token.RevokedAt))
	}
	return "Feed tokens of " + roomID + ":\n" + strings.Join(lines, "\n")
}

func (c *call) revokeToken(args []string) string {
	id, ok := parseID(args)
	if !ok {
		return "Usage: " + c.prefix + " token revoke <id>"
	}
	token, err := c.access.FindFeedToken(c.ctx, id)
	if err != nil {
		return explain(err)
	}
	if !c.canManage(token.RoomID) {
		return explain(domain.ErrCredentialNotFound) // do not reveal tokens of other rooms
	}
	if err := c.access.RevokeFeedToken(c.ctx, id); err != nil {
		return explain(err)
	}
	return fmt.Sprintf("Feed token #%d revoked.", id)
}

func (c *call) createToken(args []string) string {
	roomID, name := c.roomID, ""
	switch len(args) {
	case 1:
		name = args[0]
	case 2:
		roomID, name = args[0], args[1]
	default:
		return "Usage: " + c.prefix + " token create <room_id> <name>"
	}
	private := c.chats.IsPrivateChat(c.ctx, c.roomID, c.sender)
	if private && len(args) == 1 {
		return "In a private chat, say which room: " + c.prefix + " token create <room_id> <name>"
	}
	if !c.canManage(roomID) {
		return denied
	}
	if !private {
		return c.privateOnly(fmt.Sprintf("%s token create %s %s", c.prefix, roomID, name))
	}

	token, secret, err := c.access.IssueFeedToken(c.ctx, roomID, name, c.sender)
	if err != nil {
		return explain(err)
	}
	return fmt.Sprintf("Feed token #%d %q for %s (shown only now):\n%s", token.ID, token.Name, roomID, c.feedURL(secret))
}

// manageableKey reports whether the sender may see and revoke the key: bot admins any key, room
// admins only keys that are limited to rooms they administer.
func (c *call) manageableKey(key domain.APIKey) bool {
	if c.IsAdmin(c.sender) {
		return true
	}
	if len(key.Rooms) == 0 {
		return false
	}
	for _, room := range key.Rooms {
		if !c.canManage(room) {
			return false
		}
	}
	return true
}

func (c *call) listKeys() string {
	keys, err := c.access.ListAPIKeys(c.ctx)
	if err != nil {
		return explain(err)
	}
	var lines []string
	for _, key := range keys {
		if c.manageableKey(key) {
			lines = append(lines, fmt.Sprintf("#%d  %s  %s…  rooms: %s  expires: %s  created %s by %s%s",
				key.ID, key.Name, key.Hint, roomsNote(key.Rooms), expiryNote(key.ExpiresAt),
				key.CreatedAt.Format(dateFormat), key.CreatedBy, revokedNote(key.RevokedAt)))
		}
	}
	if len(lines) == 0 {
		return "No API keys."
	}
	return "API keys:\n" + strings.Join(lines, "\n")
}

func (c *call) revokeKey(args []string) string {
	id, ok := parseID(args)
	if !ok {
		return "Usage: " + c.prefix + " key revoke <id>"
	}
	key, err := c.access.FindAPIKey(c.ctx, id)
	if err != nil {
		return explain(err)
	}
	if !c.manageableKey(key) {
		return explain(domain.ErrCredentialNotFound) // do not reveal keys of other rooms
	}
	if err := c.access.RevokeAPIKey(c.ctx, id); err != nil {
		return explain(err)
	}
	return fmt.Sprintf("API key #%d revoked.", id)
}

func (c *call) createKey(args []string) string {
	if len(args) == 0 {
		return "Usage: " + c.prefix + " key create <name> [rooms=!a:hs,!b:hs] [expires=30d]"
	}
	req, err := parseKeyRequest(args, c.sender, time.Now())
	if err != nil {
		return explain(err)
	}
	private := c.chats.IsPrivateChat(c.ctx, c.roomID, c.sender)

	command := append([]string{c.prefix, "key", "create"}, args...)
	if !c.IsAdmin(c.sender) {
		// A room admin's key must be limited to rooms they administer; by default to this room.
		if len(req.Rooms) == 0 {
			if private {
				return "Admins of a room must limit the key to rooms they administer: add rooms=<room_id>."
			}
			req.Rooms = []string{c.roomID}
			command = append(command, "rooms="+c.roomID)
		}
		for _, room := range req.Rooms {
			if !c.canManage(room) {
				return denied
			}
		}
	}
	if !private {
		return c.privateOnly(strings.Join(command, " "))
	}

	key, secret, err := c.access.CreateAPIKey(c.ctx, req)
	if err != nil {
		return explain(err)
	}
	return fmt.Sprintf("API key #%d %q, rooms: %s, expires: %s (shown only now):\n%s",
		key.ID, key.Name, roomsNote(key.Rooms), expiryNote(key.ExpiresAt), secret)
}

// parseKeyRequest reads "<name> [rooms=a,b] [expires=30d]".
func parseKeyRequest(args []string, createdBy string, now time.Time) (domain.KeyRequest, error) {
	req := domain.KeyRequest{Name: args[0], CreatedBy: createdBy}
	for _, option := range args[1:] {
		name, value, ok := strings.Cut(option, "=")
		switch {
		case ok && name == "rooms" && value != "":
			req.Rooms = strings.Split(value, ",")
		case ok && name == "expires":
			lifetime, err := domain.ParseLifetime(value)
			if err != nil {
				return req, err
			}
			expiresAt := now.Add(lifetime).UTC()
			req.ExpiresAt = &expiresAt
		default:
			return req, fmt.Errorf("%w: unknown option %q", domain.ErrInvalid, option)
		}
	}
	return req, nil
}

func (c *call) privateOnly(command string) string {
	return "Secrets are only handed out in a private chat, because everyone in this room could read them. " +
		"Open a private chat with me and send:\n" + command
}

func parseID(args []string) (uint64, bool) {
	if len(args) != 1 {
		return 0, false
	}
	id, err := strconv.ParseUint(strings.TrimPrefix(args[0], "#"), 10, 64)
	return id, err == nil
}

func revokedNote(at *time.Time) string {
	if at == nil {
		return ""
	}
	return "  REVOKED " + at.Format(dateFormat)
}

func roomsNote(rooms []string) string {
	if len(rooms) == 0 {
		return "all"
	}
	return strings.Join(rooms, ",")
}

func expiryNote(at *time.Time) string {
	if at == nil {
		return "never"
	}
	return at.Format(dateFormat)
}
