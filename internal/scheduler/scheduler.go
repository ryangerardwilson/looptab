package scheduler

import (
	"context"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/robfig/cron/v3"
	"github.com/ryangerardwilson/looptab/internal/active"
	"github.com/ryangerardwilson/looptab/internal/codex"
	"github.com/ryangerardwilson/looptab/internal/oncejob"
	"github.com/ryangerardwilson/looptab/internal/parser"
	"github.com/ryangerardwilson/looptab/internal/paths"
	"github.com/ryangerardwilson/looptab/internal/runlog"
)

const (
	reloadPollInterval   = 250 * time.Millisecond
	reloadSettleInterval = 100 * time.Millisecond
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

	file, mtime, err := s.loadFile()
	if err != nil {
		return err
	}

	active, err := s.startCron(ctx, file, mtime, runner, store)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stdout, "looptab running: %d jobs from %s in %s\n", len(file.Jobs), s.paths.ConfigFile, file.Timezone)

	ticker := time.NewTicker(reloadPollInterval)
	defer ticker.Stop()
	defer active.Stop()
	var lastReloadErrorMTime time.Time

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
			if time.Since(stat.ModTime()) < reloadSettleInterval {
				continue
			}

			nextFile, nextMTime, err := s.loadFile()
			if err != nil {
				if !nextMTime.Equal(lastReloadErrorMTime) {
					fmt.Fprintf(os.Stderr, "looptab reload failed:\n%v\n", err)
					lastReloadErrorMTime = nextMTime
				}
				continue
			}

			active.Stop()
			active, err = s.startCron(ctx, nextFile, nextMTime, runner, store)
			if err != nil {
				if !nextMTime.Equal(lastReloadErrorMTime) {
					fmt.Fprintf(os.Stderr, "looptab reload failed: %v\n", err)
					lastReloadErrorMTime = nextMTime
				}
				continue
			}
			file = nextFile
			mtime = nextMTime
			lastReloadErrorMTime = time.Time{}
			fmt.Fprintf(os.Stdout, "looptab reloaded: %d jobs in %s\n", len(file.Jobs), file.Timezone)
		}
	}
}

func (s *Scheduler) loadFile() (parser.File, time.Time, error) {
	content, err := os.ReadFile(s.paths.ConfigFile)
	if err != nil {
		return parser.File{}, time.Time{}, err
	}
	stat, err := os.Stat(s.paths.ConfigFile)
	if err != nil {
		return parser.File{}, time.Time{}, err
	}
	file, err := parser.Parse(string(content))
	return file, stat.ModTime(), err
}

func (s *Scheduler) startCron(ctx context.Context, file parser.File, loadedAt time.Time, runner codex.Runner, store runlog.Store) (*cron.Cron, error) {
	parserSpec := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)
	c := cron.New(
		cron.WithParser(parserSpec),
		cron.WithLocation(file.Location),
		cron.WithChain(cron.Recover(cron.DefaultLogger)),
	)

	for _, job := range file.Jobs {
		localJob := job
		if localJob.Once {
			claimed, err := oncejob.NewStore(s.paths).Claim(localJob, loadedAt)
			if err != nil {
				return nil, err
			}
			if !claimed {
				continue
			}
			alreadyAttempted, err := nowJobAlreadyAttemptedForLoad(store, localJob, loadedAt)
			if err != nil {
				return nil, err
			}
			if alreadyAttempted {
				continue
			}
			go s.runJob(ctx, localJob, runner, store)
			continue
		}
		for _, spec := range localJob.CronSpecs {
			if _, err := c.AddFunc(spec, func() {
				s.runJob(ctx, localJob, runner, store)
			}); err != nil {
				return nil, err
			}
		}
	}

	c.Start()
	return c, nil
}

func nowJobAlreadyAttemptedForLoad(store runlog.Store, job parser.Job, loadedAt time.Time) (bool, error) {
	records, err := store.Records()
	if err != nil {
		return false, err
	}
	for _, record := range records {
		if record.JobID == job.ID && record.Status != "skipped" && !record.StartedAt.Before(loadedAt) {
			return true, nil
		}
	}
	return false, nil
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

	handle, err := active.NewStore(s.paths).Begin(job)
	if err != nil {
		fmt.Fprintf(os.Stderr, "looptab active status failed: %v\n", err)
	} else {
		defer handle.End()
	}

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

	var liveWriter io.Writer
	var liveOutput *os.File
	if handle != nil && handle.OutputPath() != "" {
		liveOutput, err = os.OpenFile(handle.OutputPath(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			fmt.Fprintf(os.Stderr, "looptab live output failed: %v\n", err)
		} else {
			liveWriter = liveOutput
		}
	}

	result := runner.RunWithOutputAndPID(ctx, job, handle.StartedAt(), liveWriter, func(pid int) {
		if err := handle.SetPID(pid); err != nil {
			fmt.Fprintf(os.Stderr, "looptab active pid update failed: %v\n", err)
		}
	})
	if liveOutput != nil {
		_ = liveOutput.Close()
	}
	record, err := runlog.RecordFromResult(job, result)
	if err != nil {
		fmt.Fprintf(os.Stderr, "looptab log failed: %v\n", err)
		return
	}
	if handle != nil {
		record.OutputPath = handle.OutputPath()
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
