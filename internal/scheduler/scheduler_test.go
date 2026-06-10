package scheduler

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/ryangerardwilson/looptab/internal/parser"
	"github.com/ryangerardwilson/looptab/internal/paths"
	"github.com/ryangerardwilson/looptab/internal/runlog"
)

func TestNowJobAlreadyAttemptedIgnoresSkippedRuns(t *testing.T) {
	temp := t.TempDir()
	p := paths.Paths{
		StateDir:    temp,
		LogDir:      filepath.Join(temp, "logs"),
		HistoryFile: filepath.Join(temp, "runs.jsonl"),
	}
	store := runlog.NewStore(p)
	job := parser.Job{
		ID:       "abcd1234",
		Line:     1,
		Schedule: "now",
		CWD:      temp,
		Prompt:   "Run once.",
	}

	if err := store.Save(runlog.SkippedRecord(job, "previous run still active"), ""); err != nil {
		t.Fatal(err)
	}
	attempted, err := nowJobAlreadyAttempted(store, job)
	if err != nil {
		t.Fatal(err)
	}
	if attempted {
		t.Fatal("skipped runs should not claim now jobs")
	}

	record := runlog.Record{
		RunID:          "run-1",
		JobID:          job.ID,
		Line:           job.Line,
		Schedule:       job.Schedule,
		CWD:            job.CWD,
		Prompt:         job.Prompt,
		StartedAt:      time.Now(),
		FinishedAt:     time.Now(),
		DurationMillis: 1,
		Status:         "ok",
		ExitCode:       0,
		Summary:        "completed",
	}
	if err := store.Save(record, "done"); err != nil {
		t.Fatal(err)
	}

	attempted, err = nowJobAlreadyAttempted(store, job)
	if err != nil {
		t.Fatal(err)
	}
	if !attempted {
		t.Fatal("completed runs should claim now jobs")
	}
}
