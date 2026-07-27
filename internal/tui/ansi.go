package tui

import (
	"strconv"
	"strings"
	"unicode/utf8"
)

// Control sequences. These are the ECMA-48 / xterm codes muelle emits; naming
// them keeps the render code readable.
const (
	// AltScreenOn switches to the alternate screen buffer, so quitting
	// restores whatever the user had on screen before.
	AltScreenOn  = "\x1b[?1049h"
	AltScreenOff = "\x1b[?1049l"
	CursorHide   = "\x1b[?25l"
	CursorShow   = "\x1b[?25h"
	CursorHome   = "\x1b[H"
	ClearScreen  = "\x1b[2J"
	ClearLine    = "\x1b[K"
	// SyncOn and SyncOff bracket a frame so terminals that support
	// synchronized output present it atomically instead of mid-repaint.
	// Terminals that do not support it ignore the sequence.
	SyncOn  = "\x1b[?2026h"
	SyncOff = "\x1b[?2026l"
	Reset   = "\x1b[0m"
)

// Style is a set of SGR attributes applied to a run of text.
type Style string

// Base attributes and the 256-colour palette entries muelle uses.
const (
	StyleNone      Style = ""
	StyleBold      Style = "\x1b[1m"
	StyleDim       Style = "\x1b[2m"
	StyleItalic    Style = "\x1b[3m"
	StyleUnderline Style = "\x1b[4m"
	StyleReverse   Style = "\x1b[7m"
)

// Foreground returns the SGR sequence for a 256-colour palette index.
func Foreground(color int) Style {
	return Style("\x1b[38;5;" + strconv.Itoa(color) + "m")
}

// Background returns the SGR sequence for a 256-colour palette index.
func Background(color int) Style {
	return Style("\x1b[48;5;" + strconv.Itoa(color) + "m")
}

// Join concatenates styles so they can be applied as one prefix.
func Join(styles ...Style) Style {
	var b strings.Builder
	for _, s := range styles {
		b.WriteString(string(s))
	}
	return Style(b.String())
}

// VisibleWidth reports how many terminal cells s occupies, ignoring escape
// sequences.
//
// Getting this right matters for every column alignment in the UI: measuring
// with len() counts escape bytes as visible, and every styled cell then shifts
// the layout.
func VisibleWidth(s string) int {
	width := 0
	for i := 0; i < len(s); {
		if s[i] == 0x1b {
			i += escapeLen(s[i:])
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		i += size
		width += RuneWidth(r)
	}
	return width
}

// RuneWidth reports the display width of a single rune.
//
// This is a deliberate approximation. Full Unicode width tables live in
// golang.org/x/text, which this project does not depend on; instead the
// common CJK and emoji ranges are treated as double-width and combining marks
// as zero-width. Anything else is one cell, which is correct for the ASCII
// that container names, image tags and log output overwhelmingly consist of.
func RuneWidth(r rune) int {
	switch {
	case r == 0:
		return 0
	case r < 32 || (r >= 0x7f && r < 0xa0):
		// Control characters are stripped before display; count them as
		// nothing rather than letting them skew the layout.
		return 0
	case r >= 0x0300 && r <= 0x036f:
		return 0 // combining diacritical marks
	case r >= 0x1100 && r <= 0x115f, // Hangul Jamo
		r >= 0x2e80 && r <= 0xa4cf, // CJK radicals, Kangxi, ideographs
		r >= 0xac00 && r <= 0xd7a3, // Hangul syllables
		r >= 0xf900 && r <= 0xfaff, // CJK compatibility ideographs
		r >= 0xfe30 && r <= 0xfe6f, // CJK compatibility forms
		r >= 0xff00 && r <= 0xff60, // fullwidth forms
		r >= 0xffe0 && r <= 0xffe6,
		r >= 0x1f300 && r <= 0x1f64f, // emoji
		r >= 0x1f900 && r <= 0x1f9ff:
		return 2
	default:
		return 1
	}
}

// escapeLen returns the byte length of the escape sequence starting at the
// beginning of s, or 1 if it is not a recognisable sequence.
func escapeLen(s string) int {
	if len(s) < 2 || s[0] != 0x1b {
		return 1
	}
	switch s[1] {
	case '[':
		// CSI: parameter bytes, then a final byte in @-~.
		for i := 2; i < len(s); i++ {
			if s[i] >= 0x40 && s[i] <= 0x7e {
				return i + 1
			}
		}
		return len(s)
	case ']':
		// OSC: runs until BEL or ST (ESC \).
		for i := 2; i < len(s); i++ {
			if s[i] == 0x07 {
				return i + 1
			}
			if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '\\' {
				return i + 2
			}
		}
		return len(s)
	default:
		return 2
	}
}

// Truncate shortens s to at most width visible cells, preserving escape
// sequences and appending a reset when any styling was in effect.
//
// Cutting a styled string at a byte offset can slice an escape sequence in
// half, and the terminal then interprets the tail of the sequence as text —
// corrupting everything after it on screen. Truncating on cell boundaries is
// the only safe way to fit styled content into a column.
func Truncate(s string, width int) string {
	if width <= 0 {
		return ""
	}
	var (
		b       strings.Builder
		cells   int
		escaped bool
	)
	for i := 0; i < len(s); {
		if s[i] == 0x1b {
			n := escapeLen(s[i:])
			b.WriteString(s[i : i+n])
			escaped = true
			i += n
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		w := RuneWidth(r)
		if cells+w > width {
			if escaped {
				b.WriteString(Reset)
			}
			return b.String()
		}
		b.WriteString(s[i : i+size])
		cells += w
		i += size
	}
	return s
}

// TruncateEllipsis shortens s to width cells, marking elision with a trailing
// "…" so the user can tell the value was cut.
func TruncateEllipsis(s string, width int) string {
	if VisibleWidth(s) <= width {
		return s
	}
	if width <= 1 {
		return Truncate(s, width)
	}
	return Truncate(s, width-1) + "…"
}

// TrimCells removes the first width visible cells from s and returns the
// remainder.
//
// Escape sequences in the removed portion are carried over to the front of the
// result: the styling they set still applies to what follows, so dropping them
// would leave a wrapped line's continuation unstyled.
func TrimCells(s string, width int) string {
	var (
		b     strings.Builder
		cells int
		i     int
	)
	for i < len(s) && cells < width {
		if s[i] == 0x1b {
			n := escapeLen(s[i:])
			b.WriteString(s[i : i+n])
			i += n
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		cells += RuneWidth(r)
		i += size
	}
	return b.String() + s[i:]
}

// Pad extends s with spaces to exactly width visible cells, truncating if it
// is already longer. This is what keeps table columns aligned regardless of
// styling.
func Pad(s string, width int) string {
	visible := VisibleWidth(s)
	if visible > width {
		return TruncateEllipsis(s, width)
	}
	return s + strings.Repeat(" ", width-visible)
}

// Sanitize replaces control characters that would corrupt the display.
//
// Log output is arbitrary bytes: a stray carriage return rewinds the cursor
// mid-line, a bare ESC starts a sequence that eats the following text, and a
// tab expands unpredictably. Existing SGR colour sequences are kept, since
// programs colouring their own output is common and worth preserving.
func Sanitize(s string) string {
	if !strings.ContainsFunc(s, func(r rune) bool { return r < 32 || r == 0x7f }) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c == 0x1b:
			n := escapeLen(s[i:])
			// Keep colour sequences, drop cursor movement and the rest.
			if n >= 3 && s[i+1] == '[' && s[i+n-1] == 'm' {
				b.WriteString(s[i : i+n])
			}
			i += n
		case c == '\t':
			b.WriteString("    ")
			i++
		case c < 32 || c == 0x7f:
			i++
		default:
			r, size := utf8.DecodeRuneInString(s[i:])
			if r == utf8.RuneError && size == 1 {
				// Invalid UTF-8 byte; skip rather than emit garbage.
				i++
				continue
			}
			b.WriteString(s[i : i+size])
			i += size
		}
	}
	return b.String()
}
