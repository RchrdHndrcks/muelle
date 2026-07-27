package ui

import (
	"strings"
	"testing"

	"github.com/RchrdHndrcks/muelle/internal/docker"
	"github.com/RchrdHndrcks/muelle/internal/tui"
)

// visibleWidthOf measures a rendered cell, ignoring styling.
func visibleWidthOf(s string) int { return tui.VisibleWidth(s) }

func TestStateCellMarksHealth(t *testing.T) {
	app := newTestApp(t)

	cases := map[string]string{
		"Up 2 hours (healthy)":            "✓",
		"Up 5 minutes (unhealthy)":        "✗",
		"Up 3 seconds (health: starting)": "…",
	}

	for status, glyph := range cases {
		cell := app.stateCell(docker.Container{State: "running", Status: status})
		if !strings.Contains(cell, glyph) {
			t.Errorf("status %q rendered %q, want the %q marker", status, cell, glyph)
		}
	}
}

// A container without a healthcheck must not gain a marker implying one
// passed.
func TestStateCellOmitsMarkerWithoutHealthcheck(t *testing.T) {
	app := newTestApp(t)

	cell := app.stateCell(docker.Container{State: "running", Status: "Up 8 weeks"})

	for _, glyph := range []string{"✓", "✗", "…"} {
		if strings.Contains(cell, glyph) {
			t.Errorf("got %q, want no health marker when none is reported", cell)
		}
	}
}

// The markers must differ by character, not only colour, or the distinction
// vanishes under NO_COLOR.
func TestHealthMarkersAreDistinctWithoutColour(t *testing.T) {
	app := newTestApp(t) // colour disabled

	healthy := app.stateCell(docker.Container{State: "running", Status: "Up 1h (healthy)"})
	unhealthy := app.stateCell(docker.Container{State: "running", Status: "Up 1h (unhealthy)"})

	if healthy == unhealthy {
		t.Errorf("both rendered as %q; the states must be distinguishable without colour", healthy)
	}
}

func TestStateCellShowsRestartCount(t *testing.T) {
	app := newTestApp(t)
	app.restartCounts = map[string]int{"flapper": 12}

	cell := app.stateCell(docker.Container{ID: "flapper", State: "restarting"})

	if !strings.Contains(cell, "×12") {
		t.Errorf("got %q, want the restart count shown", cell)
	}
}

func TestStateCellOmitsZeroRestartCount(t *testing.T) {
	app := newTestApp(t)
	app.restartCounts = map[string]int{"steady": 0}

	cell := app.stateCell(docker.Container{ID: "steady", State: "running"})

	if strings.Contains(cell, "×") {
		t.Errorf("got %q, want no restart marker at zero", cell)
	}
}

// The state column must still fit its width once markers are appended.
// The cell now composes an exit code, a health marker and a restart count, so
// the column has to fit whichever combination is widest.
func TestStateCellFitsItsColumn(t *testing.T) {
	const stateColumnWidth = 13

	app := newTestApp(t)
	app.restartCounts = map[string]int{"x": 999}

	cases := map[string]docker.Container{
		"crash-looping and unhealthy": {
			ID: "x", State: "restarting", Status: "Restarting (1) 2 seconds ago (unhealthy)",
		},
		"stopped after many restarts": {
			ID: "x", State: "exited", Status: "Exited (255) 2 months ago",
		},
		"long state with a marker": {
			ID: "x", State: "removing", Status: "Removal In Progress",
		},
		"plain running": {
			ID: "other", State: "running", Status: "Up 8 weeks",
		},
	}

	for name, container := range cases {
		cell := app.stateCell(container)
		if width := visibleWidthOf(cell); width > stateColumnWidth {
			t.Errorf("%s: got %q at %d cells, want at most %d",
				name, cell, width, stateColumnWidth)
		}
	}

	// The cap is what keeps the worst case bounded; without it the count
	// would be ellipsized away by the column it overflowed.
	capped := app.stateCell(docker.Container{ID: "x", State: "restarting"})
	if !strings.Contains(capped, "×99+") {
		t.Errorf("got %q, want the count capped rather than truncated away", capped)
	}
}

// A stale install looks exactly like a bug that was never fixed. Naming the
// running build is what tells the two apart.
func TestHeaderNamesTheRunningBuild(t *testing.T) {
	app := newTestApp(t)
	app.SetBuild("830ab25")

	header := app.renderHeader(160)

	if !strings.Contains(header, "muelle 830ab25") {
		t.Errorf("got %q, want the running build named", header)
	}
}

func TestHeaderOmitsBuildWhenUnknown(t *testing.T) {
	app := newTestApp(t)

	header := app.renderHeader(160)

	if strings.Contains(header, "  muelle ") {
		t.Errorf("got %q, want no build suffix when none was set", header)
	}
}
