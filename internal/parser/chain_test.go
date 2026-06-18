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