package docker

import (
	"strconv"
	"strings"
)

// ExitCode returns the code a stopped container exited with.
//
// The list endpoint reports it only inside the status text, as in
// "Exited (137) 5 minutes ago". Reading it from there avoids inspecting every
// stopped container just to render a column.
//
// The second return distinguishes "exited cleanly with 0" from "no exit code
// available", which a bare int cannot.
func (c Container) ExitCode() (int, bool) {
	open := strings.Index(c.Status, "(")
	if open < 0 {
		return 0, false
	}
	close := strings.Index(c.Status[open:], ")")
	if close < 0 {
		return 0, false
	}
	code, err := strconv.Atoi(c.Status[open+1 : open+close])
	if err != nil {
		// "(healthy)" and friends live in the same parentheses; only a
		// number is an exit code.
		return 0, false
	}
	return code, true
}

// ExitedCleanly reports whether the container stopped of its own accord with
// status 0. A container still running reports false.
func (c Container) ExitedCleanly() bool {
	code, ok := c.ExitCode()
	return ok && code == 0 && !c.Running()
}

// ExitReason explains an exit code in the terms that matter when something has
// just died.
//
// Codes above 128 are the conventional "killed by signal N" encoding, and the
// two that actually turn up — 137 and 143 — are worth naming: 137 usually
// means the kernel's OOM killer, which is a very different problem from a
// clean shutdown, and they are indistinguishable as bare numbers.
func ExitReason(code int) string {
	switch code {
	case 0:
		return "success"
	case 1:
		return "general error"
	case 125:
		return "docker run failed"
	case 126:
		return "command not executable"
	case 127:
		return "command not found"
	case 128:
		return "invalid exit argument"
	case 137:
		return "SIGKILL (often out of memory)"
	case 139:
		return "SIGSEGV (segmentation fault)"
	case 143:
		return "SIGTERM (stopped)"
	case 255:
		// exit(-1) truncates to 255, and the daemon also reports it for
		// abnormal termination. Both are "something went wrong" without
		// being more specific, so the description does not pretend to be.
		return "abnormal termination"
	}
	if code > 128 && code < 165 {
		return "killed by signal " + strconv.Itoa(code-128)
	}
	return ""
}
