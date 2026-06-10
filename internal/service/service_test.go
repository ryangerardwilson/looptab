package service

import (
	"strings"
	"testing"
)

func TestUnitUsesStablePath(t *testing.T) {
	t.Setenv("PATH", "/tmp/codex-session:/usr/bin")

	manager := UserManager{
		servicePath: "/home/ryan/.config/systemd/user/looptab.service",
		executable:  "/home/ryan/.local/bin/looptab",
		home:        "/home/ryan",
	}

	unit := manager.unit()
	if strings.Contains(unit, "/tmp/codex-session") {
		t.Fatalf("unit captured process PATH:\n%s", unit)
	}
	if !strings.Contains(unit, `Environment="PATH=/home/ryan/.local/bin:/home/ryan/go/bin:/home/ryan/.go/bin:/usr/local/bin:/usr/bin:/bin"`) {
		t.Fatalf("unit missing stable PATH:\n%s", unit)
	}
}
