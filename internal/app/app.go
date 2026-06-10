package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/ryangerardwilson/looptab/internal/active"
	"github.com/ryangerardwilson/looptab/internal/codex"
	"github.com/ryangerardwilson/looptab/internal/editor"
	"github.com/ryangerardwilson/looptab/internal/lock"
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
		if err := paths.EnsureConfigFile(p); err != nil {
			return err
		}
		if err := editor.Open(p.ConfigFile); err != nil {
			return err
		}
		return ensureSchedulerAfterEdit(p, os.Stdout)
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
	case "run":
		return runCommand(p, args[1:])
	case "logs":
		return logsCommand(p, args[1:])
	case "inspect":
		return inspectCommand(p, args[1:])
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
		if err := paths.EnsureConfigFile(p); err != nil {
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
	runner, err := codex.NewRunner()
	if err != nil {
		return err
	}

	store := runlog.NewStore(p).WithLocation(file.Location)
	activeStore := active.NewStore(p)
	fmt.Fprintf(os.Stdout, "running job %s from %s\n", job.ID, paths.DisplayPath(job.CWD))
	handle, err := activeStore.Begin(job)
	if err != nil {
		fmt.Fprintf(os.Stderr, "looptab active status failed: %v\n", err)
	} else {
		defer handle.End()
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

	result := runner.RunWithOutput(context.Background(), job, handle.StartedAt(), liveWriter)
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

func logsCommand(p paths.Paths, args []string) error {
	location := time.UTC
	if file, err := loadFile(p); err == nil {
		location = file.Location
	}
	store := runlog.NewStore(p).WithLocation(location)
	if len(args) == 0 {
		return store.PrintSummary(os.Stdout)
	}
	if len(args) == 2 && args[0] == "job" {
		return store.PrintJob(os.Stdout, args[1])
	}
	return errors.New("expected `looptab logs` or `looptab logs job <id>`")
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
			fmt.Fprintln(os.Stdout, "No looptab Codex runs are active.")
			fmt.Fprintln(os.Stdout, "Use `looptab logs` to inspect completed runs.")
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
	fmt.Fprintf(w, "prompt: %s\n", job.Prompt)
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
	fmt.Fprintf(w, "prompt: %s\n", latest.Prompt)
	if latest.OutputPath == "" {
		return errors.New("this run has no output log")
	}
	fmt.Fprintf(w, "output: %s\n\n", latest.OutputPath)
	return runlog.PrintTail(w, latest.OutputPath, 80)
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
		return errors.New("expected `looptab service install|start|stop|status|remove`")
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
	case "status":
		return manager.Status()
	case "remove":
		return manager.Remove()
	default:
		return errors.New("expected `looptab service install|start|stop|status|remove`")
	}
}

func runCheck(p paths.Paths, w io.Writer) error {
	content, err := os.ReadFile(p.ConfigFile)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("looptab file not found: %s\nrun `looptab` to create it", p.ConfigFile)
		}
		return err
	}

	file, err := parser.Parse(string(content))
	if err != nil {
		return err
	}

	codexPath, err := codex.FindBinary()
	if err != nil {
		return err
	}

	var invalid []string
	for _, job := range file.Jobs {
		info, err := os.Stat(job.CWD)
		if err != nil {
			invalid = append(invalid, fmt.Sprintf("line %d: cwd does not exist: %s", job.Line, job.CWD))
			continue
		}
		if !info.IsDir() {
			invalid = append(invalid, fmt.Sprintf("line %d: cwd is not a directory: %s", job.Line, job.CWD))
		}
	}
	if len(invalid) > 0 {
		return errors.New(strings.Join(invalid, "\n"))
	}

	fmt.Fprintf(w, "looptab check passed\n")
	fmt.Fprintf(w, "file: %s\n", p.ConfigFile)
	fmt.Fprintf(w, "timezone: %s\n", file.Timezone)
	fmt.Fprintf(w, "jobs: %d\n", len(file.Jobs))
	fmt.Fprintf(w, "codex: %s\n", codexPath)
	if len(file.Jobs) > 0 {
		fmt.Fprintln(w, "")
		fmt.Fprintln(w, "parsed jobs:")
		for _, job := range file.Jobs {
			fmt.Fprintf(w, "  %s  line %d  %s  %s  %q\n", job.ID, job.Line, job.Schedule, paths.DisplayPath(job.CWD), job.Prompt)
		}
	}
	printNowNoticeForFile(p, file, w)
	return nil
}

func loadFile(p paths.Paths) (parser.File, error) {
	content, err := os.ReadFile(p.ConfigFile)
	if err != nil {
		return parser.File{}, err
	}
	return parser.Parse(string(content))
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

func printHelp(w io.Writer) {
	text := `Looptab

Edit and run Codex loops from ~/.config/looptab/looptab.

global actions:
  looptab
    open the looptab file, then start the background scheduler when jobs exist
  looptab help
    show this help
  looptab version
    print the installed version

features:
  edit the source-of-truth loop file
  # timezone <IANA name>
  timezone UTC

  # <when> [cwd] "<prompt>"
  now "Run once from home when looptab loads."
  hourly "Review from home once per hour."
  hourly at 15 ~/Work/example "Review the repo at minute 15 every hour."
  daily 11am "Review from home and fix one small obvious issue."
  daily 11am ~/Work/example "Review the repo and fix one small obvious issue."
  daily 11am,12pm,1pm ~/Work/example "Run a quick maintenance pass."
  weekdays 9am ~/Work/example "Plan the day and update TODOs."
  mondays 5am ~/Work/example "Prepare the weekly review."

  validate the file and local Codex command
  # check
  looptab check

  run the scheduler in the foreground or run one job now
  # run | run now | run job <id>
  looptab run
  looptab run now
  looptab run job a1b2c3d4

  inspect what ran, when it ran, and what Codex reported
  # logs | logs job <id>
  looptab logs
  looptab logs job a1b2c3d4

  inspect live or completed Codex output
  # inspect | inspect <job-or-run-id>
  looptab inspect
  looptab inspect a1b2c3d4

  inspect active Codex loops
  # status | status json | status watch
  looptab status
  looptab status json
  looptab status watch

  install and manage the background scheduler
  # service install|start|stop|status|remove
  looptab service install
  looptab service start
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
