package active

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/ryangerardwilson/looptab/internal/parser"
	"github.com/ryangerardwilson/looptab/internal/paths"
)

type Store struct {
	paths paths.Paths
}

type Handle struct {
	path string
}

type Record struct {
	RunID     string    `json:"run_id"`
	JobID     string    `json:"job_id"`
	Line      int       `json:"line"`
	Schedule  string    `json:"schedule"`
	Timezone  string    `json:"timezone,omitempty"`
	CWD       string    `json:"cwd"`
	Prompt    string    `json:"prompt"`
	StartedAt time.Time `json:"started_at"`
	PID       int       `json:"pid"`
}

type Job struct {
	RunID          string    `json:"run_id"`
	JobID          string    `json:"job_id"`
	Line           int       `json:"line"`
	Schedule       string    `json:"schedule"`
	Timezone       string    `json:"timezone,omitempty"`
	CWD            string    `json:"cwd"`
	CWDDisplay     string    `json:"cwd_display"`
	Prompt         string    `json:"prompt"`
	StartedAt      time.Time `json:"started_at"`
	DurationMillis int64     `json:"duration_millis"`
	PID            int       `json:"pid"`
}

type Summary struct {
	Running   bool      `json:"running"`
	Count     int       `json:"count"`
	Jobs      []Job     `json:"jobs"`
	UpdatedAt time.Time `json:"updated_at"`
}

func NewStore(p paths.Paths) Store {
	return Store{paths: p}
}

func (s Store) Begin(job parser.Job) (*Handle, error) {
	if err := paths.EnsureState(s.paths); err != nil {
		return nil, err
	}

	started := time.Now()
	record := Record{
		RunID:     runID(job.ID, started),
		JobID:     job.ID,
		Line:      job.Line,
		Schedule:  job.Schedule,
		Timezone:  job.Timezone,
		CWD:       job.CWD,
		Prompt:    job.Prompt,
		StartedAt: started,
		PID:       os.Getpid(),
	}

	path := filepath.Join(s.paths.ActiveDir, record.RunID+".json")
	tmp := path + ".tmp"
	content, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(tmp, append(content, '\n'), 0o600); err != nil {
		return nil, err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return nil, err
	}

	return &Handle{path: path}, nil
}

func (h *Handle) End() error {
	if h == nil || h.path == "" {
		return nil
	}
	err := os.Remove(h.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func (s Store) Summary() (Summary, error) {
	if err := paths.EnsureState(s.paths); err != nil {
		return Summary{}, err
	}

	entries, err := os.ReadDir(s.paths.ActiveDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Summary{Jobs: []Job{}, UpdatedAt: time.Now()}, nil
		}
		return Summary{}, err
	}

	now := time.Now()
	summary := Summary{Jobs: []Job{}, UpdatedAt: now}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(s.paths.ActiveDir, entry.Name())
		record, err := readRecord(path)
		if err != nil {
			continue
		}
		if record.PID > 0 && !processAlive(record.PID) {
			_ = os.Remove(path)
			continue
		}
		summary.Jobs = append(summary.Jobs, Job{
			RunID:          record.RunID,
			JobID:          record.JobID,
			Line:           record.Line,
			Schedule:       record.Schedule,
			Timezone:       record.Timezone,
			CWD:            record.CWD,
			CWDDisplay:     paths.DisplayPath(record.CWD),
			Prompt:         record.Prompt,
			StartedAt:      record.StartedAt,
			DurationMillis: now.Sub(record.StartedAt).Milliseconds(),
			PID:            record.PID,
		})
	}
	summary.Count = len(summary.Jobs)
	summary.Running = summary.Count > 0
	return summary, nil
}

func (s Store) Print(w io.Writer) error {
	summary, err := s.Summary()
	if err != nil {
		return err
	}
	if !summary.Running {
		fmt.Fprintln(w, "No looptab Codex runs are active.")
		return nil
	}

	fmt.Fprintln(w, "Active looptab runs")
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "duration\tjob\tcwd\tschedule\tprompt")
	for _, job := range summary.Jobs {
		fmt.Fprintf(
			tw,
			"%s\t%s\t%s\t%s\t%s\n",
			formatDuration(job.DurationMillis),
			job.JobID,
			truncate(job.CWDDisplay, 28),
			truncate(job.Schedule, 22),
			truncate(job.Prompt, 62),
		)
	}
	return tw.Flush()
}

func (s Store) PrintJSON(w io.Writer) error {
	summary, err := s.Summary()
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(summary)
}

func readRecord(path string) (Record, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return Record{}, err
	}
	var record Record
	if err := json.Unmarshal(content, &record); err != nil {
		return Record{}, err
	}
	return record, nil
}

func processAlive(pid int) bool {
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = process.Signal(syscall.Signal(0))
	return err == nil || errors.Is(err, syscall.EPERM)
}

func runID(jobID string, started time.Time) string {
	return started.UTC().Format("20060102T150405.000000000Z") + "-" + jobID
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

func truncate(input string, limit int) string {
	if len(input) <= limit {
		return input
	}
	if limit <= 3 {
		return input[:limit]
	}
	return input[:limit-3] + "..."
}
