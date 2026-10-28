package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/joho/godotenv"
	"github.com/rs/zerolog"

	"github.com/greatgoodtiver/matrixrss/internal/app"
	"github.com/greatgoodtiver/matrixrss/internal/cli"
	"github.com/greatgoodtiver/matrixrss/internal/config"
)

const exitUsage = 2

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// A missing .env is fine: in containers the variables come from the environment.
	_ = godotenv.Load()

	err := run(ctx, os.Args[1:])
	switch {
	case err == nil:
	case errors.Is(err, cli.ErrUsage):
		os.Exit(exitUsage)
	default:
		slog.Error("fatal", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	if !cli.IsServe(args) {
		cfg, err := loadCLIConfig(args)
		if err != nil {
			return err
		}
		return app.RunCLI(ctx, cfg, args, os.Stdout, os.Stderr)
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	level := parseLevel(cfg.LogLevel)
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level.slog}))
	matrixLog := zerolog.New(os.Stdout).With().Timestamp().Str("component", "matrix").Logger().Level(level.zerolog)
	return app.Run(ctx, cfg, log, matrixLog)
}

// loadCLIConfig skips the environment for help, which must work without any configuration.
func loadCLIConfig(args []string) (config.CLI, error) {
	if cli.IsHelp(args) {
		return config.CLI{}, nil
	}
	return config.LoadCLI()
}

type logLevel struct {
	slog    slog.Level
	zerolog zerolog.Level
}

func parseLevel(name string) logLevel {
	switch name {
	case "debug":
		return logLevel{slog.LevelDebug, zerolog.DebugLevel}
	case "warn":
		return logLevel{slog.LevelWarn, zerolog.WarnLevel}
	case "error":
		return logLevel{slog.LevelError, zerolog.ErrorLevel}
	default:
		return logLevel{slog.LevelInfo, zerolog.InfoLevel}
	}
}
