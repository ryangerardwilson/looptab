package paths

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type Paths struct {
	ConfigDir   string
	ConfigFile  string
	StateDir    string
	ActiveDir   string
	LogDir      string
	HistoryFile string
	LockFile    string
}

func Default() (Paths, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Paths{}, err
	}

	configDir := filepath.Join(home, ".config", "looptab")
	stateRoot := os.Getenv("XDG_STATE_HOME")
	if stateRoot == "" {
		stateRoot = filepath.Join(home, ".local", "state")
	}

	stateDir := filepath.Join(stateRoot, "looptab")
	activeDir := filepath.Join(stateDir, "active")
	logDir := filepath.Join(stateDir, "logs")

	return Paths{
		ConfigDir:   configDir,
		ConfigFile:  filepath.Join(configDir, "looptab"),
		StateDir:    stateDir,
		ActiveDir:   activeDir,
		LogDir:      logDir,
		HistoryFile: filepath.Join(stateDir, "runs.jsonl"),
		LockFile:    filepath.Join(stateDir, "looptab.lock"),
	}, nil
}

func EnsureConfigFile(p Paths) error {
	if err := os.MkdirAll(p.ConfigDir, 0o700); err != nil {
		return err
	}
	if _, err := os.Stat(p.ConfigFile); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}

	return os.WriteFile(p.ConfigFile, []byte(sampleConfig()), 0o600)
}

func EnsureState(p Paths) error {
	if p.LogDir != "" {
		if err := os.MkdirAll(p.LogDir, 0o700); err != nil {
			return err
		}
	}
	if p.ActiveDir != "" {
		if err := os.MkdirAll(p.ActiveDir, 0o700); err != nil {
			return err
		}
	}
	if p.StateDir != "" {
		return os.MkdirAll(p.StateDir, 0o700)
	}
	return nil
}

func DisplayPath(path string) string {
	home, err := os.UserHomeDir()
	if err == nil {
		if path == home {
			return "~"
		}
		prefix := home + string(os.PathSeparator)
		if strings.HasPrefix(path, prefix) {
			return "~" + string(os.PathSeparator) + strings.TrimPrefix(path, prefix)
		}
	}
	return path
}

func sampleConfig() string {
	return fmt.Sprintf(`# Looptab
#
# Looptab is the source of truth for scheduled work on this machine.
# Add recurring jobs here instead of enabling per-app systemd timers.
#
# Format:
#   <when> [cwd] <action> [? on-success [: on-failure]] [&& ...]
#
# Timezone is configured in config.json beside this file.
#
# Optional cwd must be absolute or start with ~. It applies to every step in
# the line. Omit it to run from ~.
#
# Actions:
#   "<prompt>"                 Codex (default)
#   @codex "<prompt>"          Codex explicit
#   @grok "<prompt>"           Grok headless single-turn
#   <command> [args...]        direct command (PATH name or absolute path)
#
# Direct commands exec without a shell: no pipes, redirects, or && inside one
# step. Chain separate steps with && instead.
#
# Outcomes — run a follow-up based on the previous step's exit code:
#   <action> ? <on-success> [: <on-failure>]
# Each branch is a full action or a quoted body shorthand for notify.
# Quoted shorthand uses heading "looptab" and adds --urgency critical on failure.
#
# notify — Quickshell bar toast (falls back to notify-send):
#   notify heading "<title>" body "<message>"
#   notify --urgency critical heading "<title>" body "<message>"
#
# Schedules:
#   now                        run once when looptab loads
#   hourly
#   hourly at <minute>
#   every <duration>           e.g. every 30s, every 5m, every 1h
#   daily <time[,time...]>     e.g. daily 5am, daily 11am,12pm,1pm
#   weekdays <time[,time...]>
#   weekends <time[,time...]>
#   monday|mondays … sunday|sundays <time[,time...]>
#
# Times: 11am, 9:30am, 5pm, 17:15, or comma-separated lists.
#
# Examples:
#   now "Run once with Codex from home when looptab loads."
#   daily 5am @grok "Check my emails." ? notify heading "gmail" body "inbox review finished" : notify heading "gmail" body "inbox review failed"
#   hourly gdrive sync run ? notify heading "gdrive" body "backup finished" : notify heading "gdrive" body "backup failed"
#   every 30s tm snapshot sessions
`)
}
