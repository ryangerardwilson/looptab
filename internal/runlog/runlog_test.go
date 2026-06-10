package runlog

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ryangerardwilson/looptab/internal/paths"
)

func TestStoreSaveAndRecords(t *testing.T) {
	temp := t.TempDir()
	p := paths.Paths{
		StateDir:    temp,
		LogDir:      filepath.Join(temp, "logs"),
		HistoryFile: filepath.Join(temp, "runs.jsonl"),
	}
	store := NewStore(p)

	record := Record{
		RunID:          "run-1",
		JobID:          "abcd1234",
		Schedule:       "daily 11am",
		Timezone:       "Asia/Kolkata",
		CWD:            temp,
		Prompt:         "Do useful work.",
		StartedAt:      time.Date(2026, 6, 10, 10, 0, 0, 0, time.UTC),
		FinishedAt:     time.Date(2026, 6, 10, 10, 1, 0, 0, time.UTC),
		DurationMillis: int64(time.Minute / time.Millisecond),
		Status:         "ok",
		ExitCode:       0,
		Summary:        "Updated README and ran tests.",
	}
	if err := store.Save(record, "full output"); err != nil {
		t.Fatal(err)
	}

	records, err := store.Records()
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}
	if records[0].JobID != "abcd1234" {
		t.Fatalf("unexpected job id: %s", records[0].JobID)
	}
	content, err := os.ReadFile(records[0].OutputPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "full output" {
		t.Fatalf("unexpected output file content: %q", string(content))
	}
}

func TestSummarizeUsesLastMeaningfulLine(t *testing.T) {
	got := Summarize("starting\n\nChanged files and ran tests.\n", "")
	if got != "Changed files and ran tests." {
		t.Fatalf("unexpected summary: %q", got)
	}
}
