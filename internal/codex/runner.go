package codex

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/ryangerardwilson/looptab/internal/parser"
)

type Runner struct {
	Bin string
}

type Result struct {
	StartedAt  time.Time
	FinishedAt time.Time
	ExitCode   int
	Output     string
	Err        error
}

func NewRunner() (Runner, error) {
	bin, err := FindBinary()
	if err != nil {
		return Runner{}, err
	}
	return Runner{Bin: bin}, nil
}

func FindBinary() (string, error) {
	if configured := os.Getenv("CODEX_BIN"); configured != "" {
		info, err := os.Stat(configured)
		if err != nil {
			return "", fmt.Errorf("CODEX_BIN is not usable: %w", err)
		}
		if info.IsDir() {
			return "", fmt.Errorf("CODEX_BIN points to a directory: %s", configured)
		}
		return configured, nil
	}

	bin, err := exec.LookPath("codex")
	if err != nil {
		return "", errors.New("codex not found on PATH. Install Codex or set CODEX_BIN")
	}
	return bin, nil
}

func (r Runner) Run(ctx context.Context, job parser.Job) Result {
	result := Result{
		StartedAt: time.Now(),
		ExitCode:  -1,
	}

	cmd := exec.CommandContext(ctx, r.Bin, "exec", "--color", "never", "--cd", job.CWD, job.Prompt)
	cmd.Dir = job.CWD
	cmd.Env = os.Environ()

	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output

	err := cmd.Run()
	result.FinishedAt = time.Now()
	result.Output = output.String()

	if err == nil {
		result.ExitCode = 0
		return result
	}

	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		result.ExitCode = exitErr.ExitCode()
		result.Err = fmt.Errorf("codex exited with status %d", result.ExitCode)
		return result
	}

	if ctx.Err() != nil {
		result.Err = ctx.Err()
		return result
	}

	result.Err = err
	return result
}
