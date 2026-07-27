package docker

import "testing"

func TestExitCodeParsedFromStatusText(t *testing.T) {
	cases := map[string]struct {
		code  int
		known bool
	}{
		"Exited (0) 2 days ago":      {0, true},
		"Exited (137) 5 minutes ago": {137, true},
		"Exited (255) 2 months ago":  {255, true},
		"Exited (128) 2 weeks ago":   {128, true},
		"Up 8 weeks":                 {0, false},
		"Created":                    {0, false},
		"":                           {0, false},
	}

	for status, want := range cases {
		code, known := Container{Status: status}.ExitCode()
		if code != want.code || known != want.known {
			t.Errorf("Status %q gave (%d, %v), want (%d, %v)",
				status, code, known, want.code, want.known)
		}
	}
}

// A bare int cannot distinguish "exited cleanly" from "no code available";
// both would be zero.
func TestExitCodeDistinguishesZeroFromUnknown(t *testing.T) {
	clean, known := Container{Status: "Exited (0) 1 hour ago"}.ExitCode()
	if !known || clean != 0 {
		t.Errorf("got (%d, %v), want a known zero", clean, known)
	}

	_, known = Container{Status: "Up 2 hours"}.ExitCode()
	if known {
		t.Error("a running container has no exit code")
	}
}

// Health markers live in the same parentheses and must not be read as codes.
func TestExitCodeIgnoresHealthParentheses(t *testing.T) {
	for _, status := range []string{
		"Up 2 hours (healthy)",
		"Up 5 minutes (unhealthy)",
		"Up 3 seconds (health: starting)",
	} {
		if _, known := (Container{Status: status}).ExitCode(); known {
			t.Errorf("status %q should not yield an exit code", status)
		}
	}
}

func TestExitedCleanlyRequiresBothZeroAndStopped(t *testing.T) {
	if !(Container{Status: "Exited (0) 1 hour ago", State: "exited"}).ExitedCleanly() {
		t.Error("a container that exited 0 stopped cleanly")
	}
	if (Container{Status: "Exited (1) 1 hour ago", State: "exited"}).ExitedCleanly() {
		t.Error("a non-zero exit is not clean")
	}
	if (Container{Status: "Up 2 hours", State: "running"}).ExitedCleanly() {
		t.Error("a running container has not exited at all")
	}
}

// 137 and 143 are both "stopped" to the daemon but mean very different things.
func TestExitReasonNamesTheCommonSignals(t *testing.T) {
	cases := map[int]string{
		0:   "success",
		137: "SIGKILL (often out of memory)",
		143: "SIGTERM (stopped)",
		139: "SIGSEGV (segmentation fault)",
		127: "command not found",
		255: "abnormal termination",
	}

	for code, want := range cases {
		if got := ExitReason(code); got != want {
			t.Errorf("ExitReason(%d) = %q, want %q", code, got, want)
		}
	}
}

func TestExitReasonDecodesOtherSignals(t *testing.T) {
	// 130 is Ctrl-C: 128 + SIGINT(2).
	if got := ExitReason(130); got != "killed by signal 2" {
		t.Errorf("got %q, want the signal number decoded", got)
	}
}

// An unrecognised code should produce nothing rather than a guess.
func TestExitReasonEmptyForUnknownCodes(t *testing.T) {
	for _, code := range []int{7, 42, 99} {
		if got := ExitReason(code); got != "" {
			t.Errorf("ExitReason(%d) = %q, want no explanation invented", code, got)
		}
	}
}
