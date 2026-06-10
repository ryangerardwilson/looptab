package parser

import (
	"os"
	"strings"
	"testing"
)

func TestParseFile(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}

	jobs, err := ParseFile(`
# comment
0 * * * * ~/Work/example "Review the repo."
@daily "` + home + `/Work/notes" "Summarize \"notes\"."
`)
	if err != nil {
		t.Fatal(err)
	}

	if len(jobs) != 2 {
		t.Fatalf("expected 2 jobs, got %d", len(jobs))
	}
	if jobs[0].Schedule != "0 * * * *" {
		t.Fatalf("unexpected schedule: %s", jobs[0].Schedule)
	}
	if !strings.HasPrefix(jobs[0].CWD, home) {
		t.Fatalf("cwd was not expanded: %s", jobs[0].CWD)
	}
	if jobs[1].Prompt != `Summarize "notes".` {
		t.Fatalf("unexpected prompt: %q", jobs[1].Prompt)
	}
}

func TestParseFileRejectsUnquotedPrompt(t *testing.T) {
	_, err := ParseFile(`0 * * * * ~/Work/example Review the repo.`)
	if err == nil {
		t.Fatal("expected parse error")
	}
	if !strings.Contains(err.Error(), "prompt must be quoted") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseFileRejectsRelativeCWD(t *testing.T) {
	_, err := ParseFile(`0 * * * * ./example "Run tests."`)
	if err == nil {
		t.Fatal("expected parse error")
	}
	if !strings.Contains(err.Error(), "cwd must be absolute") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestFindJobByPrefix(t *testing.T) {
	jobs, err := ParseFile(`0 * * * * ~ "Run tests."`)
	if err != nil {
		t.Fatal(err)
	}
	found, err := FindJob(jobs, jobs[0].ID[:4])
	if err != nil {
		t.Fatal(err)
	}
	if found.ID != jobs[0].ID {
		t.Fatalf("expected %s, got %s", jobs[0].ID, found.ID)
	}
}
