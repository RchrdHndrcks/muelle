package tui

import (
	"io"
	"strings"
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

func TestReadKeysStreamsAndClosesOnEOF(t *testing.T) {
	keys := ReadKeys(strings.NewReader("ab\x1b[A"))

	var got []Key
	timeout := time.After(2 * time.Second)
	for {
		select {
		case key, ok := <-keys:
			if !ok {
				if len(got) != 3 {
					t.Fatalf("got %d keys, want 3: %+v", len(got), got)
				}
				if got[2].Type != KeyUp {
					t.Errorf("got %v, want Up last", got[2].Type)
				}
				return
			}
			got = append(got, key)
		case <-timeout:
			t.Fatal("timed out waiting for keys")
		}
	}
}

func TestReadKeysClosesChannelWhenReaderFails(t *testing.T) {
	keys := ReadKeys(failingReader{})

	select {
	case _, ok := <-keys:
		if ok {
			t.Error("expected no keys from a failing reader")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("channel was not closed after a read error")
	}
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }
