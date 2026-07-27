package tui

import "syscall"

// macOS spells the termios ioctls TIOCGETA/TIOCSETA, where Linux uses the
// TCGETS/TCSETS names. The rest of the raw-mode code is identical, so only
// these two constants vary by platform.
const (
	ioctlReadTermios  = syscall.TIOCGETA
	ioctlWriteTermios = syscall.TIOCSETA
)
