package tui

import (
	"bufio"
	"errors"
	"os"
	"testing"
	"time"
)

// TestHandoverGivesTheTerminalToAnotherReader reproduces the failure this
// handover exists to prevent: a key reader parked in read consumed the newline
// meant for the "press enter to continue" that follows a child process, and
// muelle hung until the terminal was killed.
//
// A pipe stands in for the terminal — the same two-readers-on-one-descriptor
// race caused it — with a read deadline so reads time out the way they do on a
// terminal in raw mode.
func TestHandoverGivesTheTerminalToAnotherReader(t *testing.T) {
	pipeRead, pipeWrite, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	defer pipeWrite.Close()

	reader := NewKeyReader(func(p []byte) (int, error) {
		// Mirrors VMIN=0/VTIME=1: return empty-handed rather than block.
		if err := pipeRead.SetReadDeadline(time.Now().Add(20 * time.Millisecond)); err != nil {
			return 0, err
		}
		n, err := pipeRead.Read(p)
		if errors.Is(err, os.ErrDeadlineExceeded) {
			return n, nil
		}
		return n, err
	})
	defer reader.Stop()
	time.Sleep(60 * time.Millisecond) // let it start reading

	// What Suspend does before handing the terminal to a child.
	reader.Pause()

	received := make(chan string, 1)
	go func() {
		pipeRead.SetReadDeadline(time.Time{}) // the child reads without a deadline
		line, _ := bufio.NewReader(pipeRead).ReadString('\n')
		received <- line
	}()
	time.Sleep(60 * time.Millisecond)

	pipeWrite.WriteString("user presses enter\n")

	select {
	case line := <-received:
		if line != "user presses enter\n" {
			t.Errorf("got %q, want the whole line", line)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the paused reader consumed the input meant for the child; this is the hang")
	}
}

// Pause must be bounded even when the reader cannot be stood down, or it
// becomes the very hang it was written to remove.
func TestPauseGivesUpRatherThanHanging(t *testing.T) {
	blocked := make(chan struct{})
	reader := NewKeyReader(func(p []byte) (int, error) {
		<-blocked // never returns, as a read on a terminal left in blocking mode would
		return 0, nil
	})
	defer func() { close(blocked); reader.Stop() }()

	// Wait until the reader is genuinely inside the read, or it parks before
	// reaching it and the grace period is never exercised.
	time.Sleep(80 * time.Millisecond)

	done := make(chan time.Duration, 1)
	go func() {
		start := time.Now()
		reader.Pause()
		done <- time.Since(start)
	}()

	select {
	case took := <-done:
		if took < pauseGrace {
			t.Errorf("Pause returned in %v without waiting; the grace period was not exercised", took)
		}
		if took > 2*time.Second {
			t.Errorf("Pause took %v, want it bounded by the grace period", took)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Pause hung on a reader that could not stand down")
	}
}
