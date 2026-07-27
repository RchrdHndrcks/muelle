package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/RchrdHndrcks/muelle/internal/docker"
	"github.com/RchrdHndrcks/muelle/internal/tui"
)

// plainStyle renders without escape sequences, so tests compare text.
func plainStyle(_ tui.Style, text string) string { return text }

func line(text string) docker.LogLine {
	return docker.LogLine{Stream: docker.StreamStdout, Text: text}
}

func TestLogBufferKeepsLinesInOrder(t *testing.T) {
	buffer := NewLogBuffer(10)

	buffer.Append(line("first"), line("second"))

	lines := buffer.Lines("")
	if len(lines) != 2 || lines[0].Text != "first" || lines[1].Text != "second" {
		t.Errorf("got %+v, want the lines in arrival order", lines)
	}
}

// Without eviction a chatty container would grow the process until it died.
func TestLogBufferEvictsOldestWhenFull(t *testing.T) {
	buffer := NewLogBuffer(3)

	buffer.Append(line("one"), line("two"), line("three"), line("four"), line("five"))

	lines := buffer.Lines("")
	if len(lines) != 3 {
		t.Fatalf("got %d lines, want the capacity respected", len(lines))
	}
	if lines[0].Text != "three" || lines[2].Text != "five" {
		t.Errorf("got %v, want the three most recent", texts(lines))
	}
	if buffer.Dropped() != 2 {
		t.Errorf("got %d dropped, want 2 so the UI can say history is incomplete", buffer.Dropped())
	}
}

func TestLogBufferResetClearsCountersToo(t *testing.T) {
	buffer := NewLogBuffer(2)
	buffer.Append(line("a"), line("b"), line("c"))

	buffer.Reset()

	if buffer.Len() != 0 || buffer.Dropped() != 0 {
		t.Errorf("got %d lines and %d dropped, want both zero", buffer.Len(), buffer.Dropped())
	}
}

func TestLogBufferFilterIsCaseInsensitive(t *testing.T) {
	buffer := NewLogBuffer(10)
	buffer.Append(line("ERROR connection refused"), line("info listening"), line("error retrying"))

	matched := buffer.Lines("ERROR")

	if len(matched) != 2 {
		t.Fatalf("got %v, want both error lines regardless of case", texts(matched))
	}
}

func TestLogBufferEmptyFilterReturnsEverything(t *testing.T) {
	buffer := NewLogBuffer(10)
	buffer.Append(line("a"), line("b"))

	if got := len(buffer.Lines("")); got != 2 {
		t.Errorf("got %d lines, want all of them", got)
	}
}

func TestRenderLogsMarksStderr(t *testing.T) {
	lines := []docker.LogLine{
		{Stream: docker.StreamStderr, Text: "panic: boom"},
	}
	var styled bool
	style := func(s tui.Style, text string) string {
		if s == styleStderr {
			styled = true
		}
		return text
	}

	RenderLogs(lines, 80, false, false, style)

	if !styled {
		t.Error("stderr lines should be styled differently from stdout")
	}
}

func TestRenderLogsShowsTimestampsWhenEnabled(t *testing.T) {
	stamped := docker.LogLine{
		Stream: docker.StreamStdout,
		Text:   "ready",
		Time:   time.Date(2026, 7, 27, 10, 11, 12, 0, time.UTC),
	}

	withStamps := RenderLogs([]docker.LogLine{stamped}, 80, false, true, plainStyle)
	without := RenderLogs([]docker.LogLine{stamped}, 80, false, false, plainStyle)

	if !strings.Contains(withStamps[0], "10:11:12") {
		t.Errorf("got %q, want the timestamp shown", withStamps[0])
	}
	if strings.Contains(without[0], "10:11:12") {
		t.Errorf("got %q, want no timestamp when disabled", without[0])
	}
}

// Wrapping must happen here, not in the terminal, or the scroll position
// drifts out of step with the content.
func TestRenderLogsWrapsLongLinesIntoMultipleRows(t *testing.T) {
	long := strings.Repeat("x", 250)

	rows := RenderLogs([]docker.LogLine{line(long)}, 100, true, false, plainStyle)

	if len(rows) != 3 {
		t.Fatalf("got %d rows for 250 chars at width 100, want 3", len(rows))
	}
	for i, row := range rows {
		if tui.VisibleWidth(row) > 100 {
			t.Errorf("row %d is %d cells wide, want at most 100", i, tui.VisibleWidth(row))
		}
	}
}

func TestRenderLogsWithoutWrapKeepsOneRowPerLine(t *testing.T) {
	long := strings.Repeat("x", 250)

	rows := RenderLogs([]docker.LogLine{line(long)}, 100, false, false, plainStyle)

	if len(rows) != 1 {
		t.Errorf("got %d rows, want the line left intact for horizontal reading", len(rows))
	}
}

// Control characters in log output would otherwise move the cursor and
// scribble over the frame.
func TestRenderLogsSanitizesControlCharacters(t *testing.T) {
	rows := RenderLogs([]docker.LogLine{line("progress\rdone\x07")}, 80, false, false, plainStyle)

	if strings.ContainsAny(rows[0], "\r\x07") {
		t.Errorf("got %q, want control characters stripped", rows[0])
	}
}

func TestRenderLogsHandlesEmptyInput(t *testing.T) {
	if rows := RenderLogs(nil, 80, true, true, plainStyle); len(rows) != 0 {
		t.Errorf("got %v, want no rows", rows)
	}
}

// A pathological width must not spin forever building segments.
func TestRenderLogsSurvivesDegenerateWidth(t *testing.T) {
	done := make(chan []string, 1)
	go func() {
		done <- RenderLogs([]docker.LogLine{line("hello world")}, 0, true, false, plainStyle)
	}()

	select {
	case rows := <-done:
		if len(rows) == 0 {
			t.Error("expected some output even at a degenerate width")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("RenderLogs did not terminate at width 0")
	}
}

func texts(lines []docker.LogLine) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = l.Text
	}
	return out
}
