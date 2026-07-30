package ui

import (
	"slices"
	"strings"
	"testing"
)

// The configured editor is a deliberate choice made in muelle's own settings,
// so it outranks whatever the shell happens to export.
func TestEditorArgvPrefersTheConfiguredEditor(t *testing.T) {
	t.Setenv("VISUAL", "vim")
	t.Setenv("EDITOR", "nano")

	got := EditorArgv("helix", "/srv/shop/docker-compose.yml")

	want := []string{"helix", "/srv/shop/docker-compose.yml"}
	if !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// VISUAL is the full-screen editor and EDITOR the line editor, which is the
// distinction that matters when the terminal has just been handed over.
func TestEditorArgvPrefersVisualOverEditor(t *testing.T) {
	t.Setenv("VISUAL", "vim")
	t.Setenv("EDITOR", "ed")

	if got := EditorArgv("", "f.yml"); !slices.Equal(got, []string{"vim", "f.yml"}) {
		t.Errorf("got %v, want VISUAL to win", got)
	}
}

func TestEditorArgvFallsBackToEditor(t *testing.T) {
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", "nano")

	if got := EditorArgv("", "f.yml"); !slices.Equal(got, []string{"nano", "f.yml"}) {
		t.Errorf("got %v, want EDITOR to be used", got)
	}
}

// A server with no EDITOR set is common, and vi is on every one of them.
func TestEditorArgvFallsBackToVi(t *testing.T) {
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", "")

	if got := EditorArgv("", "f.yml"); !slices.Equal(got, []string{"vi", "f.yml"}) {
		t.Errorf("got %v, want vi as the last resort", got)
	}
}

// Editors are routinely configured with flags — "code --wait" is the only
// form of a graphical editor that works here at all, since without it the
// process returns before the file has been touched.
func TestEditorArgvKeepsTheEditorsOwnFlags(t *testing.T) {
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", "code --wait")

	got := EditorArgv("", "f.yml")

	want := []string{"code", "--wait", "f.yml"}
	if !slices.Equal(got, want) {
		t.Errorf("got %v, want the flags preserved before the path", got)
	}
}

// Validating a compose file means reading what Compose has to say about it,
// which rules out handing it the terminal the way Run does.
func TestCaptureReturnsTheCommandsOutput(t *testing.T) {
	var runner Runner

	out, err := runner.Capture([]string{"sh", "-c", "echo hello"})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.TrimSpace(out) != "hello" {
		t.Errorf("got %q, want the command's output", out)
	}
}

// Compose reports a bad compose file on stderr and exits non-zero. Both parts
// matter: the status says it failed, the output says why.
func TestCaptureReturnsStderrAlongsideTheFailure(t *testing.T) {
	var runner Runner

	out, err := runner.Capture([]string{"sh", "-c", "echo 'invalid port' >&2; exit 1"})

	if err == nil {
		t.Fatal("a non-zero exit must be reported as an error")
	}
	if !strings.Contains(out, "invalid port") {
		t.Errorf("got %q, want the diagnostic the command wrote to stderr", out)
	}
}

// Capture must not suspend the TUI: nothing it runs draws on the terminal.
func TestCaptureLeavesTheTerminalAlone(t *testing.T) {
	suspended := false
	runner := Runner{
		Suspend: func() error { suspended = true; return nil },
		Resume:  func() error { return nil },
	}

	if _, err := runner.Capture([]string{"sh", "-c", "true"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if suspended {
		t.Error("capturing output must not hand over the terminal")
	}
}
