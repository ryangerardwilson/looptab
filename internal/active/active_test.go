package active

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ryangerardwilson/looptab/internal/parser"
	"github.com/ryangerardwilson/looptab/internal/paths"
)

func TestStoreTracksActiveJob(t *testing.T) {
	temp := t.TempDir()
	store := NewStore(paths.Paths{
		StateDir:  temp,
		ActiveDir: filepath.Join(temp, "active"),
		LogDir:    filepath.Join(temp, "logs"),
	})

	job := parser.Job{
		ID:       "abcd1234",
		Line:     2,
		Schedule: "daily 11am",
		Timezone: "Asia/Kolkata",
		CWD:      temp,
		Prompt:   "Do useful work.",
	}

	handle, err := store.Begin(job)
	if err != nil {
		t.Fatal(err)
	}

	summary, err := store.Summary()
	if err != nil {
		t.Fatal(err)
	}
	if !summary.Running || summary.Count != 1 {
		t.Fatalf("unexpected summary: %+v", summary)
	}
	if summary.Jobs[0].JobID != "abcd1234" {
		t.Fatalf("unexpected job id: %s", summary.Jobs[0].JobID)
	}
	if summary.Jobs[0].OutputPath == "" {
		t.Fatal("expected output path")
	}
	if _, err := os.Stat(summary.Jobs[0].OutputPath); err != nil {
		t.Fatalf("expected live output file: %v", err)
	}

	var jsonOut bytes.Buffer
	if err := store.PrintJSON(&jsonOut); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(jsonOut.String(), `"running": true`) {
		t.Fatalf("json did not show running state:\n%s", jsonOut.String())
	}

	if err := handle.End(); err != nil {
		t.Fatal(err)
	}

	summary, err = store.Summary()
	if err != nil {
		t.Fatal(err)
	}
	if summary.Running || summary.Count != 0 {
		t.Fatalf("expected inactive summary, got %+v", summary)
	}
}
