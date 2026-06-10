package oncejob

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/ryangerardwilson/looptab/internal/parser"
	"github.com/ryangerardwilson/looptab/internal/paths"
)

func TestStoreClaimsOncePerJobPerLoad(t *testing.T) {
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
	firstLoad := time.Date(2026, 6, 10, 1, 0, 0, 0, time.UTC)
	secondLoad := time.Date(2026, 6, 10, 1, 1, 0, 0, time.UTC)

	claimed, err := store.Claim(job, firstLoad)
	if err != nil {
		t.Fatal(err)
	}
	if !claimed {
		t.Fatal("expected first claim to run")
	}

	claimed, err = store.Claim(job, firstLoad)
	if err != nil {
		t.Fatal(err)
	}
	if claimed {
		t.Fatal("expected duplicate claim to be skipped")
	}

	claimed, err = store.Claim(job, secondLoad)
	if err != nil {
		t.Fatal(err)
	}
	if !claimed {
		t.Fatal("expected same job in a new load to be claimed")
	}
}

func TestStoreMigratesLegacyJobClaimOnlyForCurrentLoad(t *testing.T) {
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
	legacyClaimedAt := time.Date(2026, 6, 10, 1, 0, 0, 0, time.UTC)
	state := State{
		Jobs: map[string]Claim{
			job.ID: {
				JobID:     job.ID,
				ClaimedAt: legacyClaimedAt,
			},
		},
	}
	if err := store.write(state); err != nil {
		t.Fatal(err)
	}

	claimed, err := store.Claim(job, legacyClaimedAt.Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if claimed {
		t.Fatal("legacy claim should block the already-loaded file")
	}

	claimed, err = store.Claim(job, legacyClaimedAt.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if !claimed {
		t.Fatal("legacy claim should not block a later saved file")
	}
}
