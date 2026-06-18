package app

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/ryangerardwilson/looptab/internal/parser"
	"github.com/ryangerardwilson/looptab/internal/paths"
)

func runNowCommand(p paths.Paths, args []string) error {
	file, err := loadFile(p)
	if err != nil {
		return err
	}
	if len(file.Jobs) == 0 {
		fmt.Fprintln(os.Stdout, "No jobs are configured.")
		return nil
	}

	switch len(args) {
	case 0:
		return runInteractiveNowSelect(p, file)
	case 1:
		job, err := selectJob(file.Jobs, args[0])
		if err != nil {
			return err
		}
		return runJobOnce(p, file, job)
	default:
		return errors.New("expected `looptab now` or `looptab now <index-or-job-id>`")
	}
}

func runInteractiveNowSelect(p paths.Paths, file parser.File) error {
	printRegisteredJobs(os.Stdout, file.Jobs)

	if !isTTY(os.Stdin) {
		return errors.New("looptab now requires an interactive terminal\nrun `looptab now <index-or-job-id>` instead")
	}

	fmt.Fprint(os.Stdout, "Run which job now? [index, job id, Enter to cancel]: ")
	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil {
		if errors.Is(err, io.EOF) {
			fmt.Fprintln(os.Stdout, "Cancelled.")
			return nil
		}
		return err
	}

	selection := strings.TrimSpace(line)
	if selection == "" || isSelectionCancel(selection) {
		fmt.Fprintln(os.Stdout, "Cancelled.")
		return nil
	}

	job, err := selectJob(file.Jobs, selection)
	if err != nil {
		return err
	}
	return runJobOnce(p, file, job)
}

func printRegisteredJobs(w io.Writer, jobs []parser.Job) {
	fmt.Fprintln(w, "Registered jobs:")
	for index, job := range jobs {
		fmt.Fprintf(
			w,
			"  [%d] %s  %s  %s  %s  %s\n",
			index,
			job.ID,
			job.Schedule,
			job.Kind,
			paths.DisplayPath(job.CWD),
			job.ActionDisplay(),
		)
	}
}

func isSelectionCancel(selection string) bool {
	switch strings.ToLower(selection) {
	case "q", "quit", "cancel":
		return true
	default:
		return false
	}
}

func selectJob(jobs []parser.Job, selection string) (parser.Job, error) {
	if index, err := strconv.Atoi(selection); err == nil {
		if index < 0 || index >= len(jobs) {
			return parser.Job{}, fmt.Errorf("job index out of range: %d", index)
		}
		return jobs[index], nil
	}
	return parser.FindJob(jobs, selection)
}