package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/RchrdHndrcks/muelle/internal/docker"
	"github.com/RchrdHndrcks/muelle/internal/logfmt"
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
	if len(lines) != 2 || lines[0].Line.Text != "first" || lines[1].Line.Text != "second" {
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
	if lines[0].Line.Text != "three" || lines[2].Line.Text != "five" {
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

func TestRenderLogsMarksStderrWhenNoLevelIsKnown(t *testing.T) {
	lines := []docker.LogLine{
		{Stream: docker.StreamStderr, Text: "something happened"},
	}
	var styled bool
	style := func(s tui.Style, text string) string {
		if s == styleStderr {
			styled = true
		}
		return text
	}

	RenderLogs(records(lines...), renderOpts(80, false, false, true, false), style)

	if !styled {
		t.Error("with no severity to go on, the stream is the only signal available")
	}
}

// Plenty of programs write their whole log to stderr — MySQL, PostgreSQL and
// nginx among them — so treating the stream as a severity paints a healthy
// container's entire output in error red and the colour stops meaning
// anything. A parsed level is always the better signal.
func TestParsedLevelOutranksStreamColour(t *testing.T) {
	lines := []docker.LogLine{
		{Stream: docker.StreamStderr, Text: "2026-07-27 14:13:48.437 UTC [1] LOG:  ready"},
	}
	var stderrStyled bool
	style := func(s tui.Style, text string) string {
		if s == styleStderr {
			stderrStyled = true
		}
		return text
	}

	RenderLogs(records(lines...), renderOpts(120, false, false, true, true), style)

	if stderrStyled {
		t.Error("an informational line on stderr should not be painted as an error")
	}
}

func TestRenderLogsShowsTimestampsWhenEnabled(t *testing.T) {
	stamped := docker.LogLine{
		Stream: docker.StreamStdout,
		Text:   "ready",
		Time:   time.Date(2026, 7, 27, 10, 11, 12, 0, time.UTC),
	}

	withStamps := RenderLogs(records(stamped), renderOpts(80, false, true, true, false), plainStyle)
	without := RenderLogs(records(stamped), renderOpts(80, false, false, true, false), plainStyle)

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

	rows := RenderLogs(records(line(long)), renderOpts(100, true, false, true, false), plainStyle)

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

	rows := RenderLogs(records(line(long)), renderOpts(100, false, false, true, false), plainStyle)

	if len(rows) != 1 {
		t.Errorf("got %d rows, want the line left intact for horizontal reading", len(rows))
	}
}

// Control characters in log output would otherwise move the cursor and
// scribble over the frame.
func TestRenderLogsSanitizesControlCharacters(t *testing.T) {
	rows := RenderLogs(records(line("progress\rdone\x07")), renderOpts(80, false, false, true, false), plainStyle)

	if strings.ContainsAny(rows[0], "\r\x07") {
		t.Errorf("got %q, want control characters stripped", rows[0])
	}
}

func TestRenderLogsHandlesEmptyInput(t *testing.T) {
	if rows := RenderLogs(nil, renderOpts(80, true, true, true, false), plainStyle); len(rows) != 0 {
		t.Errorf("got %v, want no rows", rows)
	}
}

// A pathological width must not spin forever building segments.
func TestRenderLogsSurvivesDegenerateWidth(t *testing.T) {
	done := make(chan []string, 1)
	go func() {
		done <- RenderLogs(records(line("hello world")), renderOpts(0, true, false, true, false), plainStyle)
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

func texts(records []Record) []string {
	out := make([]string, len(records))
	for i, r := range records {
		out[i] = r.Line.Text
	}
	return out
}

// records wraps raw lines the way the buffer does, for render tests.
func records(lines ...docker.LogLine) []Record {
	out := make([]Record, len(lines))
	for i, line := range lines {
		out[i] = Record{Line: line, Entry: logfmt.Parse(line.Text)}
	}
	return out
}

// renderOpts builds options for a render test.
func renderOpts(width int, wrap, timestamps, format, levelled bool) LogOptions {
	return LogOptions{Width: width, Wrap: wrap, Timestamps: timestamps, Format: format, Levelled: levelled}
}

// The formatter's main contribution is that a log becomes scannable, which
// requires the columns to hold their place even when a line has nothing to put
// in them.
func TestRenderLogsKeepsColumnsAlignedWhenValuesAreMissing(t *testing.T) {
	buffer := NewLogBuffer(10)
	buffer.Append(
		docker.LogLine{Stream: docker.StreamStdout, Text: `{"time":"2026-07-27T10:00:00Z","level":"INFO","msg":"with everything"}`},
		docker.LogLine{Stream: docker.StreamStdout, Text: `no timestamp and no level`},
	)

	rows := RenderLogs(buffer.Lines(""),
		renderOpts(120, false, true, true, buffer.Levelled()), plainStyle)

	first := strings.Index(rows[0], "with everything")
	second := strings.Index(rows[1], "no timestamp")
	if first != second {
		t.Errorf("messages start at columns %d and %d; they should line up:\n%q\n%q",
			first, second, rows[0], rows[1])
	}
}

// Structured lines are the reason this exists: the message is buried behind
// the syntax a program wrote for another program.
func TestRenderLogsUnpacksStructuredLines(t *testing.T) {
	buffer := NewLogBuffer(10)
	buffer.Append(docker.LogLine{
		Stream: docker.StreamStdout,
		Text:   `{"time":"2026-07-27T10:00:00Z","level":"ERROR","msg":"send failed","attempt":3}`,
	})

	row := RenderLogs(buffer.Lines(""),
		renderOpts(120, false, false, true, true), plainStyle)[0]

	if strings.Contains(row, `"msg"`) {
		t.Errorf("got %q, want the JSON syntax gone", row)
	}
	if !strings.Contains(row, "send failed") || !strings.Contains(row, "attempt=3") {
		t.Errorf("got %q, want the message and its fields", row)
	}
	if !strings.Contains(row, "ERROR") {
		t.Errorf("got %q, want the severity in its column", row)
	}
}

// Turning formatting off must show exactly what the container wrote, which is
// what you want when the question is about the log format itself.
func TestRenderLogsRawShowsTheOriginalText(t *testing.T) {
	original := `{"level":"INFO","msg":"hello"}`
	buffer := NewLogBuffer(10)
	buffer.Append(docker.LogLine{Stream: docker.StreamStdout, Text: original})

	row := RenderLogs(buffer.Lines(""),
		renderOpts(120, false, false, false, true), plainStyle)[0]

	if !strings.Contains(row, original) {
		t.Errorf("got %q, want the untouched line", row)
	}
}

// The level column only appears where a stream actually has levels; reserving
// it for output that never carries one would waste width on every line.
func TestLevelColumnOnlyAppearsForLevelledStreams(t *testing.T) {
	plain := NewLogBuffer(10)
	plain.Append(docker.LogLine{Stream: docker.StreamStdout, Text: "just some output"})

	if plain.Levelled() {
		t.Fatal("a stream with no severities is not levelled")
	}
	row := RenderLogs(plain.Lines(""),
		renderOpts(120, false, false, true, plain.Levelled()), plainStyle)[0]
	if strings.HasPrefix(row, " ") {
		t.Errorf("got %q, want no reserved level column", row)
	}
}

// Sticky, so the layout does not shift as lines scroll past.
func TestLevelledStaysSetOnceSeen(t *testing.T) {
	buffer := NewLogBuffer(10)
	buffer.Append(docker.LogLine{Text: `{"level":"INFO","msg":"m"}`})
	buffer.Append(docker.LogLine{Text: "plain line"})

	if !buffer.Levelled() {
		t.Error("a stream that has shown a severity keeps its column")
	}
}

func TestResetClearsLevelled(t *testing.T) {
	buffer := NewLogBuffer(10)
	buffer.Append(docker.LogLine{Text: `{"level":"INFO","msg":"m"}`})

	buffer.Reset()

	if buffer.Levelled() {
		t.Error("reopening a stream starts over")
	}
}

// Searching reasons about what the container wrote, which is a superset of
// what the formatter displays.
func TestFilterMatchesTheOriginalText(t *testing.T) {
	buffer := NewLogBuffer(10)
	buffer.Append(docker.LogLine{Text: `{"level":"INFO","msg":"hello","user":"ana"}`})

	if got := len(buffer.Lines("user")); got != 1 {
		t.Errorf("got %d matches for a field name, want 1", got)
	}
	if got := len(buffer.Lines("ana")); got != 1 {
		t.Errorf("got %d matches for a field value, want 1", got)
	}
}

// painter records what was styled, so a test can ask how a run was coloured
// rather than matching escape sequences by hand.
type painter struct {
	runs []struct {
		style tui.Style
		text  string
	}
}

func (p *painter) style(style tui.Style, text string) string {
	p.runs = append(p.runs, struct {
		style tui.Style
		text  string
	}{style, text})
	return text
}

// styleOf reports the style a run of text was painted with, and whether it was
// painted as its own run at all.
func (p *painter) styleOf(text string) (tui.Style, bool) {
	for _, run := range p.runs {
		if run.text == text {
			return run.style, true
		}
	}
	return tui.StyleNone, false
}

func renderWith(t *testing.T, text string, opts LogOptions) *painter {
	t.Helper()
	buffer := NewLogBuffer(10)
	buffer.Append(line(text))
	paint := &painter{}
	if opts.Width == 0 {
		opts.Width = 200
	}
	RenderLogs(buffer.Lines(""), opts, paint.style)
	return paint
}

// An access log is mostly noise until the verb stands out: the method is the
// first thing the eye looks for when scanning for the request that failed.
func TestHTTPVerbsAreColouredApart(t *testing.T) {
	get := renderWith(t, `{"level":"INFO","msg":"| GET | /api/v1/chat/ws | 401"}`, LogOptions{Format: true, Levelled: true})
	getStyle, ok := get.styleOf("GET")
	if !ok {
		t.Fatalf("GET was not painted as its own run, got %+v", get.runs)
	}

	post := renderWith(t, `{"level":"INFO","msg":"| POST | /api/v1/login | 200"}`, LogOptions{Format: true, Levelled: true})
	postStyle, ok := post.styleOf("POST")
	if !ok {
		t.Fatalf("POST was not painted as its own run, got %+v", post.runs)
	}

	if getStyle == postStyle {
		t.Errorf("GET and POST share style %q, want a verb to be recognisable by colour", getStyle)
	}
}

// Colouring a word merely because it spells a verb makes the colour untrustworthy,
// which is the only thing it has going for it.
func TestOnlyWholeUppercaseVerbsAreColoured(t *testing.T) {
	for _, text := range []string{
		`{"level":"INFO","msg":"could not get user"}`,
		`{"level":"INFO","msg":"TARGET reached"}`,
		`{"level":"INFO","msg":"GETTING ready"}`,
	} {
		paint := renderWith(t, text, LogOptions{Format: true, Levelled: true})
		for _, run := range paint.runs {
			if _, isVerb := methodStyles[run.text]; isVerb {
				t.Errorf("%s: painted %q as a verb", text, run.text)
			}
		}
	}
}

// Nesting a colour inside the stderr red emits a reset that ends the red early,
// leaving the rest of the line uncoloured.
func TestVerbsAreLeftAloneInsideAStderrLine(t *testing.T) {
	buffer := NewLogBuffer(10)
	buffer.Append(docker.LogLine{Stream: docker.StreamStderr, Text: "| GET | /health"})
	paint := &painter{}

	RenderLogs(buffer.Lines(""), LogOptions{Width: 200, Format: true}, paint.style)

	if _, ok := paint.styleOf("GET"); ok {
		t.Errorf("GET was painted separately inside a stderr line, got %+v", paint.runs)
	}
}
