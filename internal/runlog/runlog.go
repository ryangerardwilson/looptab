package runlog

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"text/tabwriter"
	"time"
	"unicode"

	"github.com/ryangerardwilson/looptab/internal/codex"
	"github.com/ryangerardwilson/looptab/internal/parser"
	"github.com/ryangerardwilson/looptab/internal/paths"
)

type Store struct {
	paths    paths.Paths
	location *time.Location
}

type Record struct {
	RunID          string    `json:"run_id"`
	JobID          string    `json:"job_id"`
	Line           int       `json:"line"`
	Schedule       string    `json:"schedule"`
	Timezone       string    `json:"timezone,omitempty"`
	CWD            string    `json:"cwd"`
	Prompt         string    `json:"prompt"`
	StartedAt      time.Time `json:"started_at"`
	FinishedAt     time.Time `json:"finished_at"`
	DurationMillis int64     `json:"duration_millis"`
	Status         string    `json:"status"`
	ExitCode       int       `json:"exit_code"`
	Summary        string    `json:"summary"`
	Error          string    `json:"error,omitempty"`
	OutputPath     string    `json:"output_path,omitempty"`
}

func NewStore(p paths.Paths) Store {
	return Store{paths: p, location: time.UTC}
}

func (s Store) WithLocation(location *time.Location) Store {
	if location != nil {
		s.location = location
	}
	return s
}

func RecordFromResult(job parser.Job, result codex.Result) (Record, error) {
	status := "ok"
	errorText := ""
	if result.Err != nil {
		status = "failed"
		errorText = result.Err.Error()
	}

	if result.FinishedAt.IsZero() {
		result.FinishedAt = time.Now()
	}

	record := Record{
		RunID:          runID(job.ID, result.StartedAt),
		JobID:          job.ID,
		Line:           job.Line,
		Schedule:       job.Schedule,
		Timezone:       job.Timezone,
		CWD:            job.CWD,
		Prompt:         job.Prompt,
		StartedAt:      result.StartedAt,
		FinishedAt:     result.FinishedAt,
		DurationMillis: result.FinishedAt.Sub(result.StartedAt).Milliseconds(),
		Status:         status,
		ExitCode:       result.ExitCode,
		Summary:        Summarize(result.Output, errorText),
		Error:          errorText,
	}
	return record, nil
}

func SkippedRecord(job parser.Job, reason string) Record {
	now := time.Now()
	return Record{
		RunID:          runID(job.ID, now),
		JobID:          job.ID,
		Line:           job.Line,
		Schedule:       job.Schedule,
		Timezone:       job.Timezone,
		CWD:            job.CWD,
		Prompt:         job.Prompt,
		StartedAt:      now,
		FinishedAt:     now,
		DurationMillis: 0,
		Status:         "skipped",
		ExitCode:       -1,
		Summary:        reason,
		Error:          reason,
	}
}

func FailedRecord(job parser.Job, reason string) Record {
	now := time.Now()
	return Record{
		RunID:          runID(job.ID, now),
		JobID:          job.ID,
		Line:           job.Line,
		Schedule:       job.Schedule,
		Timezone:       job.Timezone,
		CWD:            job.CWD,
		Prompt:         job.Prompt,
		StartedAt:      now,
		FinishedAt:     now,
		DurationMillis: 0,
		Status:         "failed",
		ExitCode:       -1,
		Summary:        reason,
		Error:          reason,
	}
}

func (s Store) Save(record Record, output string) error {
	if err := paths.EnsureState(s.paths); err != nil {
		return err
	}
	if strings.TrimSpace(output) != "" {
		outputPath, err := s.writeOutput(record.RunID, output)
		if err != nil {
			return err
		}
		record.OutputPath = outputPath
	}
	return s.append(record)
}

func (s Store) Records() ([]Record, error) {
	file, err := os.Open(s.paths.HistoryFile)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	defer file.Close()

	var records []Record
	scanner := bufio.NewScanner(file)
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		line := scanner.Bytes()
		if len(strings.TrimSpace(string(line))) == 0 {
			continue
		}
		var record Record
		if err := json.Unmarshal(line, &record); err != nil {
			return nil, fmt.Errorf("history line %d: %w", lineNumber, err)
		}
		records = append(records, record)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return records, nil
}

func (s Store) PrintSummary(w io.Writer) error {
	records, err := s.Records()
	if err != nil {
		return err
	}
	if len(records) == 0 {
		fmt.Fprintln(w, "No looptab runs yet.")
		fmt.Fprintf(w, "History will appear after `looptab run` or `looptab run job <id>`.\n")
		return nil
	}

	fmt.Fprintln(w, "Looptab runs")
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "when\tstatus\tduration\tjob\tcwd\treport")
	for _, record := range records {
		fmt.Fprintf(
			tw,
			"%s\t%s\t%s\t%s\t%s\t%s\n",
			formatWhen(record.StartedAt, recordLocation(record, s.location)),
			record.Status,
			formatDuration(record.DurationMillis),
			record.JobID,
			truncate(paths.DisplayPath(record.CWD), 28),
			truncate(record.Summary, 72),
		)
	}
	return tw.Flush()
}

func (s Store) PrintJob(w io.Writer, id string) error {
	records, err := s.Records()
	if err != nil {
		return err
	}

	var matches []Record
	for _, record := range records {
		if record.JobID == id || strings.HasPrefix(record.JobID, id) {
			matches = append(matches, record)
		}
	}
	if len(matches) == 0 {
		return fmt.Errorf("no runs found for job: %s", id)
	}

	jobID := matches[0].JobID
	for _, record := range matches {
		if record.JobID != jobID {
			return fmt.Errorf("job id prefix is ambiguous: %s", id)
		}
	}

	latest := matches[len(matches)-1]
	fmt.Fprintf(w, "Job %s\n", jobID)
	fmt.Fprintf(w, "schedule: %s\n", latest.Schedule)
	if latest.Timezone != "" {
		fmt.Fprintf(w, "timezone: %s\n", latest.Timezone)
	}
	fmt.Fprintf(w, "cwd: %s\n", paths.DisplayPath(latest.CWD))
	fmt.Fprintf(w, "prompt: %s\n\n", latest.Prompt)

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "when\tstatus\tduration\treport")
	for _, record := range matches {
		fmt.Fprintf(
			tw,
			"%s\t%s\t%s\t%s\n",
			formatWhen(record.StartedAt, recordLocation(record, s.location)),
			record.Status,
			formatDuration(record.DurationMillis),
			truncate(record.Summary, 90),
		)
	}
	if err := tw.Flush(); err != nil {
		return err
	}

	if latest.OutputPath != "" {
		fmt.Fprintf(w, "\nlatest output: %s\n", latest.OutputPath)
		if err := printTail(w, latest.OutputPath, 40); err != nil {
			return err
		}
	}
	return nil
}

func Summarize(output, errorText string) string {
	lines := meaningfulLines(output)
	if len(lines) > 0 {
		return compact(lines[len(lines)-1])
	}
	if errorText != "" {
		return compact(errorText)
	}
	return "completed with no output"
}

func (s Store) append(record Record) error {
	file, err := os.OpenFile(s.paths.HistoryFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()

	encoded, err := json.Marshal(record)
	if err != nil {
		return err
	}
	if _, err := file.Write(append(encoded, '\n')); err != nil {
		return err
	}
	return nil
}

func (s Store) writeOutput(runID, output string) (string, error) {
	path := filepath.Join(s.paths.LogDir, runID+".log")
	if err := os.WriteFile(path, []byte(output), 0o600); err != nil {
		return "", err
	}
	return path, nil
}

func runID(jobID string, started time.Time) string {
	return started.UTC().Format("20060102T150405.000000000Z") + "-" + jobID
}

func meaningfulLines(output string) []string {
	ansi := regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`)
	clean := ansi.ReplaceAllString(output, "")
	var lines []string
	for _, line := range strings.Split(clean, "\n") {
		line = compact(line)
		if line == "" {
			continue
		}
		lines = append(lines, line)
	}
	return lines
}

func compact(input string) string {
	return strings.Join(strings.FieldsFunc(input, unicode.IsSpace), " ")
}

func truncate(input string, limit int) string {
	if len(input) <= limit {
		return input
	}
	if limit <= 3 {
		return input[:limit]
	}
	return input[:limit-3] + "..."
}

func formatDuration(ms int64) string {
	if ms <= 0 {
		return "0s"
	}
	duration := time.Duration(ms) * time.Millisecond
	if duration < time.Second {
		return duration.String()
	}
	return duration.Round(time.Second).String()
}

func formatWhen(when time.Time, location *time.Location) string {
	return when.In(location).Format("2006-01-02 15:04:05 MST")
}

func recordLocation(record Record, fallback *time.Location) *time.Location {
	if record.Timezone != "" {
		if location, err := time.LoadLocation(record.Timezone); err == nil {
			return location
		}
	}
	if fallback != nil {
		return fallback
	}
	return time.UTC
}

func printTail(w io.Writer, path string, limit int) error {
	content, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	lines := strings.Split(strings.TrimRight(string(content), "\n"), "\n")
	if len(lines) > limit {
		lines = lines[len(lines)-limit:]
	}
	for _, line := range lines {
		fmt.Fprintf(w, "  %s\n", line)
	}
	return nil
}
