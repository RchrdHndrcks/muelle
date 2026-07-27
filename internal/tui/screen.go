package tui

import (
	"bufio"
	"io"
	"strconv"
	"strings"
)

// Screen renders frames to a terminal.
//
// Rendering is immediate mode: the caller hands over the complete set of lines
// each frame and Screen writes them in a single Write, bracketed by
// synchronized-output markers. There is no cell buffer and no diffing — at
// terminal sizes a frame is a few kilobytes, well under the point where
// diffing earns back its complexity, and a full repaint cannot leave stale
// cells behind.
type Screen struct {
	out    *bufio.Writer
	buf    strings.Builder
	width  int
	height int
	// colour reports whether styling is emitted at all; false honours
	// NO_COLOR and non-terminal output.
	colour bool
}

// NewScreen writes frames to out at the given size.
func NewScreen(out io.Writer, width, height int, colour bool) *Screen {
	return &Screen{
		out:    bufio.NewWriterSize(out, 64<<10),
		width:  width,
		height: height,
		colour: colour,
	}
}

// Size returns the screen's current dimensions.
func (s *Screen) Size() (width, height int) { return s.width, s.height }

// Resize updates the dimensions, normally in response to SIGWINCH. A degenerate
// size (a terminal reporting 0, or one too small to draw anything meaningful)
// is clamped so layout arithmetic never goes negative.
func (s *Screen) Resize(width, height int) {
	if width < minWidth {
		width = minWidth
	}
	if height < minHeight {
		height = minHeight
	}
	s.width, s.height = width, height
}

// Minimum dimensions the layout is designed against. Below this the UI is
// unusable anyway, and clamping keeps every "height - 3" style calculation
// safely positive.
const (
	minWidth  = 20
	minHeight = 6
)

// Colour reports whether styled output is enabled.
func (s *Screen) Colour() bool { return s.colour }

// Style applies a style to text, or returns it unchanged when colour is off.
// Every styled run in the UI goes through here so NO_COLOR needs no special
// casing at the call sites.
func (s *Screen) Style(style Style, text string) string {
	if !s.colour || style == StyleNone {
		return text
	}
	return string(style) + text + Reset
}

// Render draws one frame. Lines beyond the screen height are dropped and each
// line is truncated to the screen width; the caller does not have to be
// careful about overflow.
func (s *Screen) Render(lines []string) error {
	s.buf.Reset()
	s.buf.WriteString(SyncOn)
	s.buf.WriteString(CursorHome)

	for row := 0; row < s.height; row++ {
		if row > 0 {
			s.buf.WriteString("\r\n")
		}
		if row < len(lines) {
			s.buf.WriteString(Truncate(lines[row], s.width))
		}
		// Erase whatever the previous frame left on the rest of the row.
		s.buf.WriteString(ClearLine)
	}

	s.buf.WriteString(SyncOff)
	if _, err := s.out.WriteString(s.buf.String()); err != nil {
		return err
	}
	return s.out.Flush()
}

// Enter switches to the alternate screen and hides the cursor.
func (s *Screen) Enter() error {
	s.out.WriteString(AltScreenOn)
	s.out.WriteString(CursorHide)
	s.out.WriteString(ClearScreen)
	s.out.WriteString(CursorHome)
	return s.out.Flush()
}

// Leave restores the cursor and the user's original screen contents.
func (s *Screen) Leave() error {
	s.out.WriteString(Reset)
	s.out.WriteString(CursorShow)
	s.out.WriteString(AltScreenOff)
	return s.out.Flush()
}

// MoveCursor positions the hardware cursor, used when an overlay is taking
// text input so the caret appears where the user is typing. Coordinates are
// zero-based.
func (s *Screen) MoveCursor(col, row int) error {
	s.out.WriteString("\x1b[" + strconv.Itoa(row+1) + ";" + strconv.Itoa(col+1) + "H")
	s.out.WriteString(CursorShow)
	return s.out.Flush()
}

// HideCursor hides the caret again after an input overlay closes.
func (s *Screen) HideCursor() error {
	s.out.WriteString(CursorHide)
	return s.out.Flush()
}
