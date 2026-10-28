package config

import (
	"strings"
	"testing"
)

func setValidEnv(t *testing.T) {
	t.Helper()
	for key, value := range map[string]string{
		"MATRIX_HOMESERVER_URL": "https://matrix.example.org",
		"MATRIX_USER_ID":        "@bot:example.org",
		"MATRIX_PASSWORD":       "secret",
		"MATRIX_PICKLE_KEY":     "0123456789abcdef",
		"MATRIX_ADMINS":         "@a:example.org,@b:example.org",
		"PUBLIC_BASE_URL":       "https://rss.example.org",
		"API_TOKEN":             "0123456789abcdef",
	} {
		t.Setenv(key, value)
	}
}

func TestLoadAppliesDefaultsAndSplitsAdmins(t *testing.T) {
	setValidEnv(t)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(cfg.Matrix.Admins) != 2 || cfg.Matrix.CommandPrefix != "!rss" || cfg.Database.Driver != "sqlite" || cfg.Matrix.RoomAdminLevel != 100 {
		t.Fatalf("unexpected config: %+v", cfg)
	}
}

func TestLoadRejectsWeakSecretsAndBadURL(t *testing.T) {
	setValidEnv(t)
	t.Setenv("API_TOKEN", "short")
	t.Setenv("PUBLIC_BASE_URL", "rss.example.org")

	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "API_TOKEN") || !strings.Contains(err.Error(), "PUBLIC_BASE_URL") {
		t.Fatalf("want both problems reported, got %v", err)
	}
}

func TestLoadRequiresAdmins(t *testing.T) {
	setValidEnv(t)
	t.Setenv("MATRIX_ADMINS", "")

	if _, err := Load(); err == nil {
		t.Fatal("expected error without admins")
	}
}

func TestRoomAdminLevel(t *testing.T) {
	setValidEnv(t)
	t.Setenv("MATRIX_ROOM_ADMIN_LEVEL", "50")
	if cfg, err := Load(); err != nil || cfg.Matrix.RoomAdminLevel != 50 {
		t.Fatalf("level 50: %+v, %v", cfg.Matrix, err)
	}
	t.Setenv("MATRIX_ROOM_ADMIN_LEVEL", "0")
	if cfg, err := Load(); err != nil || cfg.Matrix.RoomAdminLevel != 0 {
		t.Fatalf("0 turns the feature off: %+v, %v", cfg.Matrix, err)
	}
	t.Setenv("MATRIX_ROOM_ADMIN_LEVEL", "-1")
	if _, err := Load(); err == nil {
		t.Fatal("a negative level must be rejected")
	}
}
