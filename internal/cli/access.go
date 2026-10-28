package cli

import (
	"context"
	"flag"
	"fmt"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/greatgoodtiver/matrixrss/internal/domain"
)

const dateFormat = "2006-01-02"

func (r *Runner) token(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return r.fail("missing token command")
	}
	rest := args[1:]
	switch args[0] {
	case "create":
		if len(rest) != 2 {
			return r.fail("expected <room_id> <name>")
		}
		token, secret, err := r.Access.IssueFeedToken(ctx, rest[0], rest[1], actor)
		if err != nil {
			return notFoundOr(err)
		}
		fmt.Fprintf(r.Err, "token #%d %q created; the URL is shown only now\n", token.ID, token.Name)
		_, err = fmt.Fprintln(r.Out, r.FeedURL(secret))
		return err
	case "list":
		if len(rest) != 1 {
			return r.fail("expected <room_id>")
		}
		tokens, err := r.Access.ListFeedTokens(ctx, rest[0])
		if err != nil {
			return notFoundOr(err)
		}
		table := tabwriter.NewWriter(r.Out, 0, 4, 2, ' ', 0)
		fmt.Fprintln(table, "ID\tNAME\tHINT\tCREATED\tBY\tREVOKED")
		for _, token := range tokens {
			fmt.Fprintf(table, "%d\t%s\t%s…\t%s\t%s\t%s\n",
				token.ID, token.Name, token.Hint, token.CreatedAt.Format(dateFormat), token.CreatedBy, dateOrDash(token.RevokedAt))
		}
		return table.Flush()
	case "revoke":
		return r.revoke(ctx, rest, r.Access.RevokeFeedToken, "feed token")
	}
	return r.fail("unknown token command " + args[0])
}

func (r *Runner) key(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return r.fail("missing key command")
	}
	rest := args[1:]
	switch args[0] {
	case "create":
		return r.createKey(ctx, rest)
	case "list":
		keys, err := r.Access.ListAPIKeys(ctx)
		if err != nil {
			return err
		}
		table := tabwriter.NewWriter(r.Out, 0, 4, 2, ' ', 0)
		fmt.Fprintln(table, "ID\tNAME\tHINT\tROOMS\tEXPIRES\tCREATED\tBY\tREVOKED")
		for _, key := range keys {
			rooms := "all"
			if len(key.Rooms) > 0 {
				rooms = strings.Join(key.Rooms, ",")
			}
			fmt.Fprintf(table, "%d\t%s\t%s…\t%s\t%s\t%s\t%s\t%s\n", key.ID, key.Name, key.Hint, rooms,
				dateOrDash(key.ExpiresAt), key.CreatedAt.Format(dateFormat), key.CreatedBy, dateOrDash(key.RevokedAt))
		}
		return table.Flush()
	case "revoke":
		return r.revoke(ctx, rest, r.Access.RevokeAPIKey, "API key")
	}
	return r.fail("unknown key command " + args[0])
}

func (r *Runner) createKey(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("key create", flag.ContinueOnError)
	flags.SetOutput(r.Err)
	rooms := flags.String("rooms", "", "comma-separated room IDs the key may read (default: all)")
	expires := flags.String("expires", "", "validity such as 30d or 720h (default: never)")
	if err := flags.Parse(args); err != nil {
		return ErrUsage
	}
	if flags.NArg() != 1 {
		return r.fail("expected exactly one <name>")
	}

	req := domain.KeyRequest{Name: flags.Arg(0), CreatedBy: actor}
	if *rooms != "" {
		req.Rooms = strings.Split(*rooms, ",")
	}
	if *expires != "" {
		lifetime, err := domain.ParseLifetime(*expires)
		if err != nil {
			return r.fail(err.Error())
		}
		expiresAt := time.Now().Add(lifetime).UTC()
		req.ExpiresAt = &expiresAt
	}

	key, secret, err := r.Access.CreateAPIKey(ctx, req)
	if err != nil {
		return err
	}
	fmt.Fprintf(r.Err, "key #%d %q created; the secret is shown only now\n", key.ID, key.Name)
	_, err = fmt.Fprintln(r.Out, secret)
	return err
}

func (r *Runner) revoke(ctx context.Context, args []string, revoke func(context.Context, uint64) error, what string) error {
	if len(args) != 1 {
		return r.fail("expected exactly one <id>")
	}
	id, err := strconv.ParseUint(args[0], 10, 64)
	if err != nil {
		return r.fail("the id must be a number")
	}
	if err := revoke(ctx, id); err != nil {
		return notFoundOr(err)
	}
	_, err = fmt.Fprintf(r.Out, "%s #%d revoked\n", what, id)
	return err
}

func dateOrDash(at *time.Time) string {
	if at == nil {
		return "-"
	}
	return at.Format(dateFormat)
}
