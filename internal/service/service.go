package service

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

const serviceName = "looptab.service"

var ErrUnsupported = errors.New("looptab service management is only implemented for systemd user services on linux")

type UserManager struct {
	servicePath string
	executable  string
	home        string
}

func NewUserManager() (UserManager, error) {
	if runtime.GOOS != "linux" {
		return UserManager{}, ErrUnsupported
	}
	if _, err := exec.LookPath("systemctl"); err != nil {
		return UserManager{}, fmt.Errorf("systemctl not found")
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return UserManager{}, err
	}
	executable, err := os.Executable()
	if err != nil {
		return UserManager{}, err
	}
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil {
		return UserManager{}, err
	}

	return UserManager{
		servicePath: filepath.Join(home, ".config", "systemd", "user", serviceName),
		executable:  executable,
		home:        home,
	}, nil
}

func (m UserManager) Install() error {
	if err := m.install(os.Stdout); err != nil {
		return err
	}
	fmt.Fprintln(os.Stdout, "run `looptab service start` to start it")
	return nil
}

func (m UserManager) EnsureStarted(w io.Writer) error {
	if active, err := m.IsActive(); err != nil {
		return err
	} else if active {
		fmt.Fprintln(w, "looptab scheduler is already running.")
		return nil
	}

	installed, err := m.IsInstalled()
	if err != nil {
		return err
	}
	if !installed {
		if err := m.install(w); err != nil {
			return err
		}
	}

	if err := m.Start(); err != nil {
		return err
	}
	fmt.Fprintln(w, "started looptab scheduler.")
	return nil
}

func (m UserManager) IsInstalled() (bool, error) {
	if _, err := os.Stat(m.servicePath); err == nil {
		return true, nil
	} else if os.IsNotExist(err) {
		return false, nil
	} else {
		return false, err
	}
}

func (m UserManager) IsActive() (bool, error) {
	err := systemctlQuiet("is-active", "--quiet", serviceName)
	if err == nil {
		return true, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return false, nil
	}
	return false, err
}

func (m UserManager) install(w io.Writer) error {
	if err := os.MkdirAll(filepath.Dir(m.servicePath), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(m.servicePath, []byte(m.unit()), 0o600); err != nil {
		return err
	}
	if err := systemctl("daemon-reload"); err != nil {
		return err
	}
	if err := systemctl("enable", serviceName); err != nil {
		return err
	}
	fmt.Fprintf(w, "installed %s\n", m.servicePath)
	return nil
}

func (m UserManager) Start() error {
	return systemctl("start", serviceName)
}

func (m UserManager) Stop() error {
	return systemctl("stop", serviceName)
}

func (m UserManager) Status() error {
	return systemctl("status", serviceName, "--no-pager")
}

func (m UserManager) Remove() error {
	_ = systemctl("stop", serviceName)
	_ = systemctl("disable", serviceName)
	if err := os.Remove(m.servicePath); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := systemctl("daemon-reload"); err != nil {
		return err
	}
	fmt.Fprintf(os.Stdout, "removed %s\n", m.servicePath)
	return nil
}

func (m UserManager) unit() string {
	lines := []string{
		"[Unit]",
		"Description=Looptab Codex loop scheduler",
		"After=network-online.target",
		"",
		"[Service]",
		"Type=simple",
		fmt.Sprintf("ExecStart=%s run", systemdEscapePath(m.executable)),
		"Restart=on-failure",
		"RestartSec=10",
		"WorkingDirectory=%h",
	}
	lines = append(lines, fmt.Sprintf("Environment=%q", "PATH="+m.servicePathEnv()))
	if codexBin := os.Getenv("CODEX_BIN"); codexBin != "" {
		lines = append(lines, fmt.Sprintf("Environment=%q", "CODEX_BIN="+codexBin))
	}
	lines = append(lines,
		"",
		"[Install]",
		"WantedBy=default.target",
		"",
	)
	return strings.Join(lines, "\n")
}

func (m UserManager) servicePathEnv() string {
	return strings.Join(uniqueNonEmpty([]string{
		filepath.Dir(m.executable),
		filepath.Join(m.home, ".local", "bin"),
		filepath.Join(m.home, "go", "bin"),
		filepath.Join(m.home, ".go", "bin"),
		"/usr/local/bin",
		"/usr/bin",
		"/bin",
	}), ":")
}

func uniqueNonEmpty(values []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, value := range values {
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func systemctl(args ...string) error {
	cmdArgs := append([]string{"--user"}, args...)
	cmd := exec.Command("systemctl", cmdArgs...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	return cmd.Run()
}

func systemctlQuiet(args ...string) error {
	cmdArgs := append([]string{"--user"}, args...)
	cmd := exec.Command("systemctl", cmdArgs...)
	return cmd.Run()
}

func systemdEscapePath(path string) string {
	return strings.ReplaceAll(path, " ", `\x20`)
}
