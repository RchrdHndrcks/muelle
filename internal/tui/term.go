//go:build darwin || linux

// Package tui provides the terminal primitives muelle needs: raw mode, window
// size, ANSI rendering and key decoding, all on the standard library.
//
// The raw-mode handling is two ioctls, which is all golang.org/x/term does for
// this job, so they are issued directly rather than taking on a dependency.
package tui

import (
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

// State holds the terminal settings captured before switching to raw mode, so
// they can be restored on exit.
type State struct {
	termios syscall.Termios
}

// IsTerminal reports whether fd refers to a terminal. It is how muelle
// detects being piped or run without a controlling TTY.
func IsTerminal(fd uintptr) bool {
	var termios syscall.Termios
	return ioctl(fd, ioctlReadTermios, unsafe.Pointer(&termios)) == nil
}

// MakeRaw puts the terminal into raw mode and returns the previous state.
//
// Raw mode means: no line buffering (keys arrive as typed), no echo (we draw
// everything ourselves), and no signal or flow-control interpretation, so
// Ctrl-C and Ctrl-S reach the application as ordinary keys instead of being
// swallowed by the line discipline.
func MakeRaw(fd uintptr) (*State, error) {
	var termios syscall.Termios
	if err := ioctl(fd, ioctlReadTermios, unsafe.Pointer(&termios)); err != nil {
		return nil, fmt.Errorf("tui: read terminal settings: %w", err)
	}
	previous := State{termios: termios}

	// This is the canonical cfmakeraw(3) flag set.
	termios.Iflag &^= syscall.IGNBRK | syscall.BRKINT | syscall.PARMRK |
		syscall.ISTRIP | syscall.INLCR | syscall.IGNCR | syscall.ICRNL | syscall.IXON
	termios.Oflag &^= syscall.OPOST
	termios.Lflag &^= syscall.ECHO | syscall.ECHONL | syscall.ICANON |
		syscall.ISIG | syscall.IEXTEN
	termios.Cflag &^= syscall.CSIZE | syscall.PARENB
	termios.Cflag |= syscall.CS8
	// Return from read(2) after a tenth of a second even with no input,
	// rather than blocking indefinitely.
	//
	// A blocking read cannot be interrupted, which matters because the
	// terminal has to be handed over whole to a child process — an exec
	// session, a compose command. A reader parked in read(2) keeps
	// competing for input the child now owns, stealing its keystrokes and
	// deadlocking anything else that tries to read. Timing out lets the
	// reader notice it has been asked to stand down. The cost is a wakeup
	// ten times a second doing nothing.
	termios.Cc[syscall.VMIN] = 0
	termios.Cc[syscall.VTIME] = 1

	if err := ioctl(fd, ioctlWriteTermios, unsafe.Pointer(&termios)); err != nil {
		return nil, fmt.Errorf("tui: apply raw mode: %w", err)
	}
	return &previous, nil
}

// Restore returns the terminal to a previously captured state. Callers must
// defer this: a process that exits in raw mode leaves the user's shell without
// echo or line editing.
func Restore(fd uintptr, state *State) error {
	if state == nil {
		return nil
	}
	termios := state.termios
	if err := ioctl(fd, ioctlWriteTermios, unsafe.Pointer(&termios)); err != nil {
		return fmt.Errorf("tui: restore terminal settings: %w", err)
	}
	return nil
}

// winsize mirrors struct winsize from <sys/ioctl.h>.
type winsize struct {
	rows, cols, xpixel, ypixel uint16
}

// Size reports the terminal's dimensions in character cells.
func Size(fd uintptr) (width, height int, err error) {
	var ws winsize
	if err := ioctl(fd, syscall.TIOCGWINSZ, unsafe.Pointer(&ws)); err != nil {
		return 0, 0, fmt.Errorf("tui: read window size: %w", err)
	}
	return int(ws.cols), int(ws.rows), nil
}

// SizeOrDefault reports the terminal size, falling back to a conventional
// 80x24 when the size cannot be read (output redirected, no controlling
// terminal). Rendering into a plausible size beats failing.
func SizeOrDefault(fd uintptr) (width, height int) {
	w, h, err := Size(fd)
	if err != nil || w == 0 || h == 0 {
		return 80, 24
	}
	return w, h
}

// ioctl issues a terminal ioctl against fd.
func ioctl(fd uintptr, request uintptr, argp unsafe.Pointer) error {
	_, _, errno := syscall.Syscall6(syscall.SYS_IOCTL, fd, request, uintptr(argp), 0, 0, 0)
	if errno != 0 {
		return errno
	}
	return nil
}

// Stdin and Stdout file descriptors, named for readability at call sites.
var (
	StdinFd  = os.Stdin.Fd()
	StdoutFd = os.Stdout.Fd()
)
