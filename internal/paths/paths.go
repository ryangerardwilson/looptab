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
# Format:
#   timezone <IANA name>
#   <when> [cwd] "<prompt>"
#
# Examples:
#   timezone UTC
#   now "Run once from home when looptab loads."
#   daily 11am "Review from home and fix one small obvious issue."
#   daily 11am ~/Work/example "Review the repo and fix one small obvious issue."
#   daily 11am,12pm,1pm ~/Work/example "Run a quick maintenance pass."
#   weekdays 9am ~/Work/example "Plan the day and update TODOs."
#   mondays 5am ~/Work/example "Prepare the weekly review."
#
# Run:
#   looptab check
#   looptab run
timezone UTC
`)
}
