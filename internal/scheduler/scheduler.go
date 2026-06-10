package scheduler

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/robfig/cron/v3"
	"github.com/ryangerardwilson/looptab/internal/codex"
	"github.com/ryangerardwilson/looptab/internal/parser"
	"github.com/ryangerardwilson/looptab/internal/paths"
	"github.com/ryangerardwilson/looptab/internal/runlog"
)

type Scheduler struct {
	paths   paths.Paths
	running map[string]bool
	mu      sync.Mutex
}

func New(p paths.Paths) *Scheduler {
	return &Scheduler{
		paths:   p,
		running: make(map[string]bool),
	}
}

func (s *Scheduler) Run(ctx context.Context) error {
	runner, err := codex.NewRunner()
	if err != nil {
		return err
	}
	store := runlog.NewStore(s.paths)

	jobs, mtime, err := s.loadJobs()
	if err != nil {
		return err
	}

	active, err := s.startCron(ctx, jobs, runner, store)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stdout, "looptab running: %d jobs from %s\n", len(jobs), s.paths.ConfigFile)

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	defer active.Stop()

	for {
		select {
		case <-ctx.Done():
			fmt.Fprintln(os.Stdout, "looptab stopped")
			return nil
		case <-ticker.C:
			stat, err := os.Stat(s.paths.ConfigFile)
			if err != nil {
				fmt.Fprintf(os.Stderr, "looptab reload skipped: %v\n", err)
				continue
			}
			if !stat.ModTime().After(mtime) {
				continue
			}

			nextJobs, nextMTime, err := s.loadJobs()
			if err != nil {
				fmt.Fprintf(os.Stderr, "looptab reload failed:\n%v\n", err)
				continue
			}

			active.Stop()
			active, err = s.startCron(ctx, nextJobs, runner, store)
			if err != nil {
				fmt.Fprintf(os.Stderr, "looptab reload failed: %v\n", err)
				continue
			}
			jobs = nextJobs
			mtime = nextMTime
			fmt.Fprintf(os.Stdout, "looptab reloaded: %d jobs\n", len(jobs))
		}
	}
}

func (s *Scheduler) loadJobs() ([]parser.Job, time.Time, error) {
	content, err := os.ReadFile(s.paths.ConfigFile)
	if err != nil {
		return nil, time.Time{}, err
	}
	stat, err := os.Stat(s.paths.ConfigFile)
	if err != nil {
		return nil, time.Time{}, err
	}
	jobs, err := parser.ParseFile(string(content))
	return jobs, stat.ModTime(), err
}

func (s *Scheduler) startCron(ctx context.Context, jobs []parser.Job, runner codex.Runner, store runlog.Store) (*cron.Cron, error) {
	parserSpec := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)
	c := cron.New(
		cron.WithParser(parserSpec),
		cron.WithLocation(time.Local),
		cron.WithChain(cron.Recover(cron.DefaultLogger)),
	)

	for _, job := range jobs {
		localJob := job
		if _, err := c.AddFunc(localJob.Schedule, func() {
			s.runJob(ctx, localJob, runner, store)
		}); err != nil {
			return nil, err
		}
	}

	c.Start()
	return c, nil
}

func (s *Scheduler) runJob(ctx context.Context, job parser.Job, runner codex.Runner, store runlog.Store) {
	if !s.tryStart(job.ID) {
		record := runlog.SkippedRecord(job, "previous run still active")
		if err := store.Save(record, ""); err != nil {
			fmt.Fprintf(os.Stderr, "looptab log failed: %v\n", err)
		}
		return
	}
	defer s.finish(job.ID)

	info, err := os.Stat(job.CWD)
	if err != nil {
		record := runlog.FailedRecord(job, fmt.Sprintf("cwd does not exist: %s", job.CWD))
		if err := store.Save(record, ""); err != nil {
			fmt.Fprintf(os.Stderr, "looptab log failed: %v\n", err)
		}
		return
	}
	if !info.IsDir() {
		record := runlog.FailedRecord(job, fmt.Sprintf("cwd is not a directory: %s", job.CWD))
		if err := store.Save(record, ""); err != nil {
			fmt.Fprintf(os.Stderr, "looptab log failed: %v\n", err)
		}
		return
	}

	result := runner.Run(ctx, job)
	record, err := runlog.RecordFromResult(job, result)
	if err != nil {
		fmt.Fprintf(os.Stderr, "looptab log failed: %v\n", err)
		return
	}
	if err := store.Save(record, result.Output); err != nil {
		fmt.Fprintf(os.Stderr, "looptab log failed: %v\n", err)
	}
}

func (s *Scheduler) tryStart(jobID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running[jobID] {
		return false
	}
	s.running[jobID] = true
	return true
}

func (s *Scheduler) finish(jobID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.running, jobID)
}
