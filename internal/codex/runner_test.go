package codex

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ryangerardwilson/looptab/internal/parser"
)

func TestRunnerInvokesCodexExecWithoutShell(t *testing.T) {
	temp := t.TempDir()
	workdir := filepath.Join(temp, "work")
	if err := os.Mkdir(workdir, 0o700); err != nil {
		t.Fatal(err)
	}

	fake := filepath.Join(temp, "codex")
	script := `#!/usr/bin/env sh
printf 'cwd=%s\n' "$PWD"
printf 'args='
for arg do
  printf '[%s]' "$arg"
done
printf '\n'
printf 'Codex work summary.\n'
`
	if err := os.WriteFile(fake, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}

	job := parser.Job{
		ID:       "abcd1234",
		Line:     1,
		Schedule: "daily 11am",
		Timezone: "UTC",
		CWD:      workdir,
		Prompt:   "Review the repo.",
	}

	result := Runner{Bin: fake}.Run(context.Background(), job)
	if result.Err != nil {
		t.Fatal(result.Err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", result.ExitCode)
	}

	for _, want := range []string{
		"cwd=" + workdir,
		"args=[exec][--color][never][--cd][" + workdir + "][Review the repo.]",
		"Codex work summary.",
	} {
		if !strings.Contains(result.Output, want) {
			t.Fatalf("output missing %q:\n%s", want, result.Output)
		}
	}
}
