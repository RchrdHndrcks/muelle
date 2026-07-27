package tui

import "syscall"

// See term_darwin.go: only the ioctl request numbers differ between
// platforms.
const (
	ioctlReadTermios  = syscall.TCGETS
	ioctlWriteTermios = syscall.TCSETS
)
