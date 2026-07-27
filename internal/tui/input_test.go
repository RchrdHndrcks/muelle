package tui

import (
	"io"
	"sync"
	"syscall"
	"testing"
	"time"
)

func TestDecodeKeyPrintableRune(t *testing.T) {
	key, n := DecodeKey([]byte("j"))

	if !key.IsRune('j') || n != 1 {
		t.Errorf("got %+v consuming %d, want the rune j consuming 1", key, n)
	}
}

func TestDecodeKeyMultiByteRune(t *testing.T) {
	key, n := DecodeKey([]byte("ñ"))

	if !key.IsRune('ñ') || n != 2 {
		t.Errorf("got %+v consuming %d, want ñ consuming 2 bytes", key, n)
	}
}

func TestDecodeKeyArrows(t *testing.T) {
	cases := map[string]KeyType{
		"\x1b[A": KeyUp,
		"\x1b[B": KeyDown,
		"\x1b[C": KeyRight,
		"\x1b[D": KeyLeft,
		"\x1bOA": KeyUp, // application cursor mode
		"\x1bOD": KeyLeft,
	}
	for input, want := range cases {
		key, n := DecodeKey([]byte(input))
		if key.Type != want {
			t.Errorf("DecodeKey(%q) = %v, want %v", input, key.Type, want)
		}
		if n != len(input) {
			t.Errorf("DecodeKey(%q) consumed %d, want %d", input, n, len(input))
		}
	}
}

func TestDecodeKeyNavigationKeys(t *testing.T) {
	cases := map[string]KeyType{
		"\x1b[5~": KeyPageUp,
		"\x1b[6~": KeyPageDown,
		"\x1b[3~": KeyDelete,
		"\x1b[H":  KeyHome,
		"\x1b[F":  KeyEnd,
		"\x1b[1~": KeyHome,
		"\x1b[4~": KeyEnd,
		"\x1b[Z":  KeyShiftTab,
	}
	for input, want := range cases {
		if key, _ := DecodeKey([]byte(input)); key.Type != want {
			t.Errorf("DecodeKey(%q) = %v, want %v", input, key.Type, want)
		}
	}
}

// Modified variants (shift+arrow and friends) carry extra parameters. muelle
// binds none of them, but they must still decode to the base key rather than
// leaving stray bytes to be misread as text.
func TestDecodeKeyIgnoresModifierParameters(t *testing.T) {
	key, n := DecodeKey([]byte("\x1b[1;2A"))

	if key.Type != KeyUp {
		t.Errorf("got %v, want the base Up key", key.Type)
	}
	if n != 6 {
		t.Errorf("consumed %d bytes, want the whole 6-byte sequence", n)
	}
}

func TestDecodeKeyControlKeys(t *testing.T) {
	cases := map[byte]KeyType{
		0x03: KeyCtrlC,
		0x04: KeyCtrlD,
		0x15: KeyCtrlU,
		0x12: KeyCtrlR,
		0x0d: KeyEnter,
		0x0a: KeyEnter,
		0x09: KeyTab,
		0x7f: KeyBackspace,
	}
	for input, want := range cases {
		if key, _ := DecodeKey([]byte{input}); key.Type != want {
			t.Errorf("DecodeKey(%#x) = %v, want %v", input, key.Type, want)
		}
	}
}

func TestDecodeKeyLoneEscapeIsEscape(t *testing.T) {
	key, n := DecodeKey([]byte{0x1b})

	if key.Type != KeyEscape || n != 1 {
		t.Errorf("got %+v consuming %d, want Escape consuming 1", key, n)
	}
}

// A sequence split across two reads must not be decoded as garbage; the
// decoder signals "need more" by consuming nothing.
func TestDecodeKeyIncompleteSequenceConsumesNothing(t *testing.T) {
	key, n := DecodeKey([]byte("\x1b[1;"))

	if n != 0 {
		t.Errorf("consumed %d bytes, want 0 so the caller waits for the rest", n)
	}
	if key.Type != KeyUnknown {
		t.Errorf("got %v, want no key yet", key.Type)
	}
}

// One read can hold several key presses, for example when input is pasted or
// a key repeats faster than the loop drains it.
func TestDecodeKeyHandlesBatchedInput(t *testing.T) {
	buf := []byte("jk\x1b[Bq")
	var got []KeyType

	for len(buf) > 0 {
		key, n := DecodeKey(buf)
		if n == 0 {
			t.Fatal("decoder stalled on complete input")
		}
		got = append(got, key.Type)
		buf = buf[n:]
	}

	if len(got) != 4 {
		t.Fatalf("got %d keys, want 4: %v", len(got), got)
	}
	if got[2] != KeyDown {
		t.Errorf("got %v at index 2, want the arrow between the runes", got[2])
	}
}

// readerFromScript feeds pre-canned reads, then blocks like a terminal with
// nothing to say — returning no bytes and no error, the way a timed-out read
// behaves.
func readerFromScript(chunks ...string) (func([]byte) (int, error), chan struct{}) {
	index := 0
	gate := make(chan struct{})
	return func(p []byte) (int, error) {
		if index < len(chunks) {
			n := copy(p, chunks[index])
			index++
			return n, nil
		}
		// Idle: behave like a read that timed out.
		select {
		case <-gate:
		case <-time.After(5 * time.Millisecond):
		}
		return 0, nil
	}, gate
}

func TestKeyReaderStreamsDecodedKeys(t *testing.T) {
	read, _ := readerFromScript("ab\x1b[A")
	reader := NewKeyReader(read)
	defer reader.Stop()

	var got []Key
	timeout := time.After(2 * time.Second)
	for len(got) < 3 {
		select {
		case key := <-reader.Keys():
			got = append(got, key)
		case <-timeout:
			t.Fatalf("timed out after %d keys", len(got))
		}
	}

	if !got[0].IsRune('a') || !got[1].IsRune('b') || got[2].Type != KeyUp {
		t.Errorf("got %+v, want a, b, Up", got)
	}
}

// The reason this type exists: a child process cannot share the terminal, so
// Pause must guarantee the reader is not in a read before it returns.
func TestPauseStopsConsumingInput(t *testing.T) {
	var (
		mu    sync.Mutex
		reads int
	)
	reader := NewKeyReader(func(p []byte) (int, error) {
		mu.Lock()
		reads++
		mu.Unlock()
		time.Sleep(2 * time.Millisecond)
		return 0, nil
	})
	defer reader.Stop()

	// Let it get going, then stand it down.
	time.Sleep(30 * time.Millisecond)
	reader.Pause()

	mu.Lock()
	atPause := reads
	mu.Unlock()

	time.Sleep(50 * time.Millisecond)

	mu.Lock()
	afterPause := reads
	mu.Unlock()

	if afterPause != atPause {
		t.Errorf("reader performed %d further reads while paused; the child no longer has the terminal to itself",
			afterPause-atPause)
	}
}

func TestResumeTakesTheTerminalBack(t *testing.T) {
	read, _ := readerFromScript("x")
	reader := NewKeyReader(read)
	defer reader.Stop()

	reader.Pause()
	reader.Resume()

	select {
	case key := <-reader.Keys():
		if !key.IsRune('x') {
			t.Errorf("got %+v, want the key delivered after resuming", key)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no keys after Resume; the reader did not take the terminal back")
	}
}

// Suspend and Resume are wired to terminal handover, which can be re-entered;
// neither call should deadlock or double-send.
func TestPauseAndResumeAreIdempotent(t *testing.T) {
	read, _ := readerFromScript()
	reader := NewKeyReader(read)
	defer reader.Stop()

	done := make(chan struct{})
	go func() {
		defer close(done)
		reader.Resume() // never paused
		reader.Pause()
		reader.Pause() // already paused
		reader.Resume()
		reader.Resume() // already resumed
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Pause/Resume deadlocked when called out of order")
	}
}

// A half-read escape sequence must not survive the handover: by the time the
// terminal comes back it is stale, and decoding it against whatever arrives
// next would invent a keypress the user never made.
func TestPauseDiscardsPartialSequence(t *testing.T) {
	chunks := []string{"\x1b["} // the start of an arrow key, never finished
	index := 0
	resumed := make(chan struct{})
	reader := NewKeyReader(func(p []byte) (int, error) {
		if index < len(chunks) {
			n := copy(p, chunks[index])
			index++
			return n, nil
		}
		select {
		case <-resumed:
			n := copy(p, "z")
			return n, nil
		default:
		}
		time.Sleep(2 * time.Millisecond)
		return 0, nil
	})
	defer reader.Stop()

	time.Sleep(30 * time.Millisecond)
	reader.Pause()
	reader.Resume()
	close(resumed)

	select {
	case key := <-reader.Keys():
		if !key.IsRune('z') {
			t.Errorf("got %+v, want the stale sequence dropped and z delivered", key)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no key after resuming")
	}
}

func TestStopClosesTheChannel(t *testing.T) {
	read, _ := readerFromScript()
	reader := NewKeyReader(read)

	reader.Stop()

	select {
	case _, open := <-reader.Keys():
		if open {
			t.Error("expected no further keys after Stop")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the key channel was not closed by Stop")
	}
}

func TestStopIsSafeToCallTwice(t *testing.T) {
	read, _ := readerFromScript()
	reader := NewKeyReader(read)

	reader.Stop()
	reader.Stop()
}

// A read interrupted by a signal has lost nothing and should simply be retried;
// treating it as fatal would kill the keyboard on every window resize.
func TestReaderSurvivesInterruptedReads(t *testing.T) {
	calls := 0
	reader := NewKeyReader(func(p []byte) (int, error) {
		calls++
		switch calls {
		case 1:
			return 0, syscall.EINTR
		case 2:
			return copy(p, "k"), nil
		}
		time.Sleep(2 * time.Millisecond)
		return 0, nil
	})
	defer reader.Stop()

	select {
	case key := <-reader.Keys():
		if !key.IsRune('k') {
			t.Errorf("got %+v, want k", key)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the reader gave up after an interrupted read")
	}
}

// A genuine read error ends the reader rather than spinning on it.
func TestReaderStopsOnReadError(t *testing.T) {
	reader := NewKeyReader(func([]byte) (int, error) { return 0, io.ErrUnexpectedEOF })

	select {
	case _, open := <-reader.Keys():
		if open {
			t.Error("expected no keys from a failing reader")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the channel was not closed after a read error")
	}
}
