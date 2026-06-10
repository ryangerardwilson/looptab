package app

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ryangerardwilson/looptab/internal/active"
	"github.com/ryangerardwilson/looptab/internal/parser"
	"github.com/ryangerardwilson/looptab/internal/paths"
)

type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func TestLastOutputLines(t *testing.T) {
	got := string(lastOutputLines([]byte("one\ntwo\nthree\n"), 2))
	if got != "two\nthree\n" {
		t.Fatalf("unexpected tail: %q", got)
	}
}

func TestWriteLabeledOutputPreservesPartialLines(t *testing.T) {
	var out bytes.Buffer
	atLineStart, err := writeLabeledOutput(&out, "abcd1234", []byte("one\npartial"), true)
	if err != nil {
		t.Fatal(err)
	}
	if atLineStart {
		t.Fatal("expected partial line state")
	}
	atLineStart, err = writeLabeledOutput(&out, "abcd1234", []byte(" done\n"), atLineStart)
	if err != nil {
		t.Fatal(err)
	}
	if !atLineStart {
		t.Fatal("expected line-start state after newline")
	}

	want := "[abcd1234] one\n[abcd1234] partial done\n"
	if out.String() != want {
		t.Fatalf("unexpected labeled output:\n%s", out.String())
	}
}

func TestStreamActiveRunsStreamsActiveOutput(t *testing.T) {
	temp := t.TempDir()
	p := paths.Paths{
		StateDir:  temp,
		ActiveDir: filepath.Join(temp, "active"),
		LogDir:    filepath.Join(temp, "logs"),
	}
	store := active.NewStore(p)
	job := parser.Job{
		ID:       "abcd1234",
		Line:     1,
		Schedule: "now",
		CWD:      temp,
		Prompt:   "Do useful work.",
	}

	handle, err := store.Begin(job)
	if err != nil {
		t.Fatal(err)
	}
	defer handle.End()

	if err := os.WriteFile(handle.OutputPath(), []byte("first\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var out lockedBuffer
	done := make(chan error, 1)
	go func() {
		done <- streamActiveRuns(ctx, store, &out, 10*time.Millisecond)
	}()

	waitForOutput(t, &out, "[abcd1234] first\n")
	appendOutput(t, handle.OutputPath(), "second\n")
	waitForOutput(t, &out, "[abcd1234] second\n")

	if err := handle.End(); err != nil {
		t.Fatal(err)
	}
	waitForOutput(t, &out, "[abcd1234] finished")

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("stream did not stop after cancellation")
	}
}

func appendOutput(t *testing.T, path string, text string) {
	t.Helper()

	file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()

	if _, err := file.WriteString(text); err != nil {
		t.Fatal(err)
	}
}

func waitForOutput(t *testing.T, out *lockedBuffer, want string) {
	t.Helper()

	deadline := time.After(2 * time.Second)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()

	for {
		if strings.Contains(out.String(), want) {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for %q in:\n%s", want, out.String())
		case <-ticker.C:
		}
	}
}
