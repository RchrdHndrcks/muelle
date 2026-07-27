package logfmt

import (
	"strings"
	"testing"
	"time"
)

// fields renders an entry's fields for comparison.
func fields(e Entry) string {
	parts := make([]string, 0, len(e.Fields))
	for _, f := range e.Fields {
		parts = append(parts, f.Key+"="+f.Value)
	}
	return strings.Join(parts, " ")
}

// The motivating case: a Go service logging through log/slog spends ninety
// characters of JSON to say three words.
func TestParseSlogLine(t *testing.T) {
	entry := Parse(`{"time":"2026-07-27T20:55:34.503644157Z","level":"INFO","msg":"user connected","user_id":"263924de","count":1}`)

	if !entry.Structured {
		t.Fatal("a JSON object should parse as structured")
	}
	if entry.Message != "user connected" {
		t.Errorf("got %q, want the message alone", entry.Message)
	}
	if entry.Level != LevelInfo {
		t.Errorf("got %v, want INFO", entry.Level)
	}
	if got := entry.Time.Format(time.RFC3339); got != "2026-07-27T20:55:34Z" {
		t.Errorf("got %q, want the logged time", got)
	}
	if got := fields(entry); got != "user_id=263924de count=1" {
		t.Errorf("got %q, want the remaining pairs kept", got)
	}
}

// A map would reorder these arbitrarily. The order a program logs its fields
// in is usually the order that makes them readable.
func TestParsePreservesFieldOrder(t *testing.T) {
	entry := Parse(`{"msg":"m","zebra":1,"apple":2,"middle":3}`)

	if got := fields(entry); got != "zebra=1 apple=2 middle=3" {
		t.Errorf("got %q, want the order the program wrote", got)
	}
}

// zap and friends write the timestamp as epoch seconds rather than a string.
func TestParseNumericTimestamp(t *testing.T) {
	entry := Parse(`{"level":"error","ts":1780000000.5,"msg":"connection refused"}`)

	if entry.Time.IsZero() {
		t.Fatal("a numeric timestamp should still be recognised")
	}
	if entry.Level != LevelError || entry.Message != "connection refused" {
		t.Errorf("got %+v, want the level and message", entry)
	}
}

func TestParseAlternativeKeyNames(t *testing.T) {
	entry := Parse(`{"@timestamp":"2026-07-27T10:00:00Z","severity":"warning","message":"disk filling"}`)

	if entry.Level != LevelWarn {
		t.Errorf("got %v, want WARN from severity", entry.Level)
	}
	if entry.Message != "disk filling" {
		t.Errorf("got %q, want the message from the message key", entry.Message)
	}
	if entry.Time.IsZero() {
		t.Error("want the time from @timestamp")
	}
}

// Something that merely starts with a brace is not structured data, and
// reformatting it would mangle output whose layout was deliberate.
func TestParseRejectsNonObjectText(t *testing.T) {
	for _, text := range []string{
		`{not json at all`,
		`{"unterminated": `,
		`{"a":1} trailing words`,
		`[1,2,3]`,
		`plain text`,
	} {
		if entry := Parse(text); entry.Structured {
			t.Errorf("Parse(%q) claimed to be structured", text)
		}
	}
}

// An unstructured line keeps its text exactly, apart from a timestamp the
// daemon already records separately.
func TestParsePlainLineKeepsItsText(t *testing.T) {
	entry := Parse(`classifier ready: models loaded`)

	if entry.Structured {
		t.Error("plain text is not structured")
	}
	if entry.Message != "classifier ready: models loaded" {
		t.Errorf("got %q, want the line untouched", entry.Message)
	}
}

func TestParseMySQLLine(t *testing.T) {
	entry := Parse(`2026-07-27T20:44:05.113361Z 0 [Warning] [MY-011810] [Server] Insecure configuration`)

	if entry.Level != LevelWarn {
		t.Errorf("got %v, want WARN from the bracketed severity", entry.Level)
	}
	if entry.Time.IsZero() {
		t.Error("want the leading timestamp recognised")
	}
	if strings.HasPrefix(entry.Message, "2026") {
		t.Errorf("got %q, want the timestamp taken off the text", entry.Message)
	}
}

func TestParsePostgresLine(t *testing.T) {
	entry := Parse(`2026-07-27 14:13:48.437 UTC [1] LOG:  database system is ready`)

	if entry.Level != LevelInfo {
		t.Errorf("got %v, want LOG treated as informational", entry.Level)
	}
	if entry.Time.IsZero() {
		t.Error("want the leading timestamp recognised")
	}
}

func TestParseDetectsPanic(t *testing.T) {
	entry := Parse(`panic: runtime error: index out of range`)

	if entry.Level != LevelFatal {
		t.Errorf("got %v, want a panic treated as fatal", entry.Level)
	}
}

// Colouring a line red because its message mentions the word is worse than
// missing one: it makes the colouring untrustworthy, and being trustworthy at
// a glance is its only job.
func TestLevelNotInferredFromProse(t *testing.T) {
	for _, text := range []string{
		"the error rate is within limits",
		"no warnings were produced",
		"debugging is disabled",
	} {
		if level := Parse(text).Level; level != LevelUnknown {
			t.Errorf("Parse(%q) claimed level %v from ordinary prose", text, level)
		}
	}
}

// Loggers put the severity at the front; a mention far into a line is prose.
func TestLevelNotFoundBeyondTheScanLimit(t *testing.T) {
	text := strings.Repeat("x", levelScanLimit+10) + " ERROR"

	if level := Parse(text).Level; level != LevelUnknown {
		t.Errorf("got %v, want nothing found past the scan limit", level)
	}
}

func TestParseLevelAcceptsCommonSpellings(t *testing.T) {
	cases := map[string]Level{
		"ERROR": LevelError, "error": LevelError, "err": LevelError,
		"WARNING": LevelWarn, "warn": LevelWarn,
		"INFO": LevelInfo, "notice": LevelInfo,
		"DEBUG": LevelDebug, "trace": LevelTrace,
		"fatal": LevelFatal, "panic": LevelFatal,
		"": LevelUnknown, "banana": LevelUnknown,
	}

	for input, want := range cases {
		if got := ParseLevel(input); got != want {
			t.Errorf("ParseLevel(%q) = %v, want %v", input, got, want)
		}
	}
}

// Nested values have no useful multi-line rendering in a log view.
func TestParseRendersNestedValuesCompactly(t *testing.T) {
	entry := Parse(`{"msg":"m","user":{"id":7,"name":"ana"},"tags":["a","b"]}`)

	got := fields(entry)
	if !strings.Contains(got, `{"id":7,"name":"ana"}`) {
		t.Errorf("got %q, want the object kept as compact JSON", got)
	}
	if !strings.Contains(got, `["a","b"]`) {
		t.Errorf("got %q, want the array kept as compact JSON", got)
	}
}

func TestParseKeepsUnrecognisedTimeAsAField(t *testing.T) {
	entry := Parse(`{"time":"not a time","msg":"m"}`)

	if !entry.Time.IsZero() {
		t.Error("an unparseable timestamp should not become a time")
	}
	if !strings.Contains(fields(entry), "time=not a time") {
		t.Errorf("got %q, want the value kept as a field rather than dropped", fields(entry))
	}
}

// Dropping a message because the level was unrecognised would lose the line.
func TestParseKeepsUnrecognisedLevelAsAField(t *testing.T) {
	entry := Parse(`{"level":"verbose","msg":"m"}`)

	if entry.Level != LevelUnknown {
		t.Errorf("got %v, want the unrecognised level ignored", entry.Level)
	}
	if !strings.Contains(fields(entry), "level=verbose") {
		t.Errorf("got %q, want it kept as a field", fields(entry))
	}
}

func TestQuoteOnlyWhenAmbiguous(t *testing.T) {
	cases := map[string]string{
		"plain":     "plain",
		"":          `""`,
		"two words": `"two words"`,
		`has"quote`: `"has\"quote"`,
		"1234":      "1234",
	}

	for input, want := range cases {
		if got := Quote(input); got != want {
			t.Errorf("Quote(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestLevelNames(t *testing.T) {
	if got := LevelError.String(); got != "ERROR" {
		t.Errorf("got %q, want ERROR", got)
	}
	if got := LevelUnknown.String(); got != "" {
		t.Errorf("got %q, want an empty name so no column is drawn", got)
	}
}
