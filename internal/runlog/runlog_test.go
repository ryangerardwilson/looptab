package runlog

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ryangerardwilson/looptab/internal/paths"
)

func TestStorePrintSummary(t *testing.T) {
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

	var out bytes.Buffer
	if err := store.PrintSummary(&out); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	for _, want := range []string{"Looptab runs", "abcd1234", "Updated README and ran tests."} {
		if !strings.Contains(text, want) {
			t.Fatalf("summary missing %q:\n%s", want, text)
		}
	}
}

func TestStoreWriteSummaryFile(t *testing.T) {
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
		Timezone:       "UTC",
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

	path := filepath.Join(temp, "looptab.log")
	if err := store.WriteSummaryFile(path); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "Updated README and ran tests.") {
		t.Fatalf("summary file missing record:\n%s", string(content))
	}
}

func TestSummarizeUsesLastMeaningfulLine(t *testing.T) {
	got := Summarize("starting\n\nChanged files and ran tests.\n", "")
	if got != "Changed files and ran tests." {
		t.Fatalf("unexpected summary: %q", got)
	}
}
