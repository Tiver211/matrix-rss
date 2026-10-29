// Package app wires the layers together and runs the service.
package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/rs/zerolog"
	"golang.org/x/sync/errgroup"

	"github.com/greatgoodtiver/matrixrss/internal/adapter/httpapi"
	"github.com/greatgoodtiver/matrixrss/internal/adapter/matrix"
	"github.com/greatgoodtiver/matrixrss/internal/adapter/storage/gormstore"
	"github.com/greatgoodtiver/matrixrss/internal/adapter/token"
	"github.com/greatgoodtiver/matrixrss/internal/cli"
	"github.com/greatgoodtiver/matrixrss/internal/config"
	"github.com/greatgoodtiver/matrixrss/internal/usecase"
)

const (
	retentionInterval = time.Hour
	shutdownTimeout   = 10 * time.Second
)

// Run starts the bot and the HTTP server and blocks until the context is cancelled or one of them fails.
func Run(ctx context.Context, cfg config.Config, log *slog.Logger, matrixLog zerolog.Logger) error {
	store, err := gormstore.Open(cfg.Database.Driver, cfg.Database.DSN)
	if err != nil {
		return err
	}
	defer store.Close()

	feeds := usecase.NewFeedService(store.Feeds())
	access := usecase.NewAccessService(store.APIKeys(), store.FeedTokens(), store.Feeds(), token.Generator{})
	messages := usecase.NewMessageService(store.Feeds(), store.Messages(), usecase.MessageOptions{KeepDeletedContent: cfg.KeepDeleted})
	retention := usecase.NewRetentionService(store.Messages(), cfg.Retention)

	bot, err := matrix.New(ctx, matrix.Config{
		Credentials: matrix.Credentials{
			HomeserverURL: cfg.Matrix.HomeserverURL,
			UserID:        cfg.Matrix.UserID,
			Password:      cfg.Matrix.Password,
			DeviceName:    cfg.Matrix.DeviceName,
			PickleKey:     cfg.Matrix.PickleKey,
		},
		Database:       matrix.Database{Conn: store.SQLDB(), Dialect: store.SQLDialect()},
		Admins:         cfg.Matrix.Admins,
		RoomAdminLevel: cfg.Matrix.RoomAdminLevel,
		CommandPrefix:  cfg.Matrix.CommandPrefix,
		FeedURL:        httpapi.FeedURL(cfg.HTTP.PublicBaseURL),
		Feeds:          feeds,
		FeedList:       feeds,
		Access:         access,
		Recorder:       messages,
		Log:            matrixLog,
	})
	if err != nil {
		return fmt.Errorf("start matrix bot: %w", err)
	}

	var source usecase.HistorySource
	if cfg.Backfill {
		source = bot.History()
	}
	history := usecase.NewHistoryService(store.Feeds(), store.Messages(), messages, source, log)

	media := usecase.NewMediaService(store.Messages(), bot.Media(), cfg.MediaMaxSize)

	handler := httpapi.New(httpapi.Deps{Feeds: feeds, History: history, Messages: messages, Media: media, Access: access}, httpapi.Config{
		RootToken:     cfg.HTTP.APIToken,
		PublicBaseURL: cfg.HTTP.PublicBaseURL,
	}, log)
	server := httpapi.NewServer(cfg.HTTP.Addr, handler)

	group, ctx := errgroup.WithContext(ctx)
	group.Go(func() error { return bot.Run(ctx) })
	group.Go(func() error { return serve(ctx, server, log) })
	group.Go(func() error { return purgePeriodically(ctx, retention, log) })
	return group.Wait()
}

// RunCLI executes a management command against the database without connecting to Matrix.
func RunCLI(ctx context.Context, cfg config.CLI, args []string, out, errOut io.Writer) error {
	runner := &cli.Runner{Out: out, Err: errOut}
	if cli.IsHelp(args) {
		return runner.Run(ctx, args)
	}

	store, err := gormstore.Open(cfg.Database.Driver, cfg.Database.DSN)
	if err != nil {
		return err
	}
	defer store.Close()

	runner.Feeds = usecase.NewFeedService(store.Feeds())
	runner.Access = usecase.NewAccessService(store.APIKeys(), store.FeedTokens(), store.Feeds(), token.Generator{})
	runner.Messages = usecase.NewMessageService(store.Feeds(), store.Messages(), usecase.MessageOptions{})
	runner.FeedURL = httpapi.FeedURL(cfg.PublicBaseURL)
	return runner.Run(ctx, args)
}

func serve(ctx context.Context, server *http.Server, log *slog.Logger) error {
	errCh := make(chan error, 1)
	go func() { errCh <- server.ListenAndServe() }()
	log.Info("http server listening", "addr", server.Addr)

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	}
}

func purgePeriodically(ctx context.Context, retention *usecase.RetentionService, log *slog.Logger) error {
	if !retention.Enabled() {
		return nil
	}
	ticker := time.NewTicker(retentionInterval)
	defer ticker.Stop()
	for {
		removed, err := retention.Purge(ctx)
		if err != nil {
			log.Error("purge expired messages", "error", err)
		} else if removed > 0 {
			log.Info("purged expired messages", "count", removed)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}
