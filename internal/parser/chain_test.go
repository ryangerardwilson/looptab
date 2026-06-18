package parser

import (
	"testing"
	"time"
)

func TestParseActionChain(t *testing.T) {
	file, err := Parse(`daily 5am @grok "do something" && notify "something was done"`, "UTC")
	if err != nil {
		t.Fatal(err)
	}
	if len(file.Jobs) != 1 {
		t.Fatalf("expected 1 job, got %d", len(file.Jobs))
	}
	job := file.Jobs[0]
	if len(job.Steps) != 2 {
		t.Fatalf("expected 2 steps, got %d", len(job.Steps))
	}
	if job.Steps[0].Kind != JobKindGrok || job.Steps[1].Kind != JobKindCommand {
		t.Fatalf("unexpected step kinds: %v", job.Steps)
	}
	if job.Steps[1].Command[0] != "notify" {
		t.Fatalf("unexpected notify command: %v", job.Steps[1].Command)
	}
}

func TestParseOutcomeSyntax(t *testing.T) {
	file, err := Parse(`daily 5am @grok "do something" ? notify heading "grok" body "something was done" : "something failed"`, "UTC")
	if err != nil {
		t.Fatal(err)
	}
	job := file.Jobs[0]
	if len(job.Steps) != 1 {
		t.Fatalf("expected 1 step, got %d", len(job.Steps))
	}
	step := job.Steps[0]
	if step.Kind != JobKindGrok || step.OnSuccess == nil || step.OnFailure == nil {
		t.Fatalf("unexpected step: %+v", step)
	}
	wantSuccess := []string{"notify", "heading", "grok", "body", "something was done"}
	if len(step.OnSuccess.Command) != len(wantSuccess) {
		t.Fatalf("unexpected success outcome: %v", step.OnSuccess.Command)
	}
	for i, want := range wantSuccess {
		if step.OnSuccess.Command[i] != want {
			t.Fatalf("success arg %d: expected %q, got %q", i, want, step.OnSuccess.Command[i])
		}
	}
	wantFailure := []string{"notify", "--urgency", "critical", "heading", "looptab", "body", "something failed"}
	for i, want := range wantFailure {
		if step.OnFailure.Command[i] != want {
			t.Fatalf("failure arg %d: expected %q, got %v", i, want, step.OnFailure.Command)
		}
	}
}

func TestParseOutcomeGdriveJob(t *testing.T) {
	file, err := Parse(`hourly gdrive sync run ? notify heading "gdrive" body "backup finished" : notify heading "gdrive" body "backup failed"`, "UTC")
	if err != nil {
		t.Fatal(err)
	}
	job := file.Jobs[0]
	if len(job.Steps) != 1 || job.Steps[0].OnSuccess == nil || job.Steps[0].OnFailure == nil {
		t.Fatalf("unexpected outcome placement: %+v", job.Steps)
	}
}

func TestParseEveryIntervalSchedule(t *testing.T) {
	file, err := Parse(`every 30s tm snapshot sessions`, "UTC")
	if err != nil {
		t.Fatal(err)
	}
	if file.Jobs[0].Interval != 30*time.Second {
		t.Fatalf("unexpected interval: %s", file.Jobs[0].Interval)
	}
	if len(file.Jobs[0].Steps) != 1 || file.Jobs[0].Steps[0].Command[0] != "tm" {
		t.Fatalf("unexpected command: %v", file.Jobs[0].Steps[0].Command)
	}
}