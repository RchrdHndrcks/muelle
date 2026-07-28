package ui

import (
	"strings"

	"github.com/RchrdHndrcks/muelle/internal/docker"
	"github.com/RchrdHndrcks/muelle/internal/logfmt"
	"github.com/RchrdHndrcks/muelle/internal/tui"
)

// Record is one buffered log line together with what could be made of it.
//
// The parse happens once, on arrival. Doing it at render time would mean
// re-parsing the whole buffer on every frame — several thousand lines, ten
// times a second while following — to produce a result that cannot have
// changed.
type Record struct {
	Line  docker.LogLine
	Entry logfmt.Entry
}

// LogBuffer holds a bounded window of a container's log output.
//
// The capacity is what makes following a chatty container safe: a service
// logging every request would otherwise grow the process until it was killed.
// Old lines are dropped once the buffer is full, which is what a log viewer
// should do anyway — nobody scrolls back ten thousand lines.
type LogBuffer struct {
	records  []Record
	capacity int
	// dropped counts lines evicted, so the UI can say the history is
	// incomplete rather than implying the container started here.
	dropped int
	// levelled records whether any line so far carried a severity, which
	// decides whether the view reserves a column for it. Sticky, so the
	// layout does not shift as lines scroll past.
	levelled bool
}

// NewLogBuffer creates a buffer holding at most capacity lines.
func NewLogBuffer(capacity int) *LogBuffer {
	if capacity < 1 {
		capacity = 1
	}
	return &LogBuffer{capacity: capacity, records: make([]Record, 0, capacity)}
}

// Append adds lines, parsing each and evicting the oldest once full.
func (b *LogBuffer) Append(lines ...docker.LogLine) {
	for _, line := range lines {
		record := Record{Line: line, Entry: logfmt.Parse(line.Text)}
		if record.Entry.Level != logfmt.LevelUnknown {
			b.levelled = true
		}

		if len(b.records) < b.capacity {
			b.records = append(b.records, record)
			continue
		}
		// Shift down by one. Copy rather than reslice so the backing
		// array stays a fixed size instead of creeping forward in
		// memory.
		copy(b.records, b.records[1:])
		b.records[len(b.records)-1] = record
		b.dropped++
	}
}

// Len returns how many lines are buffered.
func (b *LogBuffer) Len() int { return len(b.records) }

// Dropped returns how many lines were evicted.
func (b *LogBuffer) Dropped() int { return b.dropped }

// Levelled reports whether anything in this stream carries a severity.
func (b *LogBuffer) Levelled() bool { return b.levelled }

// Reset empties the buffer, for reopening a stream.
func (b *LogBuffer) Reset() {
	b.records = b.records[:0]
	b.dropped = 0
	b.levelled = false
}

// Lines returns the buffered records, optionally filtered by a
// case-insensitive substring match.
//
// Matching is against the original text, not the formatted rendering: what the
// container actually wrote is the thing a search is reasoning about, and it is
// a superset of everything the formatter displays.
func (b *LogBuffer) Lines(filter string) []Record {
	if filter == "" {
		return b.records
	}
	needle := strings.ToLower(filter)
	matched := make([]Record, 0, len(b.records))
	for _, record := range b.records {
		if strings.Contains(strings.ToLower(record.Line.Text), needle) {
			matched = append(matched, record)
		}
	}
	return matched
}

// LogOptions controls how log lines are rendered.
type LogOptions struct {
	Width int
	// Wrap breaks long lines across rows rather than clipping them.
	Wrap bool
	// Timestamps prefixes each line with the time it was logged.
	Timestamps bool
	// Format renders structured lines as their parts. Turning it off shows
	// exactly what the container wrote, which is what you want when the
	// question is about the log format itself.
	Format bool
	// Levelled reserves a column for the severity, so messages line up.
	Levelled bool
}

// Widths reserved for the columns that precede a message. Both are held even
// when the value is missing: a line without a timestamp shifting nine columns
// left destroys the alignment that makes a log scannable, and the alignment is
// most of what the formatting is for.
const (
	levelColumn = 5
	timeColumn  = 8 // 15:04:05
)

// RenderLogs turns log records into display rows.
//
// Wrapping happens here rather than in the terminal because the viewer needs
// to know how many rows a line occupies in order to scroll by rows. Letting
// the terminal wrap would make the scroll position drift out of step with the
// content whenever a long line appeared.
func RenderLogs(records []Record, opts LogOptions, style func(tui.Style, string) string) []string {
	width := opts.Width
	if width < 1 {
		width = 1
	}
	rows := make([]string, 0, len(records))

	for _, record := range records {
		prefix := renderPrefix(record, opts, style)
		body := renderBody(record, opts, style)

		if !opts.Wrap {
			rows = append(rows, prefix+body)
			continue
		}
		available := width - tui.VisibleWidth(prefix)
		if available < 1 {
			available = 1
		}
		for i, segment := range wrapLine(body, available) {
			if i == 0 {
				rows = append(rows, prefix+segment)
				continue
			}
			// Continuation rows are indented under the message so the
			// eye can tell them from new entries.
			rows = append(rows, strings.Repeat(" ", tui.VisibleWidth(prefix))+segment)
		}
	}
	return rows
}

// renderPrefix builds the timestamp and severity columns.
func renderPrefix(record Record, opts LogOptions, style func(tui.Style, string) string) string {
	var prefix string

	if opts.Timestamps {
		// Prefer the writer's own timestamp: it is when the event
		// happened, whereas the daemon's is when the line reached it.
		stamp := record.Entry.Time
		if stamp.IsZero() {
			stamp = record.Line.Time
		}
		if stamp.IsZero() {
			prefix += strings.Repeat(" ", timeColumn) + " "
		} else {
			prefix += style(styleTimestamp, stamp.Format("15:04:05")) + " "
		}
	}

	if opts.Format && opts.Levelled {
		level := record.Entry.Level
		prefix += style(levelStyle(level), tui.Pad(level.String(), levelColumn)) + " "
	}
	return prefix
}

// renderBody builds the message and any fields.
func renderBody(record Record, opts LogOptions, style func(tui.Style, string) string) string {
	if !opts.Format {
		return renderMessage(record, tui.Sanitize(record.Line.Text), style)
	}

	body := renderMessage(record, tui.Sanitize(record.Entry.Message), style)

	for _, field := range record.Entry.Fields {
		// Dimmed, and after the message: the message is what is being
		// read, the fields are what you consult once it has your
		// attention.
		body += " " + style(styleFieldKey, tui.Sanitize(field.Key)) +
			style(styleMuted, "=") +
			style(styleFieldValue, tui.Sanitize(logfmt.Quote(field.Value)))
	}
	return body
}

// renderMessage colours a message, either as a whole or a word at a time.
//
// Never both. A colour opened inside another one closes with a reset that ends
// the outer colour too, so highlighting a verb inside a red stderr line would
// leave everything after the verb uncoloured. The line-wide signal wins: that
// the output went to stderr says more than which verb it mentions.
func renderMessage(record Record, text string, style func(tui.Style, string) string) string {
	if colouredByStream(record) {
		return style(styleStderr, text)
	}
	return highlightMethods(text, style)
}

// colouredByStream reports whether a line gets the fallback colouring for
// output whose severity is unknown.
//
// Only a fallback: plenty of programs write their entire log to stderr —
// MySQL, PostgreSQL and nginx among them — so treating the stream as a
// severity paints a healthy container's whole output in error red, and the
// colour stops meaning anything. A parsed level is always the better signal.
func colouredByStream(record Record) bool {
	return record.Entry.Level == logfmt.LevelUnknown && record.Line.Stderr()
}

// highlightMethods colours each HTTP verb that appears as a word of its own.
//
// Words are taken whole, so "TARGET" is not a GET and "GETTING" is not a
// request. Colour that fires on a coincidence is worse than no colour at all,
// because it teaches the reader to stop trusting it.
func highlightMethods(text string, style func(tui.Style, string) string) string {
	if !strings.ContainsFunc(text, func(r rune) bool { return r >= 'A' && r <= 'Z' }) {
		return text
	}

	var b strings.Builder
	b.Grow(len(text))
	for i := 0; i < len(text); {
		if text[i] == 0x1b {
			// A sequence the container wrote itself: stepped over whole,
			// so its parameters are never mistaken for a word.
			n := tui.EscapeLen(text[i:])
			b.WriteString(text[i : i+n])
			i += n
			continue
		}
		if !wordByte(text[i]) {
			b.WriteByte(text[i])
			i++
			continue
		}
		start := i
		for i < len(text) && wordByte(text[i]) {
			i++
		}
		word := text[start:i]
		if verb, ok := methodStyles[word]; ok {
			b.WriteString(style(verb, word))
			continue
		}
		b.WriteString(word)
	}
	return b.String()
}

// wordByte reports whether a byte can appear inside a word. Taking the run
// whole is what makes the boundary check unnecessary: a verb only matches when
// nothing wordlike sits against it on either side.
func wordByte(c byte) bool {
	switch {
	case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9':
		return true
	case c == '_', c >= 0x80:
		return true
	default:
		return false
	}
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
