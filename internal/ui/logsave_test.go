package ui

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/RchrdHndrcks/muelle/internal/docker"
)

func TestDefaultLogPathNamesContainerAndMoment(t *testing.T) {
	at := time.Date(2026, 8, 5, 14, 30, 9, 0, time.UTC)

	got := defaultLogPath("shop-api", at)

	if want := "./shop-api-20260805-143009.log"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// The prompt exists so the common case is a bare Enter; that only works when
// the pre-filled path already has the final shape.
func TestSaveKeyPrefillsThePromptWithADefaultPath(t *testing.T) {
	app := newTestApp(t)
	app.mode = ModeLogs
	app.logName = "shop-api"

	app.handleKey(context.Background(), runeKey('s'))

	if app.overlay == nil || app.overlay.Kind != OverlayInput {
		t.Fatal("s in the log viewer should open the input overlay")
	}
	shape := regexp.MustCompile(`^\./shop-api-\d{8}-\d{6}\.log$`)
	if !shape.MatchString(app.overlay.Input) {
		t.Errorf("got prefill %q, want ./shop-api-<YYYYMMDD-HHMMSS>.log", app.overlay.Input)
	}
}

// The saved file must be the view, not the stream: filtered, stamped when the
// toggle says so, and free of every escape sequence — including the ones the
// container wrote itself, which the viewer deliberately keeps.
func TestPlainLogLinesApplyFilterAndTimestampsWithoutEscapes(t *testing.T) {
	buffer := NewLogBuffer(10)
	buffer.Append(
		docker.LogLine{
			Stream: docker.StreamStdout,
			Text:   "\x1b[31merror\x1b[0m connecting",
			Time:   time.Date(2026, 8, 5, 10, 11, 12, 0, time.UTC),
		},
		docker.LogLine{Stream: docker.StreamStdout, Text: "healthy again"},
	)

	lines := plainLogLines(buffer.Lines("error"), LogOptions{Width: 10, Wrap: true, Timestamps: true})

	if len(lines) != 1 {
		t.Fatalf("got %d lines, want the filter applied before saving", len(lines))
	}
	if !strings.HasPrefix(lines[0], "10:11:12 ") {
		t.Errorf("got %q, want the timestamp toggle honoured", lines[0])
	}
	if strings.Contains(lines[0], "\x1b") {
		t.Errorf("got %q, want no escape sequences in file output", lines[0])
	}
	if !strings.Contains(lines[0], "error connecting") {
		t.Errorf("got %q, want the text kept once its colouring is gone", lines[0])
	}
	// Wrap was asked for and must be refused: a file has no width to fold
	// at, so each record stays one line.
	if len(plainLogLines(buffer.Lines(""), LogOptions{Width: 5, Wrap: true})) != 2 {
		t.Error("wrapping should not split records in file output")
	}
}

func TestSaveLogsWritesWhatTheViewerShows(t *testing.T) {
	app := newTestApp(t)
	app.logStamps = false
	app.logFilter = "keep"
	app.logs.Append(line("keep me"), line("drop this"), line("keep too"))
	path := filepath.Join(t.TempDir(), "out.log")

	app.saveLogs(path)

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the saved file: %v", err)
	}
	if got, want := string(data), "keep me\nkeep too\n"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if app.status.isError {
		t.Errorf("a successful save reported an error: %s", app.status.text)
	}
	if want := "saved 2 lines to " + path; !strings.Contains(app.status.text, want) {
		t.Errorf("got status %q, want it to contain %q", app.status.text, want)
	}
}

// A missing parent directory must surface as an error, not be created behind
// the user's back: a typo in the path should cost a message, not a mkdir.
func TestSaveLogsReportsWriteFailure(t *testing.T) {
	app := newTestApp(t)
	app.logs.Append(line("hello"))
	path := filepath.Join(t.TempDir(), "no-such-dir", "out.log")

	app.saveLogs(path)

	if !app.status.isError {
		t.Fatalf("got status %q, want a write failure surfaced as an error", app.status.text)
	}
	if !strings.Contains(app.status.text, "save logs") {
		t.Errorf("got status %q, want it to name the failed action", app.status.text)
	}
}
