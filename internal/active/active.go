package active

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
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
	path   string
	record Record
}

type Record struct {
	RunID      string    `json:"run_id"`
	JobID      string    `json:"job_id"`
	Line       int       `json:"line"`
	Schedule   string    `json:"schedule"`
	Timezone   string    `json:"timezone,omitempty"`
	CWD        string    `json:"cwd"`
	Kind           string   `json:"kind,omitempty"`
	Prompt         string   `json:"prompt,omitempty"`
	Command        []string `json:"command,omitempty"`
	ActionDisplay  string   `json:"action_display"`
	StartedAt  time.Time `json:"started_at"`
	OutputPath string    `json:"output_path,omitempty"`
	OwnerPID   int       `json:"owner_pid,omitempty"`
	PID        int       `json:"pid,omitempty"`
}

type Job struct {
	Index          int       `json:"index"`
	RunID          string    `json:"run_id"`
	JobID          string    `json:"job_id"`
	Line           int       `json:"line"`
	Schedule       string    `json:"schedule"`
	Timezone       string    `json:"timezone,omitempty"`
	CWD            string    `json:"cwd"`
	CWDDisplay     string    `json:"cwd_display"`
	Kind           string    `json:"kind,omitempty"`
	Prompt         string    `json:"prompt,omitempty"`
	Command        []string  `json:"command,omitempty"`
	ActionDisplay  string    `json:"action_display"`
	StartedAt      time.Time `json:"started_at"`
	DurationMillis int64     `json:"duration_millis"`
	OutputPath     string    `json:"output_path,omitempty"`
	OwnerPID       int       `json:"owner_pid,omitempty"`
	PID            int       `json:"pid"`
	KillPIDs       []int     `json:"-"`
	LegacyNoLive   bool      `json:"legacy_no_live,omitempty"`
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
	runID := runID(job.ID, started)
	outputPath := ""
	if s.paths.LogDir != "" {
		outputPath = filepath.Join(s.paths.LogDir, runID+".log")
		outputFile, err := os.OpenFile(outputPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
		if err != nil {
			return nil, err
		}
		if err := outputFile.Close(); err != nil {
			return nil, err
		}
	}

	record := Record{
		RunID:         runID,
		JobID:         job.ID,
		Line:          job.Line,
		Schedule:      job.Schedule,
		Timezone:      job.Timezone,
		CWD:           job.CWD,
		Kind:          string(job.Kind),
		Prompt:        job.Prompt,
		Command:       job.Command,
		ActionDisplay: job.ActionDisplay(),
		StartedAt:     started,
		OutputPath:    outputPath,
		OwnerPID:      os.Getpid(),
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

	return &Handle{path: path, record: record}, nil
}

func (h *Handle) StartedAt() time.Time {
	if h == nil {
		return time.Time{}
	}
	return h.record.StartedAt
}

func (h *Handle) OutputPath() string {
	if h == nil {
		return ""
	}
	return h.record.OutputPath
}

func (h *Handle) SetPID(pid int) error {
	if h == nil || h.path == "" {
		return nil
	}
	h.record.PID = pid
	return h.write()
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

func (h *Handle) write() error {
	tmp := h.path + ".tmp"
	content, err := json.MarshalIndent(h.record, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(tmp, append(content, '\n'), 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, h.path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func (s Store) Remove(runID string) error {
	if runID == "" {
		return nil
	}
	err := os.Remove(filepath.Join(s.paths.ActiveDir, runID+".json"))
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
		killPIDs := killPIDsForRecord(record)
		if !recordAlive(record, killPIDs) {
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
			Kind:           record.Kind,
			Prompt:         record.Prompt,
			Command:        record.Command,
			ActionDisplay:  actionDisplayForRecord(record),
			StartedAt:      record.StartedAt,
			DurationMillis: now.Sub(record.StartedAt).Milliseconds(),
			OutputPath:     record.OutputPath,
			OwnerPID:       record.OwnerPID,
			PID:            record.PID,
			KillPIDs:       killPIDs,
			LegacyNoLive:   isLegacyNoLiveRecord(record),
		})
	}
	sort.Slice(summary.Jobs, func(i, j int) bool {
		if summary.Jobs[i].StartedAt.Equal(summary.Jobs[j].StartedAt) {
			return summary.Jobs[i].RunID < summary.Jobs[j].RunID
		}
		return summary.Jobs[i].StartedAt.Before(summary.Jobs[j].StartedAt)
	})
	for i := range summary.Jobs {
		summary.Jobs[i].Index = i
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
		fmt.Fprintln(w, "No looptab runs are active.")
		return nil
	}

	fmt.Fprintln(w, "Active looptab runs")
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "index\tduration\tjob\tkind\tcwd\tschedule\taction")
	for _, job := range summary.Jobs {
		fmt.Fprintf(
			tw,
			"%d\t%s\t%s\t%s\t%s\t%s\t%s\n",
			job.Index,
			formatDuration(job.DurationMillis),
			job.JobID,
			job.Kind,
			truncate(job.CWDDisplay, 28),
			truncate(job.Schedule, 22),
			truncate(job.ActionDisplay, 62),
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

func recordAlive(record Record, killPIDs []int) bool {
	if record.PID > 0 && record.OwnerPID > 0 {
		return processAlive(record.PID)
	}
	if record.OwnerPID > 0 {
		return processAlive(record.OwnerPID)
	}
	if isLegacyNoLiveRecord(record) {
		return len(killPIDs) > 0
	}
	return processAlive(record.PID)
}

func killPIDsForRecord(record Record) []int {
	if isLegacyNoLiveRecord(record) {
		return codexDescendantPIDs(record.PID, record.CWD, record.Prompt)
	}
	if record.PID > 0 {
		return []int{record.PID}
	}
	return nil
}

func isLegacyNoLiveRecord(record Record) bool {
	return record.OwnerPID == 0 && record.PID > 0 && record.OutputPath == ""
}

func codexDescendantPIDs(rootPID int, cwd string, prompt string) []int {
	if rootPID <= 0 {
		return nil
	}
	children := processChildren()
	var descendants []int
	var walk func(int)
	walk = func(pid int) {
		for _, child := range children[pid] {
			descendants = append(descendants, child)
			walk(child)
		}
	}
	walk(rootPID)

	var matches []int
	for _, pid := range descendants {
		if matchesCodexCommand(processCmdline(pid), cwd, prompt) {
			matches = append(matches, pid)
		}
	}
	return matches
}

func processChildren() map[int][]int {
	children := map[int][]int{}
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return children
	}
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		ppid, ok := processPPID(pid)
		if !ok {
			continue
		}
		children[ppid] = append(children[ppid], pid)
	}
	return children
}

func processPPID(pid int) (int, bool) {
	content, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "status"))
	if err != nil {
		return 0, false
	}
	for _, line := range strings.Split(string(content), "\n") {
		if !strings.HasPrefix(line, "PPid:") {
			continue
		}
		ppid, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, "PPid:")))
		return ppid, err == nil
	}
	return 0, false
}

func processCmdline(pid int) []string {
	content, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "cmdline"))
	if err != nil {
		return nil
	}
	raw := strings.TrimRight(string(content), "\x00")
	if raw == "" {
		return nil
	}
	return strings.Split(raw, "\x00")
}

func matchesCodexCommand(cmdline []string, cwd string, prompt string) bool {
	if len(cmdline) == 0 {
		return false
	}
	joined := strings.Join(cmdline, "\x00")
	lower := strings.ToLower(joined)
	if !strings.Contains(lower, "codex") || !hasToken(cmdline, "exec") {
		return false
	}
	if cwd != "" && !strings.Contains(joined, cwd) {
		return false
	}
	return prompt == "" || strings.Contains(joined, prompt)
}

func hasToken(values []string, token string) bool {
	for _, value := range values {
		if value == token {
			return true
		}
	}
	return false
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

func actionDisplayForRecord(record Record) string {
	if record.ActionDisplay != "" {
		return record.ActionDisplay
	}
	if len(record.Command) > 0 {
		return strings.Join(record.Command, " ")
	}
	return record.Prompt
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
