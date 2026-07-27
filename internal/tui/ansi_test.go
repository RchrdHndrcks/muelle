package tui

import (
	"strings"
	"testing"
)

func TestVisibleWidthIgnoresEscapeSequences(t *testing.T) {
	styled := string(Foreground(196)) + "running" + Reset

	if got := VisibleWidth(styled); got != 7 {
		t.Errorf("got %d, want 7 (only the word counts)", got)
	}
}

func TestVisibleWidthCountsWideRunes(t *testing.T) {
	if got := VisibleWidth("日本"); got != 4 {
		t.Errorf("got %d, want 4 (two double-width runes)", got)
	}
}

func TestVisibleWidthPlainASCII(t *testing.T) {
	if got := VisibleWidth("mysql:8.0"); got != 9 {
		t.Errorf("got %d, want 9", got)
	}
}

// Truncating a styled string at a byte offset can cut an escape sequence in
// half, and the terminal then prints the remainder as text, corrupting the
// rest of the screen.
func TestTruncateNeverSplitsEscapeSequence(t *testing.T) {
	styled := string(Foreground(46)) + "abcdefghij" + Reset

	got := Truncate(styled, 4)

	if !strings.HasPrefix(got, string(Foreground(46))) {
		t.Errorf("got %q, want the opening sequence intact", got)
	}
	if !strings.HasSuffix(got, Reset) {
		t.Errorf("got %q, want a reset appended so styling does not leak", got)
	}
	if VisibleWidth(got) != 4 {
		t.Errorf("got visible width %d, want 4", VisibleWidth(got))
	}
	if strings.Contains(strings.TrimPrefix(got, string(Foreground(46))), "\x1b[38") {
		t.Errorf("got %q, want no partial sequence", got)
	}
}

func TestTruncateLeavesShortStringAlone(t *testing.T) {
	if got := Truncate("short", 40); got != "short" {
		t.Errorf("got %q, want the input unchanged", got)
	}
}

func TestTruncateZeroWidthYieldsEmpty(t *testing.T) {
	if got := Truncate("anything", 0); got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

// A double-width rune must not be half-emitted at the boundary, which would
// leave the column one cell short and shift everything after it.
func TestTruncateDoesNotSplitWideRune(t *testing.T) {
	got := Truncate("日本語", 3)

	if VisibleWidth(got) != 2 {
		t.Errorf("got %q (width %d), want to stop at 2 cells rather than split a wide rune",
			got, VisibleWidth(got))
	}
}

func TestTruncateEllipsisMarksElision(t *testing.T) {
	got := TruncateEllipsis("a-very-long-container-name", 10)

	if VisibleWidth(got) != 10 {
		t.Errorf("got %q (width %d), want exactly 10", got, VisibleWidth(got))
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("got %q, want a trailing ellipsis", got)
	}
}

func TestPadAlignsStyledAndPlainToSameWidth(t *testing.T) {
	plain := Pad("web", 10)
	styled := Pad(string(StyleBold)+"web"+Reset, 10)

	if VisibleWidth(plain) != 10 || VisibleWidth(styled) != 10 {
		t.Errorf("got widths %d and %d, want both 10 so columns line up",
			VisibleWidth(plain), VisibleWidth(styled))
	}
}

func TestPadTruncatesOverlongValue(t *testing.T) {
	if got := VisibleWidth(Pad("an-overlong-value", 8)); got != 8 {
		t.Errorf("got width %d, want 8", got)
	}
}

// A carriage return in log output would rewind the cursor and overwrite the
// line already drawn.
func TestSanitizeStripsCursorMovingControls(t *testing.T) {
	got := Sanitize("progress\rdone\x07")

	if strings.ContainsAny(got, "\r\x07") {
		t.Errorf("got %q, want control characters removed", got)
	}
	if got != "progressdone" {
		t.Errorf("got %q, want %q", got, "progressdone")
	}
}

func TestSanitizeExpandsTabs(t *testing.T) {
	if got := Sanitize("a\tb"); got != "a    b" {
		t.Errorf("got %q, want tabs expanded to spaces", got)
	}
}

// Applications colouring their own log output is common and worth keeping.
func TestSanitizeKeepsColourButDropsCursorSequences(t *testing.T) {
	input := string(Foreground(46)) + "ok" + Reset + "\x1b[2Amoved"

	got := Sanitize(input)

	if !strings.Contains(got, string(Foreground(46))) {
		t.Errorf("got %q, want the colour sequence preserved", got)
	}
	if strings.Contains(got, "\x1b[2A") {
		t.Errorf("got %q, want the cursor-up sequence dropped", got)
	}
	if !strings.Contains(got, "moved") {
		t.Errorf("got %q, want the text after the sequence kept", got)
	}
}

func TestSanitizeLeavesCleanTextUntouched(t *testing.T) {
	clean := "2026-07-27 starting server on :8080"
	if got := Sanitize(clean); got != clean {
		t.Errorf("got %q, want the input unchanged", got)
	}
}
