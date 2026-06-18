package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/ryangerardwilson/looptab/internal/active"
	"github.com/ryangerardwilson/looptab/internal/runner"
	"github.com/ryangerardwilson/looptab/internal/editor"
	"github.com/ryangerardwilson/looptab/internal/lock"
	"github.com/ryangerardwilson/looptab/internal/config"
	"github.com/ryangerardwilson/looptab/internal/loader"
	"github.com/ryangerardwilson/looptab/internal/parser"
	"github.com/ryangerardwilson/looptab/internal/paths"
	"github.com/ryangerardwilson/looptab/internal/runlog"
	"github.com/ryangerardwilson/looptab/internal/scheduler"
	"github.com/ryangerardwilson/looptab/internal/service"
)

func Run(args []string, version string) error {
	p, err := paths.Default()
	if err != nil {
		return err
	}

	if len(args) == 0 {
		return openEditor(p)
	}

	switch args[0] {
	case "help":
		printHelp(os.Stdout)
		return nil
	case "version":
		fmt.Fprintln(os.Stdout, version)
		return nil
	case "check":
		return runCheck(p, os.Stdout)
	case "now":
		return runInteractiveNow(p)
	case "run":
		return runCommand(p, args[1:])
	case "inspect":
		return inspectCommand(p, args[1:])
	case "stream":
		return streamCommand(p, args[1:])
	case "kill":
		return killCommand(p, args[1:])
	case "status":
		return statusCommand(p, args[1:])
	case "service":
		return serviceCommand(args[1:])
	default:
		return fmt.Errorf("unknown command %q\n\nrun `looptab help`", args[0])
	}
}

func runCommand(p paths.Paths, args []string) error {
	if len(args) == 0 {
		if err := ensureLayout(p); err != nil {
			return err
		}
		heldLock, err := lock.Acquire(p.LockFile)
		if err != nil {
			return err
		}
		defer heldLock.Release()

		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()

		return scheduler.New(p).Run(ctx)
	}

	if len(args) == 1 && args[0] == "now" {
		return runNowJobs(p)
	}

	if len(args) == 2 && args[0] == "job" {
		return runOneJob(p, args[1])
	}

	return errors.New("expected `looptab run`, `looptab run now`, or `looptab run job <id>`")
}

func runOneJob(p paths.Paths, id string) error {
	file, err := loadFile(p)
	if err != nil {
		return err
	}

	job, err := parser.FindJob(file.Jobs, id)
	if err != nil {
		return err
	}

	return runJobOnce(p, file, job)
}

func runNowJobs(p paths.Paths) error {
	file, err := loadFile(p)
	if err != nil {
		return err
	}

	jobs := nowJobs(file)
	if len(jobs) == 0 {
		fmt.Fprintln(os.Stdout, "No now jobs are in the looptab file.")
		return nil
	}

	var failures []string
	for _, job := range jobs {
		if err := runJobOnce(p, file, job); err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", job.ID, err))
		}
	}
	if len(failures) > 0 {
		return errors.New(strings.Join(failures, "\n"))
	}
	return nil
}

func runJobOnce(p paths.Paths, file parser.File, job parser.Job) error {
	jobRunner, err := runner.NewRunner()
	if err != nil {
		return err
	}

	store := runlog.NewStore(p).WithLocation(file.Location)
	activeStore := active.NewStore(p)
	fmt.Fprintf(os.Stdout, "running job %s (%s) from %s\n", job.ID, job.Kind, paths.DisplayPath(job.CWD))
	handle, err := activeStore.Begin(job)
	if err != nil {
		fmt.Fprintf(os.Stderr, "looptab active status failed: %v\n", err)
	} else {
		defer handle.End()
	}

	var liveWriter io.Writer = os.Stdout
	var liveOutput *os.File
	if handle != nil && handle.OutputPath() != "" {
		liveOutput, err = os.OpenFile(handle.OutputPath(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			fmt.Fprintf(os.Stderr, "looptab live output failed: %v\n", err)
		} else {
			liveWriter = io.MultiWriter(os.Stdout, liveOutput)
		}
	}

	result := jobRunner.RunWithOutputAndPID(context.Background(), job, handle.StartedAt(), liveWriter, func(pid int) {
		if err := handle.SetPID(pid); err != nil {
			fmt.Fprintf(os.Stderr, "looptab active pid update failed: %v\n", err)
		}
	})
	if liveOutput != nil {
		_ = liveOutput.Close()
	}
	record, outputErr := runlog.RecordFromResult(job, result)
	if outputErr != nil {
		return outputErr
	}
	if handle != nil {
		record.OutputPath = handle.OutputPath()
	}
	if err := store.Save(record, result.Output); err != nil {
		return err
	}
	fmt.Fprintf(os.Stdout, "%s: %s\n", record.Status, record.Summary)
	if result.Err != nil {
		return result.Err
	}
	return nil
}

func statusCommand(p paths.Paths, args []string) error {
	store := active.NewStore(p)
	if len(args) == 0 {
		return store.Print(os.Stdout)
	}
	if len(args) == 1 && args[0] == "json" {
		return store.PrintJSON(os.Stdout)
	}
	if len(args) == 1 && args[0] == "watch" {
		return watchStatus(store, os.Stdout, 200*time.Millisecond)
	}
	return errors.New("expected `looptab status`, `looptab status json`, or `looptab status watch`")
}

func watchStatus(store active.Store, w io.Writer, interval time.Duration) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		if err := printStatusJSONLine(store, w); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

func printStatusJSONLine(store active.Store, w io.Writer) error {
	summary, err := store.Summary()
	if err != nil {
		return err
	}
	content, err := json.Marshal(summary)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(w, string(content))
	return err
}

func inspectCommand(p paths.Paths, args []string) error {
	if len(args) > 1 {
		return errors.New("expected `looptab inspect` or `looptab inspect <job-or-run-id>`")
	}

	id := ""
	if len(args) == 1 {
		id = args[0]
	}

	activeStore := active.NewStore(p)
	summary, err := activeStore.Summary()
	if err != nil {
		return err
	}

	activeJob, found, err := selectActiveJob(summary.Jobs, id)
	if err != nil {
		return err
	}
	if found {
		return inspectActiveRun(p, activeJob, os.Stdout)
	}

	if id == "" {
		if summary.Count == 0 {
			fmt.Fprintln(os.Stdout, "No looptab runs are active.")
			fmt.Fprintln(os.Stdout, "Use `looptab inspect <job-or-run-id>` to inspect a completed run.")
			return nil
		}
		if err := activeStore.Print(os.Stdout); err != nil {
			return err
		}
		fmt.Fprintln(os.Stdout, "\ninspect one with:")
		fmt.Fprintln(os.Stdout, "  looptab inspect <job>")
		return nil
	}

	return inspectCompletedRun(p, id, os.Stdout)
}

func streamCommand(p paths.Paths, args []string) error {
	if len(args) > 1 {
		return errors.New("expected `looptab stream [index]`")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	store := active.NewStore(p)
	if len(args) == 0 {
		return streamActiveRuns(ctx, store, os.Stdout, 250*time.Millisecond)
	}

	index, err := strconv.Atoi(args[0])
	if err != nil || index < 0 {
		return fmt.Errorf("expected active job index, got %q", args[0])
	}
	return streamActiveRunIndex(ctx, store, os.Stdout, 250*time.Millisecond, index)
}

func killCommand(p paths.Paths, args []string) error {
	if len(args) != 1 {
		return errors.New("expected `looptab kill <index>`")
	}
	index, err := strconv.Atoi(args[0])
	if err != nil || index < 0 {
		return fmt.Errorf("expected active job index, got %q", args[0])
	}

	store := active.NewStore(p)
	summary, err := store.Summary()
	if err != nil {
		return err
	}
	if summary.Count == 0 {
		fmt.Fprintln(os.Stdout, "No looptab runs are active.")
		return nil
	}
	if index >= len(summary.Jobs) {
		return fmt.Errorf("active job index %d is not running; use `looptab status`", index)
	}

	job := summary.Jobs[index]
	if len(job.KillPIDs) == 0 {
		if job.LegacyNoLive {
			if err := store.Remove(job.RunID); err != nil {
				return err
			}
			fmt.Fprintf(os.Stdout, "Removed stale active job %d (%s); no process was running.\n", job.Index, job.JobID)
			return nil
		}
		return fmt.Errorf("active job %d (%s) has no killable process yet", job.Index, job.JobID)
	}

	for _, pid := range job.KillPIDs {
		if err := terminateProcess(pid, 2*time.Second); err != nil {
			return fmt.Errorf("kill job %d (%s), pid %d: %w", job.Index, job.JobID, pid, err)
		}
	}
	fmt.Fprintf(os.Stdout, "Killed active job %d (%s).\n", job.Index, job.JobID)
	return nil
}

func selectActiveJob(jobs []active.Job, id string) (active.Job, bool, error) {
	if id == "" {
		if len(jobs) == 1 {
			return jobs[0], true, nil
		}
		return active.Job{}, false, nil
	}

	var matches []active.Job
	for _, job := range jobs {
		if matchesRunID(job.RunID, id) || matchesRunID(job.JobID, id) {
			matches = append(matches, job)
		}
	}
	if len(matches) == 0 {
		return active.Job{}, false, nil
	}
	if len(matches) > 1 {
		return active.Job{}, false, fmt.Errorf("active run id prefix is ambiguous: %s", id)
	}
	return matches[0], true, nil
}

func inspectActiveRun(p paths.Paths, job active.Job, w io.Writer) error {
	fmt.Fprintf(w, "Active looptab run %s\n", job.RunID)
	fmt.Fprintf(w, "job: %s\n", job.JobID)
	fmt.Fprintf(w, "duration: %s\n", formatMillis(job.DurationMillis))
	fmt.Fprintf(w, "cwd: %s\n", job.CWDDisplay)
	fmt.Fprintf(w, "kind: %s\n", job.Kind)
	fmt.Fprintf(w, "action: %s\n", job.ActionDisplay)
	if job.OutputPath == "" {
		fmt.Fprintln(w, "\nlive output is not available for this run.")
		fmt.Fprintln(w, "This run started before live inspection was installed.")
		return nil
	}

	fmt.Fprintf(w, "output: %s\n\n", job.OutputPath)
	if err := runlog.PrintTail(w, job.OutputPath, 80); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			fmt.Fprintln(w, "  waiting for output...")
		} else {
			return err
		}
	}
	fmt.Fprintln(w, "\nfollowing live output; press Ctrl-C to stop inspecting.")
	return followActiveOutput(p, job, w)
}

func inspectCompletedRun(p paths.Paths, id string, w io.Writer) error {
	store := runlog.NewStore(p)
	records, err := store.Records()
	if err != nil {
		return err
	}

	var matches []runlog.Record
	for _, record := range records {
		if matchesRunID(record.RunID, id) || matchesRunID(record.JobID, id) {
			matches = append(matches, record)
		}
	}
	if len(matches) == 0 {
		return fmt.Errorf("no active or completed looptab run found for: %s", id)
	}

	latest := matches[len(matches)-1]
	fmt.Fprintf(w, "Looptab run %s\n", latest.RunID)
	fmt.Fprintf(w, "job: %s\n", latest.JobID)
	fmt.Fprintf(w, "status: %s\n", latest.Status)
	fmt.Fprintf(w, "duration: %s\n", formatMillis(latest.DurationMillis))
	fmt.Fprintf(w, "cwd: %s\n", paths.DisplayPath(latest.CWD))
	if latest.Kind != "" {
		fmt.Fprintf(w, "kind: %s\n", latest.Kind)
	}
	fmt.Fprintf(w, "action: %s\n", actionDisplayForRecord(latest))
	if latest.OutputPath == "" {
		return errors.New("this run has no output log")
	}
	fmt.Fprintf(w, "output: %s\n\n", latest.OutputPath)
	return runlog.PrintTail(w, latest.OutputPath, 80)
}

type runStream struct {
	job            active.Job
	file           *os.File
	offset         int64
	atLineStart    bool
	waitingForFile bool
}

func streamActiveRuns(ctx context.Context, store active.Store, w io.Writer, interval time.Duration) error {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	streams := make(map[string]*runStream)
	waitingPrinted := false

	for {
		summary, err := store.Summary()
		if err != nil {
			closeRunStreams(streams)
			return err
		}

		activeRuns := make(map[string]bool)
		if summary.Count == 0 && len(streams) == 0 && !waitingPrinted {
			fmt.Fprintln(w, "No looptab runs are active; waiting for live output. Press Ctrl-C to stop.")
			waitingPrinted = true
		}

		for _, job := range summary.Jobs {
			activeRuns[job.RunID] = true
			if _, ok := streams[job.RunID]; ok {
				continue
			}

			stream, err := openRunStream(job, w)
			if err != nil {
				closeRunStreams(streams)
				return err
			}
			streams[job.RunID] = stream
			waitingPrinted = false
		}

		for runID, stream := range streams {
			if err := stream.copyNew(w); err != nil {
				closeRunStreams(streams)
				return err
			}
			if activeRuns[runID] {
				continue
			}

			if err := stream.copyNew(w); err != nil {
				closeRunStreams(streams)
				return err
			}
			stream.close()
			fmt.Fprintf(w, "\n[%s] finished\n", stream.job.JobID)
			delete(streams, runID)
		}

		select {
		case <-ctx.Done():
			closeRunStreams(streams)
			return nil
		case <-ticker.C:
		}
	}
}

func streamActiveRunIndex(ctx context.Context, store active.Store, w io.Writer, interval time.Duration, index int) error {
	summary, err := store.Summary()
	if err != nil {
		return err
	}
	if summary.Count == 0 {
		fmt.Fprintln(w, "No looptab Codex runs are active.")
		return nil
	}
	if index >= len(summary.Jobs) {
		return fmt.Errorf("active job index %d is not running; use `looptab status`", index)
	}

	stream, err := openRunStream(summary.Jobs[index], w)
	if err != nil {
		return err
	}
	defer stream.close()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		if err := stream.copyNew(w); err != nil {
			return err
		}

		summary, err := store.Summary()
		if err != nil {
			return err
		}
		if !summaryHasRun(summary, stream.job.RunID) {
			if err := stream.copyNew(w); err != nil {
				return err
			}
			fmt.Fprintf(w, "\n[%s] finished\n", stream.job.JobID)
			return nil
		}

		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

func summaryHasRun(summary active.Summary, runID string) bool {
	for _, job := range summary.Jobs {
		if job.RunID == runID {
			return true
		}
	}
	return false
}

func openRunStream(job active.Job, w io.Writer) (*runStream, error) {
	fmt.Fprintf(w, "\n[%s] started from %s: %s\n", job.JobID, job.CWDDisplay, job.ActionDisplay)
	if job.OutputPath == "" {
		if job.LegacyNoLive {
			fmt.Fprintf(w, "[%s] live output is not available because this run was started by an older scheduler that did not create live output files\n", job.JobID)
			fmt.Fprintf(w, "[%s] after active jobs finish, run `looptab service restart` to load the upgraded scheduler\n", job.JobID)
		} else {
			fmt.Fprintf(w, "[%s] live output is not available for this run\n", job.JobID)
		}
		return &runStream{job: job, atLineStart: true}, nil
	}

	stream := &runStream{job: job, atLineStart: true}
	if err := stream.openOutput(w); err != nil {
		return nil, err
	}
	return stream, nil
}

func (s *runStream) openOutput(w io.Writer) error {
	if s.file != nil || s.job.OutputPath == "" {
		return nil
	}

	file, err := os.Open(s.job.OutputPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			if !s.waitingForFile {
				fmt.Fprintf(w, "[%s] waiting for live output file: %s\n", s.job.JobID, s.job.OutputPath)
				s.waitingForFile = true
			}
			return nil
		}
		return err
	}

	s.file = file
	s.waitingForFile = false
	offset, err := s.printInitialTail(w, 30)
	if err != nil {
		s.close()
		return err
	}
	s.offset = offset
	return nil
}

func (s *runStream) printInitialTail(w io.Writer, limit int) (int64, error) {
	if s.file == nil {
		return 0, nil
	}
	content, err := io.ReadAll(s.file)
	if err != nil {
		return 0, err
	}
	if len(content) == 0 {
		return 0, nil
	}

	tail := lastOutputLines(content, limit)
	if len(tail) != len(content) {
		fmt.Fprintf(w, "[%s] ... showing latest %d lines\n", s.job.JobID, limit)
	}
	atLineStart, err := writeLabeledOutput(w, s.job.JobID, tail, true)
	if err != nil {
		return 0, err
	}
	s.atLineStart = atLineStart
	return int64(len(content)), nil
}

func (s *runStream) copyNew(w io.Writer) error {
	if s.file == nil {
		return s.openOutput(w)
	}

	stat, err := s.file.Stat()
	if err != nil {
		return err
	}
	if stat.Size() < s.offset {
		s.offset = 0
	}
	if stat.Size() == s.offset {
		return nil
	}
	if _, err := s.file.Seek(s.offset, io.SeekStart); err != nil {
		return err
	}

	reader := io.LimitReader(s.file, stat.Size()-s.offset)
	content, err := io.ReadAll(reader)
	if err != nil {
		return err
	}
	atLineStart, err := writeLabeledOutput(w, s.job.JobID, content, s.atLineStart)
	if err != nil {
		return err
	}
	s.atLineStart = atLineStart
	s.offset = stat.Size()
	return nil
}

func (s *runStream) close() {
	if s.file != nil {
		_ = s.file.Close()
		s.file = nil
	}
}

func closeRunStreams(streams map[string]*runStream) {
	for _, stream := range streams {
		stream.close()
	}
}

func lastOutputLines(content []byte, limit int) []byte {
	if limit <= 0 || len(content) == 0 {
		return nil
	}

	lines := bytes.SplitAfter(content, []byte("\n"))
	if len(lines) > 0 && len(lines[len(lines)-1]) == 0 {
		lines = lines[:len(lines)-1]
	}
	if len(lines) <= limit {
		return content
	}
	return bytes.Join(lines[len(lines)-limit:], nil)
}

func writeLabeledOutput(w io.Writer, label string, content []byte, atLineStart bool) (bool, error) {
	parts := bytes.SplitAfter(content, []byte("\n"))
	for _, part := range parts {
		if len(part) == 0 {
			continue
		}
		if atLineStart {
			if _, err := fmt.Fprintf(w, "[%s] ", label); err != nil {
				return atLineStart, err
			}
		}
		if _, err := w.Write(part); err != nil {
			return atLineStart, err
		}
		atLineStart = bytes.HasSuffix(part, []byte("\n"))
	}
	return atLineStart, nil
}

func terminateProcess(pid int, grace time.Duration) error {
	if pid <= 0 {
		return errors.New("invalid pid")
	}
	if err := syscall.Kill(-pid, syscall.SIGTERM); err != nil {
		if errors.Is(err, syscall.ESRCH) {
			if err := syscall.Kill(pid, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
				return err
			}
		} else {
			return err
		}
	}

	deadline := time.Now().Add(grace)
	for time.Now().Before(deadline) {
		if !processAlive(pid) {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}

	if err := syscall.Kill(-pid, syscall.SIGKILL); err != nil {
		if errors.Is(err, syscall.ESRCH) {
			if err := syscall.Kill(pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
				return err
			}
			return nil
		}
		return err
	}
	return nil
}

func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = process.Signal(syscall.Signal(0))
	return err == nil || errors.Is(err, syscall.EPERM)
}

func followActiveOutput(p paths.Paths, job active.Job, w io.Writer) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	file, err := os.Open(job.OutputPath)
	if err != nil {
		return err
	}
	defer file.Close()

	offset, err := file.Seek(0, io.SeekEnd)
	if err != nil {
		return err
	}

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	activePath := filepath.Join(p.ActiveDir, job.RunID+".json")
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			nextOffset, err := copyNewOutput(file, offset, w)
			if err != nil {
				return err
			}
			offset = nextOffset
			if _, err := os.Stat(activePath); errors.Is(err, os.ErrNotExist) {
				_, _ = copyNewOutput(file, offset, w)
				fmt.Fprintln(w, "\nlooptab run finished.")
				return nil
			}
		}
	}
}

func copyNewOutput(file *os.File, offset int64, w io.Writer) (int64, error) {
	stat, err := file.Stat()
	if err != nil {
		return offset, err
	}
	if stat.Size() <= offset {
		return offset, nil
	}
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		return offset, err
	}
	if _, err := io.CopyN(w, file, stat.Size()-offset); err != nil {
		return offset, err
	}
	return stat.Size(), nil
}

func matchesRunID(value string, id string) bool {
	return value == id || strings.HasPrefix(value, id)
}

func formatMillis(ms int64) string {
	if ms <= 0 {
		return "0s"
	}
	duration := time.Duration(ms) * time.Millisecond
	if duration < time.Second {
		return duration.String()
	}
	return duration.Round(time.Second).String()
}

func serviceCommand(args []string) error {
	if len(args) != 1 {
		return errors.New("expected `looptab service install|start|stop|restart|status|remove`")
	}

	manager, err := service.NewUserManager()
	if err != nil {
		return err
	}

	switch args[0] {
	case "install":
		return manager.Install()
	case "start":
		return manager.Start()
	case "stop":
		return manager.Stop()
	case "restart":
		return manager.Restart()
	case "status":
		return manager.Status()
	case "remove":
		return manager.Remove()
	default:
		return errors.New("expected `looptab service install|start|stop|restart|status|remove`")
	}
}

func runCheck(p paths.Paths, w io.Writer) error {
	if err := ensureLayout(p); err != nil {
		return err
	}

	file, err := loader.Load(p)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("looptab file not found: %s\nrun `looptab` to create it", p.ConfigFile)
		}
		return err
	}

	jobRunner, err := runner.NewRunner()
	if err != nil {
		return err
	}

	var invalid []string
	needsCodex := false
	needsGrok := false
	for _, job := range file.Jobs {
		info, err := os.Stat(job.CWD)
		if err != nil {
			invalid = append(invalid, fmt.Sprintf("line %d: cwd does not exist: %s", job.Line, job.CWD))
			continue
		}
		if !info.IsDir() {
			invalid = append(invalid, fmt.Sprintf("line %d: cwd is not a directory: %s", job.Line, job.CWD))
		}
		steps := parser.FlattenSteps(job.Steps)
		if len(steps) == 0 {
			steps = []parser.Step{{Kind: job.Kind, Prompt: job.Prompt, Command: job.Command}}
		}
		for _, step := range steps {
			switch step.Kind {
			case parser.JobKindGrok:
				needsGrok = true
			case parser.JobKindCommand:
				if len(step.Command) == 0 {
					invalid = append(invalid, fmt.Sprintf("line %d: command step is missing an executable", job.Line))
					continue
				}
				if err := validateCommandExecutable(step.Command[0]); err != nil {
					invalid = append(invalid, fmt.Sprintf("line %d: %v", job.Line, err))
				}
			default:
				needsCodex = true
			}
		}
	}
	if len(invalid) > 0 {
		return errors.New(strings.Join(invalid, "\n"))
	}

	if needsCodex {
		jobRunner.CodexBin, err = runner.FindCodexBinary()
		if err != nil {
			return err
		}
	}
	if needsGrok {
		jobRunner.GrokBin, err = runner.FindGrokBinary()
		if err != nil {
			return err
		}
	}

	fmt.Fprintf(w, "looptab check passed\n")
	fmt.Fprintf(w, "file: %s\n", p.ConfigFile)
	fmt.Fprintf(w, "config: %s\n", config.SettingsPath(p))
	fmt.Fprintf(w, "timezone: %s\n", file.Timezone)
	fmt.Fprintf(w, "jobs: %d\n", len(file.Jobs))
	if needsCodex {
		fmt.Fprintf(w, "codex: %s\n", jobRunner.CodexBin)
	}
	if needsGrok {
		fmt.Fprintf(w, "grok: %s\n", jobRunner.GrokBin)
	}
	if len(file.Jobs) > 0 {
		fmt.Fprintln(w, "")
		fmt.Fprintln(w, "parsed jobs:")
		for _, job := range file.Jobs {
			fmt.Fprintf(w, "  %s  line %d  %s  %s  %s  %s\n", job.ID, job.Line, job.Kind, job.Schedule, paths.DisplayPath(job.CWD), job.ActionDisplay())
		}
	}
	printNowNoticeForFile(p, file, w)
	return nil
}

func loadFile(p paths.Paths) (parser.File, error) {
	return loader.Load(p)
}

func ensureLayout(p paths.Paths) error {
	if err := config.EnsureSettings(p); err != nil {
		return err
	}
	return paths.EnsureConfigFile(p)
}

type fileSnapshot struct {
	modTime time.Time
	size    int64
}

type editSnapshot struct {
	looptab fileSnapshot
}

func openEditor(p paths.Paths) error {
	if err := ensureLayout(p); err != nil {
		return err
	}

	before, err := snapshotEditState(p)
	if err != nil {
		return err
	}

	if err := editor.Open(p.ConfigFile); err != nil {
		if editorAborted(err) {
			return nil
		}
		return err
	}

	after, err := snapshotEditState(p)
	if err != nil {
		return err
	}
	if before.unchanged(after) {
		return nil
	}

	return ensureSchedulerAfterEdit(p, os.Stdout)
}

func snapshotFile(path string) (fileSnapshot, error) {
	stat, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fileSnapshot{}, nil
		}
		return fileSnapshot{}, err
	}
	return fileSnapshot{
		modTime: stat.ModTime(),
		size:    stat.Size(),
	}, nil
}

func snapshotEditState(p paths.Paths) (editSnapshot, error) {
	looptab, err := snapshotFile(p.ConfigFile)
	if err != nil {
		return editSnapshot{}, err
	}
	return editSnapshot{looptab: looptab}, nil
}

func (before editSnapshot) unchanged(after editSnapshot) bool {
	return before.looptab == after.looptab
}

func editorAborted(err error) bool {
	var exitErr *exec.ExitError
	return errors.As(err, &exitErr)
}

func printNowNotice(p paths.Paths, w io.Writer) error {
	file, err := loadFile(p)
	if err != nil {
		return err
	}
	printNowNoticeForFile(p, file, w)
	return nil
}

func ensureSchedulerAfterEdit(p paths.Paths, w io.Writer) error {
	file, err := loadFile(p)
	if err != nil {
		return err
	}
	if len(file.Jobs) == 0 {
		fmt.Fprintln(w, "No looptab jobs are configured; scheduler was not started.")
		return nil
	}

	if schedulerActive(p) {
		fmt.Fprintln(w, "looptab scheduler is already running.")
		return nil
	}

	manager, err := service.NewUserManager()
	if err != nil {
		if errors.Is(err, service.ErrUnsupported) {
			fmt.Fprintln(w, "Looptab background service is not supported here; run `looptab run` to keep the scheduler active.")
			return nil
		}
		return err
	}

	if err := manager.EnsureStarted(w); err != nil {
		return err
	}
	if len(nowJobs(file)) > 0 {
		fmt.Fprintln(w, "now jobs will run as the scheduler loads the file.")
	}
	return nil
}

func printNowNoticeForFile(p paths.Paths, file parser.File, w io.Writer) {
	jobs := nowJobs(file)
	if len(jobs) == 0 {
		return
	}

	if schedulerActive(p) {
		fmt.Fprintln(w, "")
		fmt.Fprintln(w, "now jobs will run when the active scheduler reloads the file.")
		return
	}

	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "now jobs are waiting, but no looptab scheduler is active.")
	fmt.Fprintln(w, "run them once with:")
	fmt.Fprintln(w, "  looptab run now")
	fmt.Fprintln(w, "or keep looptab running with:")
	fmt.Fprintln(w, "  looptab service install")
	fmt.Fprintln(w, "  looptab service start")
}

func nowJobs(file parser.File) []parser.Job {
	var jobs []parser.Job
	for _, job := range file.Jobs {
		if job.Once {
			jobs = append(jobs, job)
		}
	}
	return jobs
}

func schedulerActive(p paths.Paths) bool {
	_, err := os.Stat(p.LockFile)
	return err == nil
}

func validateCommandExecutable(executable string) error {
	if strings.HasPrefix(executable, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		executable = filepath.Join(home, strings.TrimPrefix(executable, "~/"))
	}
	if filepath.IsAbs(executable) {
		info, err := os.Stat(executable)
		if err != nil {
			return fmt.Errorf("executable does not exist: %s", executable)
		}
		if info.IsDir() {
			return fmt.Errorf("executable is a directory: %s", executable)
		}
		return nil
	}
	if _, err := exec.LookPath(executable); err != nil {
		return fmt.Errorf("executable not found on PATH: %s", executable)
	}
	return nil
}

func actionDisplayForRecord(record runlog.Record) string {
	if len(record.Command) > 0 {
		return strings.Join(record.Command, " ")
	}
	if record.Kind == string(parser.JobKindGrok) && record.Prompt != "" {
		return "@grok " + strconv.Quote(record.Prompt)
	}
	return record.Prompt
}

func printHelp(w io.Writer) {
	text := `Looptab

Edit and run scheduled AI loops and commands from ~/.config/looptab/looptab.

global actions:
  looptab
    open the looptab file, then start the background scheduler when jobs exist
  looptab help
    show this help
  looptab version
    print the installed version
  looptab now
    list registered jobs and run one immediately

features:
  edit the source-of-truth loop file
  # timezone lives in ~/.config/looptab/config.json

  # <when> [cwd] <action> [? on-success [: on-failure]] [&& ...]
  now @codex "Run once when looptab loads."
  daily 5am @grok "Check my emails." ? notify heading "gmail" body "inbox review finished" : notify heading "gmail" body "inbox review failed"
  hourly gdrive sync run ? notify heading "gdrive" body "backup finished" : notify heading "gdrive" body "backup failed"
  every 30s tm snapshot sessions
  hourly at 15 ~/Work/example @codex "Review the repo at minute 15 every hour."
  weekdays 9am ~/Work/example @codex "Plan the day and update TODOs."

  validate the file, working directories, and required executables
  # check
  looptab check

  pick a registered job and run it immediately
  # now
  looptab now

  run the scheduler in the foreground or run scheduled now-jobs
  # run | run now | run job <id>
  looptab run
  looptab run now
  looptab run job a1b2c3d4

  inspect live or completed job output
  # inspect | inspect <job-or-run-id>
  looptab inspect
  looptab inspect a1b2c3d4

  stream live job output across all active loops or one active index
  # stream [index]
  looptab stream
  looptab stream 0

  kill an active loop by status index
  # kill <index>
  looptab status
  looptab kill 0

  inspect active loops
  # status | status json | status watch
  looptab status
  looptab status json
  looptab status watch

  install and manage the background scheduler
  # service install|start|stop|restart|status|remove
  looptab service install
  looptab service start
  looptab service restart
  looptab service status
`

	if isTTY(os.Stdout) && os.Getenv("NO_COLOR") == "" {
		fmt.Fprintf(w, "\033[38;5;245m%s\033[0m", text)
		return
	}
	fmt.Fprint(w, text)
}

func isTTY(file *os.File) bool {
	info, err := file.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}
