package oncejob

import (
	"path/filepath"
	"testing"

	"github.com/ryangerardwilson/looptab/internal/parser"
	"github.com/ryangerardwilson/looptab/internal/paths"
)

func TestStoreClaimsOncePerJobID(t *testing.T) {
	temp := t.TempDir()
	store := NewStore(paths.Paths{
		StateDir:  temp,
		ActiveDir: filepath.Join(temp, "active"),
		LogDir:    filepath.Join(temp, "logs"),
	})
	job := parser.Job{
		ID:       "abcd1234",
		Line:     1,
		Schedule: "now",
		CWD:      temp,
		Prompt:   "Run once.",
	}

	claimed, err := store.Claim(job)
	if err != nil {
		t.Fatal(err)
	}
	if !claimed {
		t.Fatal("expected first claim to run")
	}

	claimed, err = store.Claim(job)
	if err != nil {
		t.Fatal(err)
	}
	if claimed {
		t.Fatal("expected duplicate claim to be skipped")
	}

	job.ID = "efgh5678"
	claimed, err = store.Claim(job)
	if err != nil {
		t.Fatal(err)
	}
	if !claimed {
		t.Fatal("expected changed job to be claimed")
	}
}
