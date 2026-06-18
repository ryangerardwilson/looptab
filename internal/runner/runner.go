package runner

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/ryangerardwilson/looptab/internal/parser"
)

type Runner struct {
	CodexBin string
	GrokBin  string
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
	return Runner{}, nil
}

func FindCodexBinary() (string, error) {
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

func FindGrokBinary() (string, error) {
	if configured := os.Getenv("GROK_BIN"); configured != "" {
		info, err := os.Stat(configured)
		if err != nil {
			return "", fmt.Errorf("GROK_BIN is not usable: %w", err)
		}
		if info.IsDir() {
			return "", fmt.Errorf("GROK_BIN points to a directory: %s", configured)
		}
		return configured, nil
	}

	bin, err := exec.LookPath("grok")
	if err != nil {
		return "", errors.New("grok not found on PATH. Install Grok or set GROK_BIN")
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

	steps := job.Steps
	if len(steps) == 0 {
		steps = []parser.Step{{Kind: job.Kind, Prompt: job.Prompt, Command: job.Command}}
	}

	aggregate := Result{
		StartedAt: startedAt,
		ExitCode:  0,
	}
	var outputBuf strings.Builder
	pidReported := false

	for index, step := range steps {
		stepJob := parser.Job{
			CWD:     job.CWD,
			Kind:    step.Kind,
			Prompt:  step.Prompt,
			Command: step.Command,
		}
		var stepOnStart func(int)
		if !pidReported && onStart != nil {
			stepOnStart = func(pid int) {
				pidReported = true
				onStart(pid)
			}
		}

		stepResult := r.runStep(ctx, stepJob, aggregate.StartedAt, output, stepOnStart)
		if outputBuf.Len() > 0 && stepResult.Output != "" {
			outputBuf.WriteString("\n")
		}
		outputBuf.WriteString(stepResult.Output)
		aggregate.Output = outputBuf.String()
		aggregate.FinishedAt = stepResult.FinishedAt
		aggregate.ExitCode = stepResult.ExitCode

		if step.OnSuccess != nil || step.OnFailure != nil {
			var branch *parser.Step
			if stepResult.ExitCode == 0 && step.OnSuccess != nil {
				branch = step.OnSuccess
			} else if stepResult.ExitCode != 0 && step.OnFailure != nil {
				branch = step.OnFailure
			}
			if branch != nil {
				branchJob := parser.Job{
					CWD:     job.CWD,
					Kind:    branch.Kind,
					Prompt:  branch.Prompt,
					Command: branch.Command,
				}
				branchResult := r.runStep(ctx, branchJob, aggregate.StartedAt, output, nil)
				if outputBuf.Len() > 0 && branchResult.Output != "" {
					outputBuf.WriteString("\n")
				}
				outputBuf.WriteString(branchResult.Output)
				aggregate.Output = outputBuf.String()
				aggregate.FinishedAt = branchResult.FinishedAt
				if branchResult.Err != nil {
					aggregate.Err = fmt.Errorf("step %d outcome failed: %w", index+1, branchResult.Err)
					return aggregate
				}
			}
		}

		if stepResult.Err != nil {
			aggregate.Err = fmt.Errorf("step %d failed: %w", index+1, stepResult.Err)
			return aggregate
		}
		if stepResult.ExitCode != 0 {
			return aggregate
		}
	}

	if aggregate.FinishedAt.IsZero() {
		aggregate.FinishedAt = time.Now()
	}
	return aggregate
}

func (r Runner) runStep(ctx context.Context, job parser.Job, startedAt time.Time, output io.Writer, onStart func(int)) Result {
	result := Result{
		StartedAt: startedAt,
		ExitCode:  -1,
	}

	bin, args, label, err := r.commandForStep(job)
	if err != nil {
		result.FinishedAt = time.Now()
		result.Err = err
		return result
	}

	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = job.CWD
	cmd.Env = os.Environ()
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	capture := &captureWriter{live: output}
	cmd.Stdout = capture
	cmd.Stderr = capture

	err = cmd.Start()
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
		result.Err = fmt.Errorf("%s exited with status %d", label, result.ExitCode)
		return result
	}

	if ctx.Err() != nil {
		result.Err = ctx.Err()
		return result
	}

	result.Err = err
	return result
}

func (r *Runner) commandForStep(job parser.Job) (string, []string, string, error) {
	switch job.Kind {
	case parser.JobKindGrok:
		if job.Prompt == "" {
			return "", nil, "", errors.New("grok job is missing a prompt")
		}
		bin, err := r.grokBin()
		if err != nil {
			return "", nil, "", err
		}
		return bin, []string{"--always-approve", "--cwd", job.CWD, "-p", job.Prompt}, "grok", nil
	case parser.JobKindCommand:
		if len(job.Command) == 0 {
			return "", nil, "", errors.New("command job is missing an executable")
		}
		executable, err := resolveExecutable(job.Command[0])
		if err != nil {
			return "", nil, "", err
		}
		return executable, job.Command[1:], filepath.Base(executable), nil
	default:
		if job.Prompt == "" {
			return "", nil, "", errors.New("codex job is missing a prompt")
		}
		bin, err := r.codexBin()
		if err != nil {
			return "", nil, "", err
		}
		return bin, []string{"exec", "--color", "never", "--cd", job.CWD, job.Prompt}, "codex", nil
	}
}

func (r *Runner) codexBin() (string, error) {
	if r.CodexBin != "" {
		return r.CodexBin, nil
	}
	bin, err := FindCodexBinary()
	if err != nil {
		return "", err
	}
	r.CodexBin = bin
	return bin, nil
}

func (r *Runner) grokBin() (string, error) {
	if r.GrokBin != "" {
		return r.GrokBin, nil
	}
	bin, err := FindGrokBinary()
	if err != nil {
		return "", err
	}
	r.GrokBin = bin
	return bin, nil
}

func resolveExecutable(path string) (string, error) {
	if path == "~" || stringsHasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		if path == "~" {
			path = home
		} else {
			path = filepath.Join(home, path[2:])
		}
		return filepath.Clean(path), nil
	}
	if filepath.IsAbs(path) {
		return filepath.Clean(path), nil
	}
	bin, err := exec.LookPath(path)
	if err != nil {
		return "", fmt.Errorf("executable not found on PATH: %s", path)
	}
	return bin, nil
}

func stringsHasPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}