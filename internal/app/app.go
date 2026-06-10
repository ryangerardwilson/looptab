package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

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
		return editor.Open(p.ConfigFile)
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

	if len(args) == 2 && args[0] == "job" {
		return runOneJob(p, args[1])
	}

	return errors.New("expected `looptab run` or `looptab run job <id>`")
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

	runner, err := codex.NewRunner()
	if err != nil {
		return err
	}

	store := runlog.NewStore(p).WithLocation(file.Location)
	fmt.Fprintf(os.Stdout, "running job %s from %s\n", job.ID, paths.DisplayPath(job.CWD))
	result := runner.Run(context.Background(), job)
	record, outputErr := runlog.RecordFromResult(job, result)
	if outputErr != nil {
		return outputErr
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
	return nil
}

func loadFile(p paths.Paths) (parser.File, error) {
	content, err := os.ReadFile(p.ConfigFile)
	if err != nil {
		return parser.File{}, err
	}
	return parser.Parse(string(content))
}

func printHelp(w io.Writer) {
	text := `Looptab

Edit and run Codex loops from ~/.config/looptab/looptab.

global actions:
  looptab
    open the looptab file
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
  daily 11am "Review from home and fix one small obvious issue."
  daily 11am ~/Work/example "Review the repo and fix one small obvious issue."
  daily 11am,12pm,1pm ~/Work/example "Run a quick maintenance pass."
  weekdays 9am ~/Work/example "Plan the day and update TODOs."
  mondays 5am ~/Work/example "Prepare the weekly review."

  validate the file and local Codex command
  # check
  looptab check

  run the scheduler in the foreground or run one job now
  # run | run job <id>
  looptab run
  looptab run job a1b2c3d4

  inspect what ran, when it ran, and what Codex reported
  # logs | logs job <id>
  looptab logs
  looptab logs job a1b2c3d4

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
