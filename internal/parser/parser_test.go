package parser

import (
	"os"
	"strings"
	"testing"
)

func TestParseFileHumanSchedules(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}

	file, err := Parse(`
# comment
timezone asia/kolkata
daily 11am,12pm,1pm ~/Work/example "Review the repo."
weekdays 9:30am "` + home + `/Work/notes" "Summarize \"notes\"."
weekends 5am /tmp "Clean temp notes."
mondays 17:15 /tmp "Prepare the weekly review."
now "Run once from home."
`)
	if err != nil {
		t.Fatal(err)
	}

	if file.Timezone != "Asia/Kolkata" {
		t.Fatalf("unexpected timezone: %s", file.Timezone)
	}
	if len(file.Jobs) != 5 {
		t.Fatalf("expected 5 jobs, got %d", len(file.Jobs))
	}
	if file.Jobs[0].Schedule != "daily 11am,12pm,1pm" {
		t.Fatalf("unexpected schedule: %s", file.Jobs[0].Schedule)
	}
	wantSpecs := []string{"0 11 * * *", "0 12 * * *", "0 13 * * *"}
	for i, want := range wantSpecs {
		if file.Jobs[0].CronSpecs[i] != want {
			t.Fatalf("cron spec %d: expected %q, got %q", i, want, file.Jobs[0].CronSpecs[i])
		}
	}
	if !strings.HasPrefix(file.Jobs[0].CWD, home) {
		t.Fatalf("cwd was not expanded: %s", file.Jobs[0].CWD)
	}
	if file.Jobs[1].CronSpecs[0] != "30 9 * * 1-5" {
		t.Fatalf("unexpected weekday spec: %s", file.Jobs[1].CronSpecs[0])
	}
	if file.Jobs[1].Prompt != `Summarize "notes".` {
		t.Fatalf("unexpected prompt: %q", file.Jobs[1].Prompt)
	}
	if file.Jobs[2].CronSpecs[0] != "0 5 * * 0,6" {
		t.Fatalf("unexpected weekend spec: %s", file.Jobs[2].CronSpecs[0])
	}
	if file.Jobs[3].CronSpecs[0] != "15 17 * * 1" {
		t.Fatalf("unexpected monday spec: %s", file.Jobs[3].CronSpecs[0])
	}
	if !file.Jobs[4].Once {
		t.Fatal("expected now job to be marked once")
	}
	if file.Jobs[4].Schedule != "now" {
		t.Fatalf("unexpected now schedule: %s", file.Jobs[4].Schedule)
	}
	if file.Jobs[4].CWD != home {
		t.Fatalf("expected default cwd %s, got %s", home, file.Jobs[4].CWD)
	}
}

func TestParseFileDefaultsToUTC(t *testing.T) {
	file, err := Parse(`daily 11am "Run tests."`)
	if err != nil {
		t.Fatal(err)
	}
	if file.Timezone != "UTC" {
		t.Fatalf("expected UTC, got %s", file.Timezone)
	}
	if file.Location.String() != "UTC" {
		t.Fatalf("expected UTC location, got %s", file.Location)
	}
}

func TestParseFileUsesHomeWhenCWDIsOmitted(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}

	file, err := Parse(`weekdays 9am "Plan the day."`)
	if err != nil {
		t.Fatal(err)
	}
	if file.Jobs[0].CWD != home {
		t.Fatalf("expected cwd %s, got %s", home, file.Jobs[0].CWD)
	}
	if file.Jobs[0].Schedule != "weekdays 9am" {
		t.Fatalf("unexpected schedule: %s", file.Jobs[0].Schedule)
	}
}

func TestParseFileSupportsHourlySchedules(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}

	file, err := Parse(`
hourly "Run from home."
hourly at 15 ~/Work/example "Run from repo."
`)
	if err != nil {
		t.Fatal(err)
	}
	if len(file.Jobs) != 2 {
		t.Fatalf("expected 2 jobs, got %d", len(file.Jobs))
	}
	if file.Jobs[0].Schedule != "hourly" {
		t.Fatalf("unexpected schedule: %s", file.Jobs[0].Schedule)
	}
	if file.Jobs[0].CronSpecs[0] != "0 * * * *" {
		t.Fatalf("unexpected hourly spec: %s", file.Jobs[0].CronSpecs[0])
	}
	if file.Jobs[0].CWD != home {
		t.Fatalf("expected cwd %s, got %s", home, file.Jobs[0].CWD)
	}
	if file.Jobs[1].Schedule != "hourly at 15" {
		t.Fatalf("unexpected schedule: %s", file.Jobs[1].Schedule)
	}
	if file.Jobs[1].CronSpecs[0] != "15 * * * *" {
		t.Fatalf("unexpected hourly at spec: %s", file.Jobs[1].CronSpecs[0])
	}
}

func TestParseFileRejectsOldCronSyntax(t *testing.T) {
	_, err := Parse(`0 * * * * ~/Work/example "Review the repo."`)
	if err == nil {
		t.Fatal("expected parse error")
	}
	if !strings.Contains(err.Error(), "unknown schedule") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseFileRejectsUnquotedPrompt(t *testing.T) {
	_, err := Parse(`daily 11am ~/Work/example Review the repo.`)
	if err == nil {
		t.Fatal("expected parse error")
	}
	if !strings.Contains(err.Error(), "prompt must be quoted") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseFileRejectsRelativeCWD(t *testing.T) {
	_, err := Parse(`daily 11am ./example "Run tests."`)
	if err == nil {
		t.Fatal("expected parse error")
	}
	if !strings.Contains(err.Error(), "cwd must be absolute or start with ~") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseFileRejectsNowWithTime(t *testing.T) {
	_, err := Parse(`now 11am "Run tests."`)
	if err == nil {
		t.Fatal("expected parse error")
	}
	if !strings.Contains(err.Error(), "now does not accept a time") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseFileRejectsBadHourlyMinute(t *testing.T) {
	_, err := Parse(`hourly at 60 "Run tests."`)
	if err == nil {
		t.Fatal("expected parse error")
	}
	if !strings.Contains(err.Error(), "invalid hourly minute") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseFileRejectsTimezoneAfterJobs(t *testing.T) {
	_, err := Parse(`
daily 11am ~ "Run tests."
timezone Asia/Kolkata
`)
	if err == nil {
		t.Fatal("expected parse error")
	}
	if !strings.Contains(err.Error(), "timezone must appear before jobs") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestFindJobByPrefix(t *testing.T) {
	jobs, err := ParseFile(`daily 11am "Run tests."`)
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
