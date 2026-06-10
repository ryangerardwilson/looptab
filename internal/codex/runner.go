package codex

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"syscall"
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

type captureWriter struct {
	mu     sync.Mutex
	buffer bytes.Buffer
	live   io.Writer
}

func (w *captureWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.buffer.Write(p)
	if w.live == nil {
		return len(p), nil
	}
	if _, err := w.live.Write(p); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (w *captureWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buffer.String()
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
	return r.RunWithOutput(ctx, job, time.Time{}, nil)
}

func (r Runner) RunWithOutput(ctx context.Context, job parser.Job, startedAt time.Time, output io.Writer) Result {
	return r.RunWithOutputAndPID(ctx, job, startedAt, output, nil)
}

func (r Runner) RunWithOutputAndPID(ctx context.Context, job parser.Job, startedAt time.Time, output io.Writer, onStart func(int)) Result {
	if startedAt.IsZero() {
		startedAt = time.Now()
	}
	result := Result{
		StartedAt: startedAt,
		ExitCode:  -1,
	}

	cmd := exec.CommandContext(ctx, r.Bin, "exec", "--color", "never", "--cd", job.CWD, job.Prompt)
	cmd.Dir = job.CWD
	cmd.Env = os.Environ()
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	capture := &captureWriter{live: output}
	cmd.Stdout = capture
	cmd.Stderr = capture

	err := cmd.Start()
	if err != nil {
		result.FinishedAt = time.Now()
		result.Output = capture.String()
		if ctx.Err() != nil {
			result.Err = ctx.Err()
		} else {
			result.Err = err
		}
		return result
	}
	if onStart != nil {
		onStart(cmd.Process.Pid)
	}

	err = cmd.Wait()
	result.FinishedAt = time.Now()
	result.Output = capture.String()

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
