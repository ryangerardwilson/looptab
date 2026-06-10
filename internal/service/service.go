package service

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

const serviceName = "looptab.service"

type UserManager struct {
	servicePath string
	executable  string
}

func NewUserManager() (UserManager, error) {
	if runtime.GOOS != "linux" {
		return UserManager{}, fmt.Errorf("service management is only implemented for systemd user services on linux")
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
	}, nil
}

func (m UserManager) Install() error {
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
	fmt.Fprintf(os.Stdout, "installed %s\n", m.servicePath)
	fmt.Fprintln(os.Stdout, "run `looptab service start` to start it")
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
	if path := os.Getenv("PATH"); path != "" {
		lines = append(lines, fmt.Sprintf("Environment=%q", "PATH="+path))
	}
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

func systemctl(args ...string) error {
	cmdArgs := append([]string{"--user"}, args...)
	cmd := exec.Command("systemctl", cmdArgs...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	return cmd.Run()
}

func systemdEscapePath(path string) string {
	return strings.ReplaceAll(path, " ", `\x20`)
}
