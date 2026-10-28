// Package config loads and validates the settings from environment variables.
package config

import (
	"errors"
	"fmt"
	"net/url"
	"time"

	"github.com/caarlos0/env/v11"
)

const (
	minSecretLength = 16
)

type Config struct {
	Matrix    Matrix
	HTTP      HTTP
	Database  Database
	Retention time.Duration `env:"MESSAGE_RETENTION" envDefault:"720h"`
	// KeepDeleted keeps the text of messages that were deleted in the room; otherwise only who deleted
	// them and when is kept.
	KeepDeleted bool `env:"MESSAGE_KEEP_DELETED" envDefault:"false"`
	// Backfill lets the API fetch missing older messages from the homeserver.
	Backfill bool   `env:"HISTORY_BACKFILL" envDefault:"true"`
	LogLevel string `env:"LOG_LEVEL" envDefault:"info"`
}

type Matrix struct {
	HomeserverURL string   `env:"MATRIX_HOMESERVER_URL,required,notEmpty"`
	UserID        string   `env:"MATRIX_USER_ID,required,notEmpty"`
	Password      string   `env:"MATRIX_PASSWORD,required,notEmpty"`
	PickleKey     string   `env:"MATRIX_PICKLE_KEY,required,notEmpty"`
	Admins        []string `env:"MATRIX_ADMINS,required,notEmpty" envSeparator:","`
	CommandPrefix string   `env:"MATRIX_COMMAND_PREFIX" envDefault:"!rss"`
	// RoomAdminLevel is the power level from which members of a room may manage that room's feed
	// tokens and API keys (100 = admin, 50 = moderator). 0 leaves this to MATRIX_ADMINS.
	RoomAdminLevel int    `env:"MATRIX_ROOM_ADMIN_LEVEL" envDefault:"100"`
	DeviceName     string `env:"MATRIX_DEVICE_NAME" envDefault:"matrixrss"`
}

type HTTP struct {
	Addr          string `env:"HTTP_ADDR" envDefault:":8080"`
	PublicBaseURL string `env:"PUBLIC_BASE_URL,required,notEmpty"`
	APIToken      string `env:"API_TOKEN,required,notEmpty"`
}

type Database struct {
	Driver string `env:"DATABASE_DRIVER" envDefault:"sqlite"`
	DSN    string `env:"DATABASE_DSN" envDefault:"data/matrixrss.db"`
}

// Load reads the environment and rejects incomplete or insecure settings.
func Load() (Config, error) {
	var cfg Config
	if err := env.Parse(&cfg); err != nil {
		return Config{}, fmt.Errorf("parse environment: %w", err)
	}
	if err := cfg.validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c Config) validate() error {
	var errs []error
	if len(c.Matrix.PickleKey) < minSecretLength {
		errs = append(errs, fmt.Errorf("MATRIX_PICKLE_KEY must be at least %d characters", minSecretLength))
	}
	if len(c.HTTP.APIToken) < minSecretLength {
		errs = append(errs, fmt.Errorf("API_TOKEN must be at least %d characters", minSecretLength))
	}
	if u, err := url.Parse(c.HTTP.PublicBaseURL); err != nil || u.Scheme == "" || u.Host == "" {
		errs = append(errs, errors.New("PUBLIC_BASE_URL must be an absolute URL"))
	}
	if c.Matrix.RoomAdminLevel < 0 {
		errs = append(errs, errors.New("MATRIX_ROOM_ADMIN_LEVEL must not be negative"))
	}
	if c.Retention < 0 {
		errs = append(errs, errors.New("MESSAGE_RETENTION must not be negative"))
	}
	return errors.Join(errs...)
}

// CLI holds the settings of the management commands, which work on the database only
// and therefore need no Matrix account.
type CLI struct {
	Database      Database
	PublicBaseURL string `env:"PUBLIC_BASE_URL,required,notEmpty"`
}

func LoadCLI() (CLI, error) {
	var cfg CLI
	if err := env.Parse(&cfg); err != nil {
		return CLI{}, fmt.Errorf("parse environment: %w", err)
	}
	return cfg, nil
}
