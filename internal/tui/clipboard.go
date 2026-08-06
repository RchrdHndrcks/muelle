package tui

import "encoding/base64"

// CopySequence returns the OSC 52 sequence that asks the terminal to place
// text on the clipboard.
//
// The terminal doing the copying is the whole point: the sequence travels
// down an SSH connection like any other output, so the text lands on the
// clipboard of the machine the user is actually sitting at — the one they
// will paste from. A `docker cp`-style round trip through the remote host's
// clipboard would put it on the wrong machine entirely. Terminals without
// OSC 52 support discard the sequence unrendered, so emitting it costs
// nothing where it does not work.
//
// The payload is base64 because the clipboard text is arbitrary: unencoded,
// a container ID is safe but a name containing BEL or ESC would terminate
// the sequence early and leak the rest to the screen.
func CopySequence(text string) string {
	return "\x1b]52;c;" + base64.StdEncoding.EncodeToString([]byte(text)) + "\a"
}
