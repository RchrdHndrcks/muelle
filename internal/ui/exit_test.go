package ui

import (
	"strings"
	"testing"

	"github.com/RchrdHndrcks/muelle/internal/docker"
	"github.com/RchrdHndrcks/muelle/internal/tui"
)

func TestStateCellShowsExitCode(t *testing.T) {
	app := newTestApp(t)

	cell := app.stateCell(docker.Container{State: "exited", Status: "Exited (137) 5 minutes ago"})

	if !strings.Contains(cell, "137") {
		t.Errorf("got %q, want the exit code shown", cell)
	}
}

// "exited" alone hides the difference between a clean shutdown and an
// out-of-memory kill.
func TestStateCellDistinguishesCleanFromFailedExit(t *testing.T) {
	app := newTestApp(t)

	clean := app.stateCell(docker.Container{State: "exited", Status: "Exited (0) 1 hour ago"})
	failed := app.stateCell(docker.Container{State: "exited", Status: "Exited (137) 1 hour ago"})

	if clean == failed {
		t.Errorf("both rendered as %q; a clean exit must look different from a kill", clean)
	}
	if !strings.Contains(clean, "0") {
		t.Errorf("got %q, want the zero shown rather than hidden", clean)
	}
}

func TestStateCellLeavesRunningContainersAlone(t *testing.T) {
	app := newTestApp(t)

	cell := app.stateCell(docker.Container{State: "running", Status: "Up 2 hours"})

	if cell != "up" {
		t.Errorf("got %q, want the plain running label", cell)
	}
}

// A running container's health parentheses must not be rendered as an exit.
func TestStateCellIgnoresHealthOnRunningContainer(t *testing.T) {
	app := newTestApp(t)

	cell := app.stateCell(docker.Container{State: "running", Status: "Up 2 hours (healthy)"})

	if strings.Contains(cell, "exit") {
		t.Errorf("got %q, want no exit code for a running container", cell)
	}
}

// The right side carries information found nowhere else; the hints are a
// reminder available from "?".
func TestStatusBarKeepsStateWhenHintsDoNotFit(t *testing.T) {
	app := loadedApp(t)
	app.filter = "shop"

	bar := app.renderStatusBar(60)

	if !strings.Contains(bar, "filter:shop") {
		t.Errorf("got %q, want the state kept when space is tight", bar)
	}
	if tui.VisibleWidth(bar) > 60 {
		t.Errorf("got %d cells, want at most 60", tui.VisibleWidth(bar))
	}
}

func TestStatusBarExplainsSelectedContainersExit(t *testing.T) {
	app := newTestApp(t)
	app.containers = []docker.Container{
		{ID: "a", Names: []string{"/crashed"}, State: "exited", Status: "Exited (137) 1 minute ago"},
	}

	bar := app.renderStatusBar(160)

	if !strings.Contains(bar, "out of memory") {
		t.Errorf("got %q, want the exit code explained", bar)
	}
}

func TestStatusBarSaysNothingForCleanExit(t *testing.T) {
	app := newTestApp(t)
	app.containers = []docker.Container{
		{ID: "a", Names: []string{"/done"}, State: "exited", Status: "Exited (0) 1 minute ago"},
	}

	bar := app.renderStatusBar(160)

	if strings.Contains(bar, "success") {
		t.Errorf("got %q, want no explanation for an ordinary clean exit", bar)
	}
}

// The exit code and the restart count answer different questions, and a
// container that crash-looped and finally stayed down is exactly the case
// where both matter. Neither feature should clobber the other.
func TestStateCellCombinesExitCodeWithRestartCount(t *testing.T) {
	app := newTestApp(t)
	app.restartCounts = map[string]int{"looper": 40}

	cell := app.stateCell(docker.Container{
		ID: "looper", State: "exited", Status: "Exited (137) 1 minute ago",
	})

	if !strings.Contains(cell, "137") {
		t.Errorf("got %q, want the exit code kept", cell)
	}
	if !strings.Contains(cell, "×40") {
		t.Errorf("got %q, want the restart count kept", cell)
	}
}

// A stopped container reports no healthcheck, so the exit code takes the slot
// the marker would have used rather than both competing for it.
func TestStateCellPrefersExitCodeOverHealthMarkerWhenStopped(t *testing.T) {
	app := newTestApp(t)

	cell := app.stateCell(docker.Container{State: "exited", Status: "Exited (1) 1 minute ago"})

	for _, glyph := range []string{"✓", "✗", "…"} {
		if strings.Contains(cell, glyph) {
			t.Errorf("got %q, want no health marker on a stopped container", cell)
		}
	}
}

// A running container keeps its health marker; the exit-code path must not
// swallow it.
func TestStateCellKeepsHealthMarkerWhileRunning(t *testing.T) {
	app := newTestApp(t)

	cell := app.stateCell(docker.Container{State: "running", Status: "Up 2 hours (unhealthy)"})

	if !strings.Contains(cell, "✗") {
		t.Errorf("got %q, want the health marker preserved", cell)
	}
	if strings.Contains(cell, "exit") {
		t.Errorf("got %q, want no exit code on a running container", cell)
	}
}
