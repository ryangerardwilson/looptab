package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ryangerardwilson/looptab/internal/paths"
)

func TestLoadSettingsResolvesTimezone(t *testing.T) {
	dir := t.TempDir()
	p := paths.Paths{
		ConfigDir:  dir,
		ConfigFile: filepath.Join(dir, "looptab"),
	}
	if err := os.WriteFile(SettingsPath(p), []byte(`{"timezone":"asia/kolkata"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	settings, location, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if settings.Timezone != "Asia/Kolkata" {
		t.Fatalf("unexpected timezone: %s", settings.Timezone)
	}
	if location.String() != "Asia/Kolkata" {
		t.Fatalf("unexpected location: %s", location)
	}
}

func TestEnsureSettingsCreatesDefaultConfig(t *testing.T) {
	dir := t.TempDir()
	p := paths.Paths{ConfigDir: dir, ConfigFile: filepath.Join(dir, "looptab")}
	if err := EnsureSettings(p); err != nil {
		t.Fatal(err)
	}
	settings, _, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if settings.Timezone != "UTC" {
		t.Fatalf("expected UTC default, got %s", settings.Timezone)
	}
}