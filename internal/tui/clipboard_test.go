package tui

import (
	"bytes"
	"encoding/base64"
	"strings"
	"testing"
)

func TestCopySequenceEncodesText(t *testing.T) {
	got := CopySequence("shop-api")
	want := "\x1b]52;c;" + base64.StdEncoding.EncodeToString([]byte("shop-api")) + "\a"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// The payload must survive text that would otherwise terminate or corrupt the
// sequence — that is what the base64 encoding is for.
func TestCopySequenceRoundTripsAwkwardText(t *testing.T) {
	text := "semi;colons \a bells and \x1b escapes"

	sequence := CopySequence(text)
	payload := strings.TrimSuffix(strings.TrimPrefix(sequence, "\x1b]52;c;"), "\a")
	decoded, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		t.Fatalf("payload is not valid base64: %v", err)
	}
	if string(decoded) != text {
		t.Errorf("round trip got %q, want %q", decoded, text)
	}
	if strings.Count(sequence, "\a") != 1 {
		t.Error("raw BEL in the text must not leak into the sequence body")
	}
}

func TestCopyRidesOutWithTheNextFrame(t *testing.T) {
	var out bytes.Buffer
	screen := NewScreen(&out, 20, 3, true)
	screen.EnableClipboard()

	if !screen.Copy("hello") {
		t.Fatal("Copy should succeed once the clipboard is enabled")
	}
	if out.Len() != 0 {
		t.Fatal("Copy must queue, not write: a write here could land inside a frame")
	}

	if err := screen.Render([]string{"line"}); err != nil {
		t.Fatalf("Render: %v", err)
	}
	frame := out.String()
	sequence := CopySequence("hello")
	if !strings.HasSuffix(frame, SyncOff+sequence) {
		t.Errorf("the sequence should follow the closing sync marker, keeping the frame bracketing intact; got %q", frame)
	}

	// The sequence is a one-shot: emitting it again on every frame would
	// rewrite the clipboard each refresh.
	out.Reset()
	if err := screen.Render([]string{"line"}); err != nil {
		t.Fatalf("Render: %v", err)
	}
	if strings.Contains(out.String(), "\x1b]52") {
		t.Error("a queued copy must be emitted exactly once")
	}
}

func TestCopyRefusesWithoutATerminal(t *testing.T) {
	var out bytes.Buffer
	screen := NewScreen(&out, 20, 3, true)

	if screen.Copy("hello") {
		t.Error("Copy should refuse when no terminal is reading the output")
	}
	if err := screen.Render([]string{"line"}); err != nil {
		t.Fatalf("Render: %v", err)
	}
	if strings.Contains(out.String(), "\x1b]52") {
		t.Error("no OSC 52 sequence should reach a non-terminal writer")
	}
}
