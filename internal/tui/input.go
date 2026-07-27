package tui

import (
	"io"
	"unicode/utf8"
)

// KeyType identifies a key press.
type KeyType int

// Recognised keys. Anything printable arrives as KeyRune with Rune set.
const (
	KeyRune KeyType = iota
	KeyEnter
	KeyEscape
	KeyBackspace
	KeyTab
	KeyShiftTab
	KeyUp
	KeyDown
	KeyLeft
	KeyRight
	KeyHome
	KeyEnd
	KeyPageUp
	KeyPageDown
	KeyDelete
	KeyCtrlC
	KeyCtrlD
	KeyCtrlU
	KeyCtrlR
	KeyCtrlL
	KeyCtrlF
	KeyCtrlB
	KeyUnknown
)

// Key is a decoded key press.
type Key struct {
	Type KeyType
	Rune rune
}

// IsRune reports whether the key is the given printable character.
func (k Key) IsRune(r rune) bool { return k.Type == KeyRune && k.Rune == r }

// Control byte values that arrive as single bytes in raw mode.
const (
	ctrlB     = 0x02
	ctrlC     = 0x03
	ctrlD     = 0x04
	ctrlF     = 0x06
	tab       = 0x09
	ctrlL     = 0x0c
	enter     = 0x0d
	newline   = 0x0a
	ctrlR     = 0x12
	ctrlU     = 0x15
	escape    = 0x1b
	backspace = 0x7f
)

// DecodeKey decodes the first key press in b, returning the key and how many
// bytes it consumed. A consumed count of zero means b holds an incomplete
// sequence and the caller should read more input before trying again.
//
// Terminals deliver special keys as multi-byte escape sequences, and a single
// read can contain several keys (or half of one) — hence returning the byte
// count rather than decoding the whole buffer.
func DecodeKey(b []byte) (Key, int) {
	if len(b) == 0 {
		return Key{Type: KeyUnknown}, 0
	}

	switch b[0] {
	case enter, newline:
		return Key{Type: KeyEnter}, 1
	case tab:
		return Key{Type: KeyTab}, 1
	case backspace, 0x08:
		return Key{Type: KeyBackspace}, 1
	case ctrlC:
		return Key{Type: KeyCtrlC}, 1
	case ctrlD:
		return Key{Type: KeyCtrlD}, 1
	case ctrlU:
		return Key{Type: KeyCtrlU}, 1
	case ctrlR:
		return Key{Type: KeyCtrlR}, 1
	case ctrlL:
		return Key{Type: KeyCtrlL}, 1
	case ctrlF:
		return Key{Type: KeyCtrlF}, 1
	case ctrlB:
		return Key{Type: KeyCtrlB}, 1
	case escape:
		return decodeEscape(b)
	}

	if b[0] < 32 {
		return Key{Type: KeyUnknown}, 1
	}

	r, size := utf8.DecodeRune(b)
	if r == utf8.RuneError && size <= 1 {
		if !utf8.FullRune(b) {
			// A multi-byte rune split across reads; wait for the rest.
			return Key{Type: KeyUnknown}, 0
		}
		return Key{Type: KeyUnknown}, 1
	}
	return Key{Type: KeyRune, Rune: r}, size
}

// decodeEscape handles sequences introduced by ESC.
func decodeEscape(b []byte) (Key, int) {
	// A lone ESC. Ambiguous by nature: it could be the start of a sequence
	// whose remainder has not arrived yet. The reader resolves this by
	// only calling with a complete read, so a solitary ESC means the user
	// pressed Escape.
	if len(b) == 1 {
		return Key{Type: KeyEscape}, 1
	}

	switch b[1] {
	case '[':
		return decodeCSI(b)
	case 'O':
		// SS3, used by some terminals for arrows in application mode.
		if len(b) < 3 {
			return Key{Type: KeyEscape}, 1
		}
		switch b[2] {
		case 'A':
			return Key{Type: KeyUp}, 3
		case 'B':
			return Key{Type: KeyDown}, 3
		case 'C':
			return Key{Type: KeyRight}, 3
		case 'D':
			return Key{Type: KeyLeft}, 3
		case 'H':
			return Key{Type: KeyHome}, 3
		case 'F':
			return Key{Type: KeyEnd}, 3
		}
		return Key{Type: KeyUnknown}, 3
	}
	// ESC followed by a printable character is Alt+key, which muelle does
	// not bind; treat the ESC as its own key press.
	return Key{Type: KeyEscape}, 1
}

// decodeCSI handles ESC [ ... sequences.
func decodeCSI(b []byte) (Key, int) {
	// Find the final byte, which terminates the sequence.
	end := -1
	for i := 2; i < len(b); i++ {
		if b[i] >= 0x40 && b[i] <= 0x7e {
			end = i
			break
		}
	}
	if end < 0 {
		// Incomplete; ask for more bytes.
		return Key{Type: KeyUnknown}, 0
	}
	n := end + 1
	params := string(b[2:end])

	switch b[end] {
	case 'A':
		return Key{Type: KeyUp}, n
	case 'B':
		return Key{Type: KeyDown}, n
	case 'C':
		return Key{Type: KeyRight}, n
	case 'D':
		return Key{Type: KeyLeft}, n
	case 'H':
		return Key{Type: KeyHome}, n
	case 'F':
		return Key{Type: KeyEnd}, n
	case 'Z':
		return Key{Type: KeyShiftTab}, n
	case '~':
		// Numbered keys: ESC [ <n> ~. Modifiers may follow as ";2" etc,
		// which we ignore — muelle binds no modified variants.
		switch leadingNumber(params) {
		case 1, 7:
			return Key{Type: KeyHome}, n
		case 2:
			return Key{Type: KeyUnknown}, n // Insert
		case 3:
			return Key{Type: KeyDelete}, n
		case 4, 8:
			return Key{Type: KeyEnd}, n
		case 5:
			return Key{Type: KeyPageUp}, n
		case 6:
			return Key{Type: KeyPageDown}, n
		}
	}
	return Key{Type: KeyUnknown}, n
}

// leadingNumber parses the numeric prefix of a CSI parameter string, stopping
// at the first ';' so modifier suffixes are ignored.
func leadingNumber(params string) int {
	value := 0
	for i := 0; i < len(params); i++ {
		if params[i] < '0' || params[i] > '9' {
			break
		}
		value = value*10 + int(params[i]-'0')
	}
	return value
}

// ReadKeys decodes key presses from r and sends them on the returned channel
// until r reports an error, at which point the channel is closed.
//
// This runs on its own goroutine so a blocking read never stalls the event
// loop: in raw mode read(2) parks until a byte arrives, which could be
// minutes.
func ReadKeys(r io.Reader) <-chan Key {
	keys := make(chan Key, 16)
	go func() {
		defer close(keys)
		buf := make([]byte, 0, 256)
		chunk := make([]byte, 256)
		for {
			n, err := r.Read(chunk)
			if n > 0 {
				buf = append(buf, chunk[:n]...)
				for len(buf) > 0 {
					key, consumed := DecodeKey(buf)
					if consumed == 0 {
						// Partial sequence: keep it and wait
						// for the remainder.
						break
					}
					buf = buf[consumed:]
					if key.Type != KeyUnknown {
						keys <- key
					}
				}
			}
			if err != nil {
				return
			}
		}
	}()
	return keys
}
