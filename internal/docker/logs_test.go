package docker

import (
	"encoding/binary"
	"io"
	"strings"
	"testing"
	"time"
)

// frame builds one multiplexed log frame the way the daemon does.
func frame(stream int, payload string) []byte {
	header := make([]byte, 8)
	header[0] = byte(stream)
	binary.BigEndian.PutUint32(header[4:], uint32(len(payload)))
	return append(header, payload...)
}

// collect drains a scanner into a slice.
func collect(t *testing.T, s *LogScanner) []LogLine {
	t.Helper()
	var lines []LogLine
	for s.Scan() {
		lines = append(lines, s.Line())
	}
	if err := s.Err(); err != nil {
		t.Fatalf("scanner error: %v", err)
	}
	return lines
}

func TestLogScannerDemuxesStreams(t *testing.T) {
	var stream []byte
	stream = append(stream, frame(StreamStdout, "listening on :8080\n")...)
	stream = append(stream, frame(StreamStderr, "connection refused\n")...)
	stream = append(stream, frame(StreamStdout, "ready\n")...)

	lines := collect(t, NewLogScanner(io.NopCloser(strings.NewReader(string(stream))), false))

	if len(lines) != 3 {
		t.Fatalf("got %d lines, want 3: %+v", len(lines), lines)
	}
	if lines[0].Text != "listening on :8080" || lines[0].Stderr() {
		t.Errorf("line 0 = %+v, want stdout text without the newline", lines[0])
	}
	if !lines[1].Stderr() {
		t.Errorf("line 1 = %+v, want it attributed to stderr", lines[1])
	}
}

func TestLogScannerSplitsMultipleLinesInOneFrame(t *testing.T) {
	stream := frame(StreamStdout, "one\ntwo\nthree\n")

	lines := collect(t, NewLogScanner(io.NopCloser(strings.NewReader(string(stream))), false))

	if len(lines) != 3 {
		t.Fatalf("got %d lines, want 3: %+v", len(lines), lines)
	}
	if lines[2].Text != "three" {
		t.Errorf("got %q, want \"three\"", lines[2].Text)
	}
}

// A line can straddle several frames; the daemon flushes on write boundaries,
// not line boundaries.
func TestLogScannerJoinsLineSpanningFrames(t *testing.T) {
	var stream []byte
	stream = append(stream, frame(StreamStdout, "starting up")...)
	stream = append(stream, frame(StreamStdout, " and then")...)
	stream = append(stream, frame(StreamStdout, " done\n")...)

	lines := collect(t, NewLogScanner(io.NopCloser(strings.NewReader(string(stream))), false))

	if len(lines) != 1 {
		t.Fatalf("got %d lines, want 1: %+v", len(lines), lines)
	}
	if lines[0].Text != "starting up and then done" {
		t.Errorf("got %q, want the fragments joined", lines[0].Text)
	}
}

// Interleaved partial writes must not bleed across streams.
func TestLogScannerKeepsPartialLinesPerStream(t *testing.T) {
	var stream []byte
	stream = append(stream, frame(StreamStdout, "out-part")...)
	stream = append(stream, frame(StreamStderr, "err-part")...)
	stream = append(stream, frame(StreamStdout, "-end\n")...)
	stream = append(stream, frame(StreamStderr, "-end\n")...)

	lines := collect(t, NewLogScanner(io.NopCloser(strings.NewReader(string(stream))), false))

	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2: %+v", len(lines), lines)
	}
	if lines[0].Text != "out-part-end" || lines[0].Stderr() {
		t.Errorf("line 0 = %+v, want the stdout halves joined", lines[0])
	}
	if lines[1].Text != "err-part-end" || !lines[1].Stderr() {
		t.Errorf("line 1 = %+v, want the stderr halves joined", lines[1])
	}
}

// The last line of a container's output usually has no trailing newline.
func TestLogScannerEmitsUnterminatedFinalLine(t *testing.T) {
	stream := frame(StreamStdout, "no newline at the end")

	lines := collect(t, NewLogScanner(io.NopCloser(strings.NewReader(string(stream))), false))

	if len(lines) != 1 || lines[0].Text != "no newline at the end" {
		t.Fatalf("got %+v, want the trailing fragment flushed", lines)
	}
}

func TestLogScannerParsesTimestamps(t *testing.T) {
	stream := frame(StreamStdout, "2026-07-27T10:11:12.123456789Z hello world\n")

	lines := collect(t, NewLogScanner(io.NopCloser(strings.NewReader(string(stream))), false))

	if len(lines) != 1 {
		t.Fatalf("got %d lines, want 1", len(lines))
	}
	if lines[0].Text != "hello world" {
		t.Errorf("got %q, want the timestamp stripped from the text", lines[0].Text)
	}
	want := time.Date(2026, 7, 27, 10, 11, 12, 123456789, time.UTC)
	if !lines[0].Time.Equal(want) {
		t.Errorf("got time %v, want %v", lines[0].Time, want)
	}
}

// A line that merely starts with a word must not be mistaken for a timestamp.
func TestLogScannerLeavesUntimestampedLineIntact(t *testing.T) {
	stream := frame(StreamStdout, "ERROR something failed\n")

	lines := collect(t, NewLogScanner(io.NopCloser(strings.NewReader(string(stream))), false))

	if lines[0].Text != "ERROR something failed" {
		t.Errorf("got %q, want the full line kept", lines[0].Text)
	}
	if !lines[0].Time.IsZero() {
		t.Errorf("got time %v, want zero", lines[0].Time)
	}
}

func TestLogScannerTTYModeReadsRawLines(t *testing.T) {
	raw := "plain one\nplain two\n"

	lines := collect(t, NewLogScanner(io.NopCloser(strings.NewReader(raw)), true))

	if len(lines) != 2 || lines[0].Text != "plain one" {
		t.Fatalf("got %+v, want two raw lines", lines)
	}
}

// Demultiplexing a TTY stream would otherwise emit binary garbage; the header
// check turns it into a diagnosable error.
func TestLogScannerRejectsUnmultiplexedStream(t *testing.T) {
	scanner := NewLogScanner(io.NopCloser(strings.NewReader("this is plain text, not framed\n")), false)

	for scanner.Scan() {
	}

	err := scanner.Err()
	if err == nil {
		t.Fatal("expected an error for an unframed stream")
	}
	if !strings.Contains(err.Error(), "TTY") {
		t.Errorf("got %q, want the message to point at the TTY setting", err)
	}
}

// Truncation mid-frame (daemon restart, container removed) is an ordinary end
// of stream, not an error to show the user.
func TestLogScannerTreatsTruncatedFrameAsEOF(t *testing.T) {
	stream := frame(StreamStdout, "complete\n")
	stream = append(stream, 1, 0, 0, 0, 0, 0) // header cut short

	scanner := NewLogScanner(io.NopCloser(strings.NewReader(string(stream))), false)
	lines := collect(t, scanner)

	if len(lines) != 1 || lines[0].Text != "complete" {
		t.Errorf("got %+v, want the complete line and no error", lines)
	}
}

func TestLogScannerCapsUnboundedLine(t *testing.T) {
	huge := strings.Repeat("x", maxLineBytes+100)
	stream := frame(StreamStdout, huge)

	lines := collect(t, NewLogScanner(io.NopCloser(strings.NewReader(string(stream))), false))

	if len(lines) != 1 {
		t.Fatalf("got %d lines, want the oversized run emitted once", len(lines))
	}
	if len(lines[0].Text) != len(huge) {
		t.Errorf("got %d bytes, want %d", len(lines[0].Text), len(huge))
	}
}
