package docker

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Log stream identifiers used in the multiplexed frame header.
const (
	StreamStdin  = 0
	StreamStdout = 1
	StreamStderr = 2
)

// maxLineBytes caps how much of a newline-free run we buffer before emitting
// it as a line anyway. Without it a container printing an unbounded stream
// with no newlines would grow the buffer until the process died.
const maxLineBytes = 64 << 10

// LogLine is a single line of container output.
type LogLine struct {
	// Stream is StreamStdout or StreamStderr. Containers with a TTY
	// combine both onto stdout, so everything reports as StreamStdout.
	Stream int
	// Time is the daemon-side timestamp, zero when it could not be parsed.
	Time time.Time
	// Text is the line with any timestamp prefix and trailing CR removed.
	Text string
}

// Stderr reports whether the line came from the container's stderr.
func (l LogLine) Stderr() bool { return l.Stream == StreamStderr }

// LogOptions controls which portion of a container's logs to fetch.
type LogOptions struct {
	// Follow keeps the stream open and delivers new output as it is
	// written.
	Follow bool
	// Tail is how many trailing lines to fetch first. Zero means all.
	Tail int
	// Since limits output to entries after this time, when non-zero.
	Since time.Time
	// TTY must match the container's Config.Tty. It decides whether the
	// stream is multiplexed, and getting it wrong yields binary garbage in
	// the output.
	TTY bool
}

// Logs opens a container's log stream. The returned LogScanner must be closed
// by the caller.
//
// Timestamps are always requested and parsed off the front of each line, so
// the UI can toggle showing them without reopening the stream.
func (c *Client) Logs(ctx context.Context, id string, opts LogOptions) (*LogScanner, error) {
	query := url.Values{
		"stdout":     {"1"},
		"stderr":     {"1"},
		"timestamps": {"1"},
	}
	if opts.Follow {
		query.Set("follow", "1")
	}
	if opts.Tail > 0 {
		query.Set("tail", strconv.Itoa(opts.Tail))
	} else {
		query.Set("tail", "all")
	}
	if !opts.Since.IsZero() {
		query.Set("since", strconv.FormatInt(opts.Since.Unix(), 10))
	}
	body, err := c.do(ctx, http.MethodGet, "/containers/"+id+"/logs", query, nil)
	if err != nil {
		return nil, err
	}
	return NewLogScanner(body, opts.TTY), nil
}

// LogScanner turns a raw log stream into lines, transparently handling
// Docker's multiplexed framing.
//
// Usage mirrors bufio.Scanner:
//
//	for scanner.Scan() {
//	        line := scanner.Line()
//	}
//	err := scanner.Err()
type LogScanner struct {
	src     io.ReadCloser
	reader  *bufio.Reader
	tty     bool
	pending []LogLine
	current LogLine
	err     error
	// partial holds the incomplete trailing line per stream id. Frame
	// boundaries do not align with line boundaries, so a line can span
	// several frames and frames can carry several lines.
	partial map[int][]byte
}

// NewLogScanner reads lines from r. When tty is true the stream is treated as
// raw output; otherwise Docker's 8-byte frame headers are stripped and each
// line is attributed to stdout or stderr.
func NewLogScanner(r io.ReadCloser, tty bool) *LogScanner {
	return &LogScanner{
		src:     r,
		reader:  bufio.NewReaderSize(r, 32<<10),
		tty:     tty,
		partial: make(map[int][]byte, 2),
	}
}

// Scan advances to the next line, returning false at end of stream or on
// error.
func (s *LogScanner) Scan() bool {
	for {
		if len(s.pending) > 0 {
			s.current = s.pending[0]
			s.pending = s.pending[1:]
			return true
		}
		if s.err != nil {
			return false
		}
		if s.tty {
			s.readTTY()
			continue
		}
		s.readFrame()
	}
}

// Line returns the line found by the most recent call to Scan.
func (s *LogScanner) Line() LogLine { return s.current }

// Err returns the first error encountered, excluding the expected io.EOF and
// the context cancellation that closing the stream produces.
func (s *LogScanner) Err() error {
	if errors.Is(s.err, io.EOF) || errors.Is(s.err, context.Canceled) {
		return nil
	}
	// A closed stream surfaces as a read on a closed connection; that is
	// how the UI stops following, not a failure worth reporting.
	if s.err != nil && strings.Contains(s.err.Error(), "use of closed network connection") {
		return nil
	}
	return s.err
}

// Close terminates the underlying stream, unblocking a Scan in progress.
func (s *LogScanner) Close() error { return s.src.Close() }

// readTTY handles the unmultiplexed case: the payload is the output verbatim.
func (s *LogScanner) readTTY() {
	line, err := s.reader.ReadString('\n')
	if line != "" {
		s.pending = append(s.pending, parseLogLine(StreamStdout, []byte(line)))
	}
	if err != nil {
		s.err = err
		s.flushPartial()
	}
}

// readFrame reads one multiplexed frame and appends whatever complete lines it
// yields.
//
// Frame layout: [0]=stream id, [1:4]=zero padding, [4:8]=big-endian payload
// length, followed by that many payload bytes.
func (s *LogScanner) readFrame() {
	var header [8]byte
	if _, err := io.ReadFull(s.reader, header[:]); err != nil {
		s.err = normalizeEOF(err)
		s.flushPartial()
		return
	}
	stream := int(header[0])
	if stream > StreamStderr {
		// Docker only ever emits stream ids 0-2. Anything else means we
		// are demultiplexing a stream that was not multiplexed, almost
		// always because the container's TTY flag was misread.
		s.err = fmt.Errorf("docker: invalid log frame header %#v (stream is not multiplexed; check the container's TTY setting)", header)
		s.flushPartial()
		return
	}
	size := binary.BigEndian.Uint32(header[4:])
	if size == 0 {
		return
	}
	payload := make([]byte, size)
	if _, err := io.ReadFull(s.reader, payload); err != nil {
		s.err = normalizeEOF(err)
		s.flushPartial()
		return
	}
	s.appendPayload(stream, payload)
}

// appendPayload splits a frame payload into lines, carrying any incomplete
// trailing line over to the next frame on the same stream.
func (s *LogScanner) appendPayload(stream int, payload []byte) {
	buf := append(s.partial[stream], payload...)
	for {
		idx := bytes.IndexByte(buf, '\n')
		if idx < 0 {
			break
		}
		s.pending = append(s.pending, parseLogLine(stream, buf[:idx+1]))
		buf = buf[idx+1:]
	}
	if len(buf) > maxLineBytes {
		// Emit the oversized run rather than buffering without bound.
		s.pending = append(s.pending, parseLogLine(stream, buf))
		buf = nil
	}
	if len(buf) == 0 {
		delete(s.partial, stream)
		return
	}
	// Copy so the retained slice does not alias the payload buffer.
	s.partial[stream] = bytes.Clone(buf)
}

// flushPartial emits any buffered incomplete lines at end of stream. A
// container's final line often lacks a trailing newline and would otherwise be
// lost.
func (s *LogScanner) flushPartial() {
	for _, stream := range []int{StreamStdout, StreamStderr} {
		if buf := s.partial[stream]; len(buf) > 0 {
			s.pending = append(s.pending, parseLogLine(stream, buf))
			delete(s.partial, stream)
		}
	}
}

// parseLogLine strips the trailing newline and splits off the RFC3339Nano
// timestamp the daemon prefixes when timestamps=1.
func parseLogLine(stream int, raw []byte) LogLine {
	text := strings.TrimRight(string(raw), "\r\n")
	line := LogLine{Stream: stream, Text: text}

	stamp, rest, found := strings.Cut(text, " ")
	if !found {
		return line
	}
	parsed, err := time.Parse(time.RFC3339Nano, stamp)
	if err != nil {
		return line
	}
	line.Time = parsed
	line.Text = rest
	return line
}

// normalizeEOF maps a truncated frame onto plain EOF. A stream cut mid-frame
// (the container was removed, the daemon restarted) is an ordinary end of
// stream from the UI's point of view.
func normalizeEOF(err error) error {
	if errors.Is(err, io.ErrUnexpectedEOF) {
		return io.EOF
	}
	return err
}
