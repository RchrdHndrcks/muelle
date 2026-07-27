package ui

import (
	"errors"
	"os/exec"
	"strconv"
	"strings"
	"testing"
)

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
