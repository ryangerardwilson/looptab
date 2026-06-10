package scheduler

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/ryangerardwilson/looptab/internal/parser"
	"github.com/ryangerardwilson/looptab/internal/paths"
	"github.com/ryangerardwilson/looptab/internal/runlog"
)

func TestNowJobAlreadyAttemptedForLoad(t *testing.T) {
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
	loadedAt := time.Date(2026, 6, 10, 10, 0, 0, 0, time.UTC)

	if err := store.Save(runlog.SkippedRecord(job, "previous run still active"), ""); err != nil {
		t.Fatal(err)
	}
	attempted, err := nowJobAlreadyAttemptedForLoad(store, job, loadedAt)
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
		StartedAt:      loadedAt.Add(-time.Minute),
		FinishedAt:     loadedAt.Add(-time.Minute),
		DurationMillis: 1,
		Status:         "ok",
		ExitCode:       0,
		Summary:        "completed",
	}
	if err := store.Save(record, "done"); err != nil {
		t.Fatal(err)
	}

	attempted, err = nowJobAlreadyAttemptedForLoad(store, job, loadedAt)
	if err != nil {
		t.Fatal(err)
	}
	if attempted {
		t.Fatal("runs from a previous saved file should not claim this load")
	}

	record.RunID = "run-2"
	record.StartedAt = loadedAt.Add(time.Second)
	record.FinishedAt = loadedAt.Add(time.Second)
	if err := store.Save(record, "done"); err != nil {
		t.Fatal(err)
	}

	attempted, err = nowJobAlreadyAttemptedForLoad(store, job, loadedAt)
	if err != nil {
		t.Fatal(err)
	}
	if !attempted {
		t.Fatal("runs from the same saved file should claim now jobs")
	}
}
