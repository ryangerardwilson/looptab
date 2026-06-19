package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"

	"github.com/ryangerardwilson/looptab/internal/active"
	"github.com/ryangerardwilson/looptab/internal/parser"
	"github.com/ryangerardwilson/looptab/internal/paths"
	"github.com/ryangerardwilson/looptab/internal/runlog"
	"github.com/ryangerardwilson/looptab/internal/runner"
)

func runManualJobOnce(p paths.Paths, file parser.File, job parser.Job) error {
	if shouldLaunchInteractiveAgent(job, isTTY(os.Stdin), isTTY(os.Stdout), isTTY(os.Stderr)) {
		return runInteractiveAgentJob(p, file, job)
	}
	return runJobOnce(p, file, job)
}

func shouldLaunchInteractiveAgent(job parser.Job, stdinTTY bool, stdoutTTY bool, stderrTTY bool) bool {
	if !stdinTTY || !stdoutTTY || !stderrTTY {
		return false
	}
	_, ok := firstInteractiveAgentStep(job)
	return ok
}

func firstInteractiveAgentStep(job parser.Job) (parser.Step, bool) {
	if len(job.Steps) == 0 {
		if isAIKind(job.Kind) && job.Prompt != "" {
			return parser.Step{Kind: job.Kind, Prompt: job.Prompt, Command: job.Command}, true
		}
		return parser.Step{}, false
	}
	step := job.Steps[0]
	if isAIKind(step.Kind) && step.Prompt != "" {
		return step, true
	}
	return parser.Step{}, false
}

func isAIKind(kind parser.JobKind) bool {
	return kind == parser.JobKindCodex || kind == parser.JobKindGrok
}

func runInteractiveAgentJob(p paths.Paths, file parser.File, job parser.Job) error {
	bin, args, label, err := interactiveAgentCommand(job)
	if err != nil {
		return err
	}

	store := runlog.NewStore(p).WithLocation(file.Location)
	activeStore := active.NewStore(p)
	fmt.Fprintf(os.Stdout, "launching %s TUI for job %s from %s\n", label, job.ID, paths.DisplayPath(job.CWD))
	handle, err := activeStore.Begin(job)
	if err != nil {
		fmt.Fprintf(os.Stderr, "looptab active status failed: %v\n", err)
	} else {
		defer handle.End()
	}

	startedAt := time.Now()
	if handle != nil && !handle.StartedAt().IsZero() {
		startedAt = handle.StartedAt()
	}

	result := runner.Result{
		StartedAt: startedAt,
		ExitCode:  -1,
		Output:    fmt.Sprintf("%s TUI session launched.\n", label),
	}

	cmd := exec.Command(bin, args...)
	cmd.Dir = job.CWD
	cmd.Env = os.Environ()
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		result.FinishedAt = time.Now()
		result.Err = err
		if saveErr := saveInteractiveAgentResult(store, handle, job, result); saveErr != nil {
			return saveErr
		}
		return err
	}
	if handle != nil {
		if err := handle.SetPID(cmd.Process.Pid); err != nil {
			fmt.Fprintf(os.Stderr, "looptab active pid update failed: %v\n", err)
		}
	}

	waitErr := waitForInteractiveAgent(cmd)
	result.FinishedAt = time.Now()
	if waitErr == nil {
		result.ExitCode = 0
		result.Output += fmt.Sprintf("%s TUI session exited successfully.\n", label)
	} else {
		var exitErr *exec.ExitError
		if errors.As(waitErr, &exitErr) {
			result.ExitCode = exitErr.ExitCode()
			result.Err = fmt.Errorf("%s exited with status %d", label, result.ExitCode)
		} else {
			result.Err = waitErr
		}
	}

	automationResult, automationRan := runPostInteractiveAutomation(job, result.ExitCode, os.Stdout)
	if automationRan && automationResult.Output != "" {
		result.Output += automationResult.Output
	}
	if automationRan && automationResult.Err != nil {
		if result.Err != nil {
			result.Err = fmt.Errorf("%v; post-agent automation failed: %w", result.Err, automationResult.Err)
		} else {
			result.ExitCode = automationResult.ExitCode
			result.Err = fmt.Errorf("post-agent automation failed: %w", automationResult.Err)
		}
	}

	if err := saveInteractiveAgentResult(store, handle, job, result); err != nil {
		return err
	}
	if result.Err != nil {
		return result.Err
	}
	fmt.Fprintf(os.Stdout, "%s: %s\n", "ok", runlog.Summarize(result.Output, ""))
	return nil
}

func interactiveAgentCommand(job parser.Job) (string, []string, string, error) {
	step, ok := firstInteractiveAgentStep(job)
	if !ok {
		return "", nil, "", fmt.Errorf("interactive TUI is only supported when the first step is @codex or @grok")
	}

	switch step.Kind {
	case parser.JobKindGrok:
		bin, err := runner.FindGrokBinary()
		if err != nil {
			return "", nil, "", err
		}
		return bin, []string{"--always-approve", "--cwd", job.CWD, step.Prompt}, "grok", nil
	case parser.JobKindCodex:
		bin, err := runner.FindCodexBinary()
		if err != nil {
			return "", nil, "", err
		}
		return bin, []string{"--cd", job.CWD, step.Prompt}, "codex", nil
	default:
		return "", nil, "", fmt.Errorf("interactive TUI is only supported for @codex and @grok jobs")
	}
}

func runPostInteractiveAutomation(job parser.Job, firstExitCode int, output *os.File) (runner.Result, bool) {
	if len(job.Steps) == 0 {
		return runner.Result{}, false
	}

	first := job.Steps[0]
	steps := make([]parser.Step, 0, len(job.Steps))
	if firstExitCode == 0 {
		if first.OnSuccess != nil {
			steps = append(steps, *first.OnSuccess)
		}
		steps = append(steps, job.Steps[1:]...)
	} else if first.OnFailure != nil {
		steps = append(steps, *first.OnFailure)
	}
	if len(steps) == 0 {
		return runner.Result{}, false
	}

	jobRunner, err := runner.NewRunner()
	if err != nil {
		return runner.Result{
			StartedAt:  time.Now(),
			FinishedAt: time.Now(),
			ExitCode:   -1,
			Err:        err,
		}, true
	}
	postJob := parser.Job{
		CWD:   job.CWD,
		Steps: steps,
	}
	return jobRunner.RunWithOutput(context.Background(), postJob, time.Now(), output), true
}

func waitForInteractiveAgent(cmd *exec.Cmd) error {
	signals := make(chan os.Signal, 2)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)

	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	select {
	case err := <-done:
		return err
	case sig := <-signals:
		if cmd.Process != nil {
			_ = cmd.Process.Signal(sig)
		}
		return <-done
	}
}

func saveInteractiveAgentResult(store runlog.Store, handle *active.Handle, job parser.Job, result runner.Result) error {
	record, outputErr := runlog.RecordFromResult(job, result)
	if outputErr != nil {
		return outputErr
	}
	if handle != nil {
		record.OutputPath = handle.OutputPath()
	}
	return store.Save(record, result.Output)
}
