package parser

import (
	"testing"
	"time"
)

func TestParseActionChain(t *testing.T) {
	file, err := Parse(`daily 5am @grok "do something" && notify "something was done"`)
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
	file, err := Parse(`daily 5am @grok "do something" ? notify "something was done" : "something failed"`)
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
	if step.OnSuccess.Command[0] != "notify" || step.OnSuccess.Command[1] != "something was done" {
		t.Fatalf("unexpected success outcome: %v", step.OnSuccess.Command)
	}
	if step.OnFailure.Command[0] != "notify" || step.OnFailure.Command[1] != "something failed" {
		t.Fatalf("unexpected failure outcome: %v", step.OnFailure.Command)
	}
	if step.OnFailure.Command[len(step.OnFailure.Command)-2] != "--urgency" {
		t.Fatalf("expected failure notify to be critical: %v", step.OnFailure.Command)
	}
}

func TestParseOutcomeWithChain(t *testing.T) {
	file, err := Parse(`hourly notify "gdrive" "started" && gdrive sync run ? notify "gdrive" "finished" : notify "gdrive" "failed" --urgency critical`)
	if err != nil {
		t.Fatal(err)
	}
	job := file.Jobs[0]
	if len(job.Steps) != 2 {
		t.Fatalf("expected 2 steps, got %d", len(job.Steps))
	}
	if job.Steps[0].OnSuccess != nil || job.Steps[1].OnSuccess == nil || job.Steps[1].OnFailure == nil {
		t.Fatalf("unexpected outcome placement: %+v", job.Steps)
	}
}

func TestParseEveryIntervalSchedule(t *testing.T) {
	file, err := Parse(`every 30s tm snapshot sessions`)
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