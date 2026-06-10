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
	logDir := filepath.Join(stateDir, "logs")

	return Paths{
		ConfigDir:   configDir,
		ConfigFile:  filepath.Join(configDir, "looptab"),
		StateDir:    stateDir,
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
	if err := os.MkdirAll(p.LogDir, 0o700); err != nil {
		return err
	}
	return os.MkdirAll(p.StateDir, 0o700)
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
#   <cron> <cwd> "<prompt>"
#
# Examples:
#   0 * * * * ~/Work/example "Review the repo and fix one small obvious issue."
#   @daily ~/Work/notes "Summarize yesterday's notes and update TODOs."
#
# Run:
#   looptab check
#   looptab run
`)
}
