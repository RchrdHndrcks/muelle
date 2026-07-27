// Package logfmt turns a line of container output into its parts: when it
// happened, how bad it is, what it says, and what came along with it.
//
// Container logs arrive as text, but a great deal of it is structure that a
// program wrote for another program. A Go service logging through log/slog
// emits ninety characters of JSON to say six words. This package recovers the
// six words, and keeps the rest available rather than discarding it.
package logfmt

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"
)

// Level is how severe a log line claims to be.
type Level int

// Severities, ordered so they can be compared.
const (
	LevelUnknown Level = iota
	LevelTrace
	LevelDebug
	LevelInfo
	LevelWarn
	LevelError
	LevelFatal
)

// String returns a short, fixed-width-friendly name.
func (l Level) String() string {
	switch l {
	case LevelTrace:
		return "TRACE"
	case LevelDebug:
		return "DEBUG"
	case LevelInfo:
		return "INFO"
	case LevelWarn:
		return "WARN"
	case LevelError:
		return "ERROR"
	case LevelFatal:
		return "FATAL"
	default:
		return ""
	}
}

// Field is one key/value pair carried alongside a message.
type Field struct {
	Key   string
	Value string
}

// Entry is a log line taken apart.
type Entry struct {
	// Time is the timestamp the line carried, distinct from the one the
	// daemon records. Zero when the line had none.
	Time time.Time
	// Level is the severity the line claimed, LevelUnknown when it claimed
	// none.
	Level Level
	// Message is the human-readable part, with structure stripped away.
	Message string
	// Fields are the remaining key/value pairs, in the order written.
	Fields []Field
	// Structured reports whether the line was parsed as data rather than
	// merely scanned. It decides whether Fields is meaningful and whether
	// reformatting the line is safe.
	Structured bool
}

// Keys that carry each part, in the order they are looked for. Between them
// these cover slog, logrus, zap, zerolog, bunyan and pino, which is most of
// what a container is likely to emit.
var (
	timeKeys    = []string{"time", "timestamp", "ts", "@timestamp", "t"}
	levelKeys   = []string{"level", "severity", "lvl", "levelname", "log.level"}
	messageKeys = []string{"msg", "message", "event", "short_message"}
)

// Parse takes a line apart.
//
// A line that is a JSON object is read as data; anything else is scanned for a
// severity and a leading timestamp but otherwise left exactly as it is. The
// distinction matters: reformatting text that was never structured risks
// mangling output whose layout was deliberate.
func Parse(text string) Entry {
	if entry, ok := parseJSON(text); ok {
		return entry
	}
	return parsePlain(text)
}

// parseJSON reads a line written as a JSON object.
func parseJSON(text string) (Entry, bool) {
	trimmed := strings.TrimSpace(text)
	if !strings.HasPrefix(trimmed, "{") || !strings.HasSuffix(trimmed, "}") {
		return Entry{}, false
	}

	decoder := json.NewDecoder(strings.NewReader(trimmed))
	// Read pairs through the token stream rather than into a map, so the
	// fields keep the order the program wrote them in. A map would sort
	// them arbitrarily, and the order a program logs its fields in is
	// usually the order that makes them readable.
	if token, err := decoder.Token(); err != nil || token != json.Delim('{') {
		return Entry{}, false
	}

	entry := Entry{Structured: true}
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return Entry{}, false
		}
		key, ok := token.(string)
		if !ok {
			return Entry{}, false
		}
		var raw json.RawMessage
		if err := decoder.Decode(&raw); err != nil {
			return Entry{}, false
		}
		entry.absorb(key, raw)
	}
	if _, err := decoder.Token(); err != nil {
		// Trailing garbage after the object: not the structured line it
		// appeared to be.
		return Entry{}, false
	}
	return entry, true
}

// absorb files one decoded pair into the entry.
func (e *Entry) absorb(key string, raw json.RawMessage) {
	lower := strings.ToLower(key)

	if e.Time.IsZero() && contains(timeKeys, lower) {
		if parsed, ok := parseTimeValue(raw); ok {
			e.Time = parsed
			return
		}
	}
	if e.Level == LevelUnknown && contains(levelKeys, lower) {
		if value, ok := decodeString(raw); ok {
			if level := ParseLevel(value); level != LevelUnknown {
				e.Level = level
				return
			}
		}
	}
	if e.Message == "" && contains(messageKeys, lower) {
		if value, ok := decodeString(raw); ok {
			e.Message = value
			return
		}
	}
	e.Fields = append(e.Fields, Field{Key: key, Value: renderValue(raw)})
}

// parseTimeValue reads a timestamp written either as a string or as seconds
// since the epoch, which the popular loggers disagree about.
func parseTimeValue(raw json.RawMessage) (time.Time, bool) {
	if value, ok := decodeString(raw); ok {
		for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02 15:04:05.999999999 -0700 MST", "2006-01-02T15:04:05"} {
			if parsed, err := time.Parse(layout, value); err == nil {
				return parsed, true
			}
		}
		return time.Time{}, false
	}
	var seconds float64
	if err := json.Unmarshal(raw, &seconds); err == nil && seconds > 0 {
		whole, fraction := int64(seconds), seconds-float64(int64(seconds))
		return time.Unix(whole, int64(fraction*1e9)), true
	}
	return time.Time{}, false
}

// decodeString reads a JSON string, reporting whether the value was one.
func decodeString(raw json.RawMessage) (string, bool) {
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", false
	}
	return value, true
}

// renderValue turns a JSON value into something readable on one line.
func renderValue(raw json.RawMessage) string {
	if value, ok := decodeString(raw); ok {
		return value
	}
	// Numbers, booleans and null are already their own best rendering;
	// objects and arrays stay as compact JSON rather than being expanded
	// across a log view.
	return strings.TrimSpace(string(raw))
}

// ParseLevel recognises a severity by name, in whatever case and abbreviation
// the writer favoured.
func ParseLevel(value string) Level {
	switch strings.ToUpper(strings.TrimSpace(value)) {
	case "TRACE", "TRC":
		return LevelTrace
	case "DEBUG", "DBG", "D":
		return LevelDebug
	case "INFO", "INFORMATION", "INF", "I", "NOTICE", "LOG", "SYSTEM":
		return LevelInfo
	case "WARN", "WARNING", "WRN", "W":
		return LevelWarn
	case "ERROR", "ERR", "E", "SEVERE", "CRITICAL", "CRIT":
		return LevelError
	case "FATAL", "PANIC", "EMERGENCY", "ALERT":
		return LevelFatal
	default:
		return LevelUnknown
	}
}

// levelScanLimit bounds how far into a line a severity is looked for.
//
// Loggers put the severity near the front. Searching the whole line would
// colour a message that merely mentions the word "error" as an error, which
// is worse than missing one: it makes the colouring untrustworthy, and its
// only job is to be trusted at a glance.
const levelScanLimit = 64

// parsePlain scans an unstructured line, leaving its text alone.
func parsePlain(text string) Entry {
	entry := Entry{Message: text}

	if stamp, rest, ok := splitLeadingTimestamp(text); ok {
		// The daemon already records when the line arrived, and the view
		// can show that. Carrying the writer's own copy as data rather
		// than text stops the two being printed side by side.
		entry.Time = stamp
		entry.Message = rest
	}
	entry.Level = detectLevel(entry.Message)
	return entry
}

// detectLevel looks for a severity marked out as its own token.
func detectLevel(text string) Level {
	head := text
	if len(head) > levelScanLimit {
		head = head[:levelScanLimit]
	}

	// Bracketed, as MySQL and many Java loggers write it: [Warning].
	if open := strings.Index(head, "["); open >= 0 {
		if close := strings.Index(head[open:], "]"); close > 1 {
			if level := ParseLevel(head[open+1 : open+close]); level != LevelUnknown {
				return level
			}
		}
	}
	// Followed by a colon, as PostgreSQL and Python write it: LOG:, ERROR:.
	for _, word := range strings.Fields(head) {
		if trimmed, found := strings.CutSuffix(word, ":"); found {
			if level := ParseLevel(trimmed); level != LevelUnknown {
				return level
			}
		}
	}
	// A bare upper-case token, as many shell scripts and Go programs write
	// it. Upper case only: a message about a "warning" is not a warning.
	for _, word := range strings.Fields(head) {
		if word == strings.ToUpper(word) {
			if level := ParseLevel(word); level != LevelUnknown {
				return level
			}
		}
	}
	return LevelUnknown
}

// timestampLayouts are the leading-timestamp formats worth recognising, from
// the databases and runtimes that write one.
var timestampLayouts = []string{
	time.RFC3339Nano,
	time.RFC3339,
	"2006-01-02T15:04:05.999999Z",
	"2006-01-02 15:04:05.999 MST",
	"2006-01-02 15:04:05.999",
	"2006-01-02 15:04:05",
	"2006/01/02 15:04:05",
}

// splitLeadingTimestamp separates a timestamp at the start of a line from the
// rest of it.
func splitLeadingTimestamp(text string) (time.Time, string, bool) {
	trimmed := strings.TrimLeft(text, " \t")

	// Try progressively shorter prefixes: the layouts differ in how many
	// space-separated pieces they span.
	for pieces := 3; pieces >= 1; pieces-- {
		prefix, rest, ok := splitWords(trimmed, pieces)
		if !ok {
			continue
		}
		for _, layout := range timestampLayouts {
			if parsed, err := time.Parse(layout, prefix); err == nil {
				return parsed, rest, true
			}
		}
	}
	return time.Time{}, text, false
}

// splitWords cuts the first n space-separated pieces off s.
func splitWords(s string, n int) (prefix, rest string, ok bool) {
	index := 0
	for range n {
		for index < len(s) && s[index] == ' ' {
			index++
		}
		start := index
		for index < len(s) && s[index] != ' ' {
			index++
		}
		if index == start {
			return "", "", false
		}
	}
	return s[:index], strings.TrimLeft(s[index:], " "), true
}

// contains reports whether the list holds the value.
func contains(list []string, value string) bool {
	for _, item := range list {
		if item == value {
			return true
		}
	}
	return false
}

// Quote renders a field value, quoting it only when it would otherwise be
// ambiguous — an empty value, or one containing spaces.
func Quote(value string) string {
	if value == "" {
		return `""`
	}
	if strings.ContainsAny(value, " \t\"") {
		return strconv.Quote(value)
	}
	return value
}
