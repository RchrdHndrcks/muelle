package ui

import (
	"strings"

	"github.com/RchrdHndrcks/muelle/internal/docker"
	"github.com/RchrdHndrcks/muelle/internal/tui"
)

// LogBuffer holds a bounded window of a container's log output.
//
// The capacity is what makes following a chatty container safe: a service
// logging every request would otherwise grow the process until it was killed.
// Old lines are dropped once the buffer is full, which is what a log viewer
// should do anyway — nobody scrolls back ten thousand lines.
type LogBuffer struct {
	lines    []docker.LogLine
	capacity int
	// dropped counts lines evicted, so the UI can say the history is
	// incomplete rather than implying the container started here.
	dropped int
}

// NewLogBuffer creates a buffer holding at most capacity lines.
func NewLogBuffer(capacity int) *LogBuffer {
	if capacity < 1 {
		capacity = 1
	}
	return &LogBuffer{capacity: capacity, lines: make([]docker.LogLine, 0, capacity)}
}

// Append adds lines, evicting the oldest once the buffer is full.
func (b *LogBuffer) Append(lines ...docker.LogLine) {
	for _, line := range lines {
		if len(b.lines) < b.capacity {
			b.lines = append(b.lines, line)
			continue
		}
		// Shift down by one. Copy rather than reslice so the backing
		// array stays a fixed size instead of creeping forward in
		// memory.
		copy(b.lines, b.lines[1:])
		b.lines[len(b.lines)-1] = line
		b.dropped++
	}
}

// Len returns how many lines are buffered.
func (b *LogBuffer) Len() int { return len(b.lines) }

// Dropped returns how many lines were evicted.
func (b *LogBuffer) Dropped() int { return b.dropped }

// Reset empties the buffer, for reopening a stream.
func (b *LogBuffer) Reset() {
	b.lines = b.lines[:0]
	b.dropped = 0
}

// Lines returns the buffered lines, optionally filtered by a case-insensitive
// substring match.
func (b *LogBuffer) Lines(filter string) []docker.LogLine {
	if filter == "" {
		return b.lines
	}
	needle := strings.ToLower(filter)
	matched := make([]docker.LogLine, 0, len(b.lines))
	for _, line := range b.lines {
		if strings.Contains(strings.ToLower(line.Text), needle) {
			matched = append(matched, line)
		}
	}
	return matched
}

// RenderLogs turns log lines into display rows.
//
// Wrapping happens here rather than in the terminal because the viewer needs
// to know how many rows a line occupies in order to scroll by rows. Letting
// the terminal wrap would make the scroll position drift out of step with the
// content whenever a long line appeared.
func RenderLogs(lines []docker.LogLine, width int, wrap, timestamps bool, style func(tui.Style, string) string) []string {
	if width < 1 {
		width = 1
	}
	rows := make([]string, 0, len(lines))

	for _, line := range lines {
		prefix := ""
		if timestamps && !line.Time.IsZero() {
			prefix = style(styleTimestamp, line.Time.Format("15:04:05")) + " "
		}
		text := tui.Sanitize(line.Text)
		if line.Stderr() {
			text = style(styleStderr, text)
		}

		if !wrap {
			rows = append(rows, prefix+text)
			continue
		}
		available := width - tui.VisibleWidth(prefix)
		if available < 1 {
			available = 1
		}
		for i, segment := range wrapLine(text, available) {
			if i == 0 {
				rows = append(rows, prefix+segment)
				continue
			}
			// Continuation rows are indented under the timestamp so
			// the eye can tell them from new entries.
			rows = append(rows, strings.Repeat(" ", tui.VisibleWidth(prefix))+segment)
		}
	}
	return rows
}

// wrapLine splits a styled string into segments of at most width visible
// cells, without breaking escape sequences.
func wrapLine(s string, width int) []string {
	if tui.VisibleWidth(s) <= width {
		return []string{s}
	}
	var segments []string
	remainder := s
	// Guard against a pathological case producing an unbounded slice: a
	// zero-progress iteration would otherwise loop forever.
	for tui.VisibleWidth(remainder) > width {
		head := tui.Truncate(remainder, width)
		if tui.VisibleWidth(head) == 0 {
			break
		}
		segments = append(segments, head)
		remainder = tui.TrimCells(remainder, width)
	}
	if remainder != "" {
		segments = append(segments, remainder)
	}
	return segments
}
