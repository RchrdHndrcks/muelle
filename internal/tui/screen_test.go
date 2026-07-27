package tui

import (
	"bytes"
	"strings"
	"testing"
)

func TestRenderEmitsOneFramePerCall(t *testing.T) {
	var out bytes.Buffer
	screen := NewScreen(&out, 20, 3, true)

	if err := screen.Render([]string{"first", "second"}); err != nil {
		t.Fatalf("Render: %v", err)
	}

	frame := out.String()
	if !strings.HasPrefix(frame, SyncOn) || !strings.HasSuffix(frame, SyncOff) {
		t.Error("frame should be bracketed by synchronized-output markers")
	}
	if !strings.Contains(frame, "first") || !strings.Contains(frame, "second") {
		t.Errorf("frame is missing content: %q", frame)
	}
	if strings.Count(frame, ClearLine) != 3 {
		t.Errorf("got %d line clears, want one per screen row so stale cells cannot survive",
			strings.Count(frame, ClearLine))
	}
}

// Fewer lines than rows must still clear the remaining rows, or content from
// the previous frame stays on screen.
func TestRenderClearsRowsBeyondContent(t *testing.T) {
	var out bytes.Buffer
	screen := NewScreen(&out, 20, 5, true)

	if err := screen.Render([]string{"only one"}); err != nil {
		t.Fatalf("Render: %v", err)
	}

	if got := strings.Count(out.String(), ClearLine); got != 5 {
		t.Errorf("got %d line clears, want 5", got)
	}
}

func TestRenderTruncatesToScreenWidth(t *testing.T) {
	var out bytes.Buffer
	screen := NewScreen(&out, 10, 1, true)

	if err := screen.Render([]string{"this line is far too long for the screen"}); err != nil {
		t.Fatalf("Render: %v", err)
	}

	body := strings.TrimSuffix(strings.TrimPrefix(out.String(), SyncOn+CursorHome), ClearLine+SyncOff)
	if VisibleWidth(body) != 10 {
		t.Errorf("got %q (width %d), want it clipped to 10 cells", body, VisibleWidth(body))
	}
}

func TestRenderDropsLinesBeyondScreenHeight(t *testing.T) {
	var out bytes.Buffer
	screen := NewScreen(&out, 20, 2, true)

	if err := screen.Render([]string{"one", "two", "three"}); err != nil {
		t.Fatalf("Render: %v", err)
	}

	if strings.Contains(out.String(), "three") {
		t.Error("lines past the screen height should be dropped, not wrapped")
	}
}

func TestStyleIsSuppressedWhenColourIsOff(t *testing.T) {
	var out bytes.Buffer
	screen := NewScreen(&out, 20, 2, false)

	if got := screen.Style(StyleBold, "text"); got != "text" {
		t.Errorf("got %q, want the text unstyled when colour is disabled", got)
	}
}

func TestStyleWrapsWithReset(t *testing.T) {
	var out bytes.Buffer
	screen := NewScreen(&out, 20, 2, true)

	got := screen.Style(StyleBold, "text")

	if got != string(StyleBold)+"text"+Reset {
		t.Errorf("got %q, want the text wrapped in bold and a reset", got)
	}
}

// A terminal reporting a degenerate size must not drive layout arithmetic
// negative.
func TestResizeClampsToMinimum(t *testing.T) {
	var out bytes.Buffer
	screen := NewScreen(&out, 80, 24, true)

	screen.Resize(0, 0)

	width, height := screen.Size()
	if width < minWidth || height < minHeight {
		t.Errorf("got %dx%d, want it clamped to at least %dx%d",
			width, height, minWidth, minHeight)
	}
}
