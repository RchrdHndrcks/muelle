package ui

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// A sequence stops at the first failure: updating a stack must not run "up"
// against images a failed pull never delivered.
func TestRunSequenceStopsAtTheFirstFailure(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "second-command-ran")
	runner := Runner{}

	completed, err := runner.RunSequence([][]string{
		{"sh", "-c", "exit 3"},
		{"sh", "-c", "touch " + marker},
	}, false)

	if err == nil {
		t.Fatal("a failing command must be reported")
	}
	if completed != 0 {
		t.Errorf("got %d completed, want 0 — the failing command did not finish", completed)
	}
	if _, statErr := os.Stat(marker); !errors.Is(statErr, os.ErrNotExist) {
		t.Error("the command after the failure must not run")
	}
}

// The completed count is what lets a caller name the step that failed.
func TestRunSequenceReportsHowManyCommandsFinished(t *testing.T) {
	runner := Runner{}

	completed, err := runner.RunSequence([][]string{
		{"sh", "-c", "true"},
		{"sh", "-c", "exit 1"},
	}, false)
	if err == nil || completed != 1 {
		t.Errorf("got %d completed with err %v, want 1 and an error", completed, err)
	}

	completed, err = runner.RunSequence([][]string{
		{"sh", "-c", "true"},
		{"sh", "-c", "true"},
	}, false)
	if err != nil || completed != 2 {
		t.Errorf("got %d completed with err %v, want 2 and no error", completed, err)
	}
}

// The terminal is handed over once for the whole sequence. Suspending and
// resuming per command would repaint the alternate screen over the first
// command's output at the moment the second one starts.
func TestRunSequenceSuspendsTheTerminalOnce(t *testing.T) {
	suspends, resumes := 0, 0
	runner := Runner{
		Suspend: func() error { suspends++; return nil },
		Resume:  func() error { resumes++; return nil },
	}

	if _, err := runner.RunSequence([][]string{
		{"sh", "-c", "true"},
		{"sh", "-c", "true"},
	}, false); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if suspends != 1 || resumes != 1 {
		t.Errorf("got %d suspends and %d resumes, want exactly one of each", suspends, resumes)
	}
}

// A binary missing anywhere in the sequence must be reported before the
// terminal is handed over: a sequence whose later step can never run should
// not start at all.
func TestRunSequenceChecksEveryBinaryBeforeSuspending(t *testing.T) {
	suspended := false
	runner := Runner{
		Suspend: func() error { suspended = true; return nil },
		Resume:  func() error { return nil },
	}

	completed, err := runner.RunSequence([][]string{
		{"sh", "-c", "true"},
		{"muelle-binary-that-does-not-exist"},
	}, false)

	if err == nil {
		t.Fatal("a missing binary must be reported")
	}
	if completed != 0 {
		t.Errorf("got %d completed, want 0 — nothing should run", completed)
	}
	if suspended {
		t.Error("the terminal must not be handed over for a sequence that cannot finish")
	}
}

// A container with no shell answers with an OCI runtime error describing the
// mechanism rather than the situation.
func TestExplainRecognisesAMissingCommand(t *testing.T) {
	// Exit 126 is what docker returns when the thing asked for cannot be run
	// inside the container.
	err := runExitStatus(t, 126)

	hint := explain(err)

	if hint == "" {
		t.Fatal("a missing command should be explained")
	}
	if !strings.Contains(hint, "distroless") {
		t.Errorf("got %q, want the usual cause named", hint)
	}
}

func TestExplainStaysQuietForOtherFailures(t *testing.T) {
	for _, code := range []int{1, 2, 130} {
		if hint := explain(runExitStatus(t, code)); hint != "" {
			t.Errorf("exit %d produced %q; only a missing command should be explained", code, hint)
		}
	}
}

func TestExplainStaysQuietForNonExitErrors(t *testing.T) {
	if hint := explain(errors.New("something else")); hint != "" {
		t.Errorf("got %q, want nothing for an unrelated error", hint)
	}
}

// runExitStatus produces a real ExitError with the given status.
func runExitStatus(t *testing.T, code int) error {
	t.Helper()
	err := exec.Command("sh", "-c", "exit "+strconv.Itoa(code)).Run()
	if err == nil {
		t.Fatalf("expected a non-zero exit for status %d", code)
	}
	return err
}
