package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ryangerardwilson/looptab/internal/paths"
)

const DefaultTimezone = "UTC"

type Settings struct {
	Timezone string `json:"timezone"`
}

func SettingsPath(p paths.Paths) string {
	return filepath.Join(p.ConfigDir, "config.json")
}

func EnsureSettings(p paths.Paths) error {
	if err := os.MkdirAll(p.ConfigDir, 0o700); err != nil {
		return err
	}

	path := SettingsPath(p)
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}

	payload, err := json.MarshalIndent(Settings{Timezone: DefaultTimezone}, "", "  ")
	if err != nil {
		return err
	}
	payload = append(payload, '\n')
	return os.WriteFile(path, payload, 0o600)
}

func Load(p paths.Paths) (Settings, *time.Location, error) {
	path := SettingsPath(p)
	content, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Settings{}, nil, fmt.Errorf("config not found: %s\nrun `looptab` to create it", path)
		}
		return Settings{}, nil, err
	}

	var settings Settings
	if err := json.Unmarshal(content, &settings); err != nil {
		return Settings{}, nil, fmt.Errorf("invalid config %s: %w", path, err)
	}
	if strings.TrimSpace(settings.Timezone) == "" {
		settings.Timezone = DefaultTimezone
	}

	location, canonical, err := ResolveTimezone(settings.Timezone)
	if err != nil {
		return Settings{}, nil, fmt.Errorf("invalid timezone in %s: %w", path, err)
	}
	settings.Timezone = canonical
	return settings, location, nil
}

func ResolveTimezone(name string) (*time.Location, string, error) {
	if strings.EqualFold(strings.TrimSpace(name), "utc") {
		return time.UTC, "UTC", nil
	}
	if location, err := time.LoadLocation(name); err == nil {
		return location, location.String(), nil
	}
	candidate := canonicalTimezoneCandidate(name)
	if candidate != name {
		if location, err := time.LoadLocation(candidate); err == nil {
			return location, location.String(), nil
		}
	}
	return nil, "", fmt.Errorf("invalid timezone %q", name)
}

func canonicalTimezoneCandidate(name string) string {
	parts := strings.Split(name, "/")
	for i, part := range parts {
		pieces := strings.Split(part, "_")
		for j, piece := range pieces {
			if piece == "" {
				continue
			}
			lower := strings.ToLower(piece)
			pieces[j] = strings.ToUpper(lower[:1]) + lower[1:]
		}
		parts[i] = strings.Join(pieces, "_")
	}
	return strings.Join(parts, "/")
}

func LatestMtime(p paths.Paths) (time.Time, error) {
	latest := time.Time{}
	for _, path := range []string{SettingsPath(p), p.ConfigFile} {
		stat, err := os.Stat(path)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return time.Time{}, err
		}
		if stat.ModTime().After(latest) {
			latest = stat.ModTime()
		}
	}
	return latest, nil
}