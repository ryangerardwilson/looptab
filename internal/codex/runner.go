package codex

import (
	"context"
	"io"
	"time"

	"github.com/ryangerardwilson/looptab/internal/parser"
	"github.com/ryangerardwilson/looptab/internal/runner"
)

type Runner struct {
	inner runner.Runner
}

type Result = runner.Result

func NewRunner() (Runner, error) {
	inner, err := runner.NewRunner()
	if err != nil {
		return Runner{}, err
	}
	return Runner{inner: inner}, nil
}

func FindBinary() (string, error) {
	return runner.FindCodexBinary()
}

func (r Runner) Run(ctx context.Context, job parser.Job) Result {
	return r.inner.Run(ctx, job)
}

func (r Runner) RunWithOutput(ctx context.Context, job parser.Job, startedAt time.Time, output io.Writer) Result {
	return r.inner.RunWithOutput(ctx, job, startedAt, output)
}

func (r Runner) RunWithOutputAndPID(ctx context.Context, job parser.Job, startedAt time.Time, output io.Writer, onStart func(int)) Result {
	return r.inner.RunWithOutputAndPID(ctx, job, startedAt, output, onStart)
}