// Package cli implements the management commands that work directly on the database.
package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/greatgoodtiver/matrixrss/internal/domain"
)

const usage = `Usage: matrixrss <command> [arguments]

Commands:
  serve                         run the bot and the HTTP server (default)
  rooms                         list published rooms with their feed URLs
  messages [-limit N] [-before CURSOR] [-deleted exclude|include|only] <room_id>
                                print stored messages of a room, oldest first
  history <room_id> <event_id>  print the versions of an edited message
  rotate <room_id>              revoke all feed tokens of a room and print a new feed URL
  disable <room_id>             stop recording a room and hide its feed
  token create <room_id> <name>   issue another feed token (secret shown once)
  token list <room_id>            list the feed tokens of a room
  token revoke <id>               revoke a feed token
  key create [-rooms a,b] [-expires 30d] <name>
                                issue a read-only API key (secret shown once)
  key list                        list API keys
  key revoke <id>                 revoke an API key
  help                          show this text`

// Feeds is the part of the feed use cases the commands need.
type Feeds interface {
	ListEnabled(ctx context.Context) ([]domain.Feed, error)
	Disable(ctx context.Context, roomID string) error
}

// Access is the part of the credential use cases the commands need.
type Access interface {
	RotateFeedTokens(ctx context.Context, roomID, createdBy string) (domain.FeedToken, string, error)
	IssueFeedToken(ctx context.Context, roomID, name, createdBy string) (domain.FeedToken, string, error)
	ListFeedTokens(ctx context.Context, roomID string) ([]domain.FeedToken, error)
	RevokeFeedToken(ctx context.Context, id uint64) error
	CreateAPIKey(ctx context.Context, req domain.KeyRequest) (domain.APIKey, string, error)
	ListAPIKeys(ctx context.Context) ([]domain.APIKey, error)
	RevokeAPIKey(ctx context.Context, id uint64) error
}

// actor is recorded as the creator of credentials issued from the command line.
const actor = "cli"

// Messages is the part of the message use cases the commands need. It reads the database only:
// unlike the HTTP API the commands do not fetch missing history from Matrix.
type Messages interface {
	Page(ctx context.Context, roomID string, query domain.MessageQuery) ([]domain.Message, error)
	Revisions(ctx context.Context, roomID, eventID string) ([]domain.Revision, error)
}

// Runner executes management commands and writes their results to Out.
type Runner struct {
	Feeds    Feeds
	Access   Access
	Messages Messages
	FeedURL  func(token string) string
	Out      io.Writer
	Err      io.Writer
}

// ErrUsage means the command line was wrong; the usage text has already been printed.
var ErrUsage = errors.New("invalid usage")

// IsServe reports whether the arguments ask for the long-running service.
// Running without a command also serves, so existing container entrypoints keep working.
func IsServe(args []string) bool {
	return len(args) == 0 || args[0] == "serve"
}

// IsHelp reports whether the arguments only ask for the usage text, which needs no database.
func IsHelp(args []string) bool {
	return len(args) > 0 && (args[0] == "help" || args[0] == "-h" || args[0] == "--help")
}

// Run executes the management command named by args[0].
func (r *Runner) Run(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return r.fail("missing command")
	}
	rest := args[1:]
	switch args[0] {
	case "rooms":
		return r.rooms(ctx)
	case "messages":
		return r.messages(ctx, rest)
	case "history":
		return r.history(ctx, rest)
	case "rotate":
		return r.rotate(ctx, rest)
	case "token":
		return r.token(ctx, rest)
	case "key":
		return r.key(ctx, rest)
	case "disable":
		return r.disable(ctx, rest)
	case "help", "-h", "--help":
		_, err := fmt.Fprintln(r.Out, usage)
		return err
	default:
		return r.fail("unknown command " + args[0])
	}
}

func (r *Runner) rooms(ctx context.Context) error {
	feeds, err := r.Feeds.ListEnabled(ctx)
	if err != nil {
		return err
	}
	table := tabwriter.NewWriter(r.Out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(table, "ROOM ID\tTITLE")
	for _, feed := range feeds {
		fmt.Fprintf(table, "%s\t%s\n", feed.RoomID, feed.Title)
	}
	return table.Flush()
}

func (r *Runner) messages(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("messages", flag.ContinueOnError)
	flags.SetOutput(r.Err)
	limit := flags.Int("limit", 20, "number of messages")
	before := flags.String("before", "", "only messages older than this cursor")
	deleted := flags.String("deleted", "exclude", "exclude, include or only deleted messages")
	if err := flags.Parse(args); err != nil {
		return ErrUsage
	}
	roomID, err := roomArg(flags.Args())
	if err != nil {
		return r.fail(err.Error())
	}
	query := domain.MessageQuery{Limit: *limit}
	if *before != "" {
		if query.Before, err = domain.ParseCursor(*before); err != nil {
			return r.fail(err.Error())
		}
	}
	if query.Deleted, err = domain.ParseDeletedFilter(*deleted); err != nil {
		return r.fail(err.Error())
	}

	page, err := r.Messages.Page(ctx, roomID, query)
	if err != nil {
		return notFoundOr(err)
	}
	for i := len(page) - 1; i >= 0; i-- {
		fmt.Fprintln(r.Out, formatMessage(page[i]))
	}
	if len(page) > 0 {
		fmt.Fprintf(r.Err, "older messages: -before %s\n", domain.NewCursor(page[len(page)-1]))
	}
	return nil
}

func (r *Runner) history(ctx context.Context, args []string) error {
	if len(args) != 2 {
		return r.fail("expected <room_id> <event_id>")
	}
	revisions, err := r.Messages.Revisions(ctx, args[0], args[1])
	if err != nil {
		return notFoundOr(err)
	}
	for _, revision := range revisions {
		fmt.Fprintf(r.Out, "%s  %s\n", revision.CreatedAt.Local().Format(time.DateTime), strings.ReplaceAll(revision.Body, "\n", "\n    "))
	}
	return nil
}

func (r *Runner) rotate(ctx context.Context, args []string) error {
	roomID, err := roomArg(args)
	if err != nil {
		return r.fail(err.Error())
	}
	_, secret, err := r.Access.RotateFeedTokens(ctx, roomID, actor)
	if err != nil {
		return notFoundOr(err)
	}
	_, err = fmt.Fprintln(r.Out, r.FeedURL(secret))
	return err
}

func (r *Runner) disable(ctx context.Context, args []string) error {
	roomID, err := roomArg(args)
	if err != nil {
		return r.fail(err.Error())
	}
	if err := r.Feeds.Disable(ctx, roomID); err != nil {
		return notFoundOr(err)
	}
	_, err = fmt.Fprintf(r.Out, "disabled %s\n", roomID)
	return err
}

func (r *Runner) fail(message string) error {
	fmt.Fprintf(r.Err, "%s\n\n%s\n", message, usage)
	return ErrUsage
}

func roomArg(args []string) (string, error) {
	if len(args) != 1 {
		return "", errors.New("expected exactly one <room_id>")
	}
	return args[0], nil
}

func notFoundOr(err error) error {
	if errors.Is(err, domain.ErrFeedNotFound) {
		return errors.New("room has no feed")
	}
	if errors.Is(err, domain.ErrCredentialNotFound) {
		return errors.New("no key or token with this ID")
	}
	if errors.Is(err, domain.ErrMessageNotFound) {
		return errors.New("message not found")
	}
	return err
}

func formatMessage(msg domain.Message) string {
	name := msg.SenderName
	if name == "" {
		name = msg.Sender
	}
	label := ""
	if msg.Kind != "" && msg.Kind != domain.KindText {
		label = " (" + string(msg.Kind) + ")"
	}
	line := fmt.Sprintf("%s [%d] %s%s: %s", msg.SentAt.Local().Format(time.DateTime), msg.Seq, name, label, strings.ReplaceAll(msg.Body, "\n", "\n    "))
	if a := msg.Attachment; a != nil {
		line += fmt.Sprintf("\n    file: %s (%s, %d bytes)", a.Name, a.MIMEType, a.Size)
	}
	if msg.Location != "" {
		line += "\n    location: " + msg.Location
	}
	if msg.ReplyTo != "" {
		line += "\n    reply to: " + msg.ReplyTo
	}
	if msg.EditedAt != nil {
		line += "  (edited " + msg.EditedAt.Local().Format(time.DateTime) + ")"
	}
	if d := msg.Deletion; d != nil {
		line += fmt.Sprintf("  (deleted %s by %s", d.At.Local().Format(time.DateTime), d.By)
		if d.Reason != "" {
			line += ": " + d.Reason
		}
		line += ")"
	}
	return line
}
