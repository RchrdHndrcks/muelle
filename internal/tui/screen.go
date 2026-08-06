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
	// clipboard reports whether OSC 52 copy sequences may be emitted. Off by
	// default: only a screen that is driving a real terminal should ask it to
	// touch the user's clipboard, and the headless dump mode writes to a pipe
	// whose reader would see the sequence as garbage.
	clipboard bool
	// pendingCopy is a queued OSC 52 sequence waiting to depart with the
	// next frame; see Copy.
	pendingCopy string
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

// EnableClipboard declares that a real terminal is reading this screen's
// output, so OSC 52 copy sequences will reach something able to act on them.
// A separate switch rather than a constructor argument because only the
// interactive path flips it; every other caller keeps the safe default.
func (s *Screen) EnableClipboard() { s.clipboard = true }

// Copy queues text for the terminal's clipboard and reports whether emitting
// is possible at all — false means no terminal is listening and the caller
// should tell the user instead of pretending the copy happened.
//
// The sequence is not written immediately. A frame is this screen's unit of
// output — one Write bracketed by synchronized-output markers — and a second
// writer racing that Write could land bytes inside a frame. Queuing keeps
// the invariant: the sequence departs inside the next Render's Write, after
// the frame's closing marker, so the bracketing stays intact.
func (s *Screen) Copy(text string) bool {
	if !s.clipboard {
		return false
	}
	s.pendingCopy = CopySequence(text)
	return true
}

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
	// A queued clipboard write rides out with the frame it was requested
	// under, in the same Write so nothing can interleave, and after the
	// closing sync marker so the frame bracketing stays exactly a frame.
	if s.pendingCopy != "" {
		s.buf.WriteString(s.pendingCopy)
		s.pendingCopy = ""
	}
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
