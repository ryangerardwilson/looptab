package editor

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/ryangerardwilson/looptab/internal/shellwords"
)

func Open(path string) error {
	editor := os.Getenv("VISUAL")
	if editor == "" {
		editor = os.Getenv("EDITOR")
	}
	if editor == "" {
		editor = "vi"
	}

	parts, err := shellwords.Fields(editor)
	if err != nil {
		return fmt.Errorf("parse editor command: %w", err)
	}
	if len(parts) == 0 {
		return fmt.Errorf("empty editor command")
	}

	args := append(parts[1:], path)
	cmd := exec.Command(parts[0], args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
