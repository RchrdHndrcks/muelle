package ui

import (
	"os"
	"strings"
	"time"

	"github.com/RchrdHndrcks/muelle/internal/tui"
)

// This file is the log viewer's "s" key: writing what is on screen to a file.
// The point is to capture the view, not the raw stream — `docker logs` already
// exists for the latter. So the saved file honours the filter and the
// timestamp toggle, and drops every escape sequence, because its readers are
// grep and a pull-request comment box rather than a terminal.

// defaultLogPath proposes where a save should land: the working directory,
// named after the container (or Compose project) and the moment. The
// timestamp is not decoration — it is what lets two saves of the same
// container coexist instead of the second silently replacing the first.
func defaultLogPath(name string, now time.Time) string {
	return "./" + name + "-" + now.Format("20060102-150405") + ".log"
}

// openLogSavePrompt opens the input overlay pre-filled with a default path,
// so the common case is Enter and the uncommon case is editing a path that
// already shows the expected shape.
func (a *App) openLogSavePrompt() {
	a.overlay = NewInput("Save logs", "path:", defaultLogPath(a.logName, time.Now()), func(value any) {
		path, _ := value.(string)
		a.saveLogs(strings.TrimSpace(path))
	})
}

// saveLogs writes the lines the viewer is currently showing — same filter,
// same timestamp and formatting toggles — to path, as plain text.
//
// The write happens on the event loop rather than in a goroutine, the same
// trade remember() makes for the configuration file: a local write is over in
// microseconds, and the synchronous path means the status line can report the
// outcome immediately and truthfully.
func (a *App) saveLogs(path string) {
	if path == "" {
		return
	}
	lines := plainLogLines(a.logs.Lines(a.logFilter), a.logOptions(a.screenWidth()))

	var content strings.Builder
	for _, line := range lines {
		content.WriteString(line)
		content.WriteByte('\n')
	}
	// 0644 and no MkdirAll: this is an ordinary file in a directory the user
	// named. Inventing missing parents would turn a typo into a directory
	// tree, which is a worse surprise than the error naming the typo.
	if err := os.WriteFile(path, []byte(content.String()), 0o644); err != nil {
		a.setError("save logs: %v", err)
		return
	}
	a.setStatus("saved %s to %s", Plural(len(lines), "line", "lines"), path)
}

// plainLogLines renders records through the same pipeline the viewer uses —
// same prefixes, same formatting, same column layout — with muelle's styling
// withheld and the container's own colour codes removed. Sharing RenderLogs
// is what keeps "what you saved" equal to "what you saw"; a second assembly
// path would drift from the first the next time either changed.
//
// Wrapping is forced off: it exists so the pager can scroll by rows, and a
// file has no width to fold at. One record, one line.
func plainLogLines(records []Record, opts LogOptions) []string {
	opts.Wrap = false
	unstyled := func(_ tui.Style, text string) string { return text }
	rows := RenderLogs(records, opts, unstyled)
	for i, row := range rows {
		rows[i] = stripEscapes(row)
	}
	return rows
}

// stripEscapes removes every escape sequence from a line. Sanitize
// deliberately keeps the colour codes a container wrote, because a terminal
// renders them; in a file they are noise that breaks grep and diff.
func stripEscapes(s string) string {
	if !strings.Contains(s, "\x1b") {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		if s[i] == 0x1b {
			i += tui.EscapeLen(s[i:])
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}
