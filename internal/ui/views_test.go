package ui

import (
	"strings"
	"testing"
	"time"

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

// Age and uptime answer different questions and must not be conflated: a
// container restarted a minute ago was still created three days ago. Reporting
// only the creation time is what makes a restart look like it did nothing.
func TestContainerRowShowsUptimeAlongsideAge(t *testing.T) {
	app := newTestApp(t)
	app.containers = []docker.Container{{
		ID:      "x",
		Names:   []string{"/api"},
		State:   "running",
		Status:  "Up 2 minutes",
		Created: time.Now().Add(-72 * time.Hour).Unix(),
	}}

	row := strings.Join(app.renderContainers(120, 10), "\n")

	if !strings.Contains(row, "3d") {
		t.Errorf("got %q, want the creation age 3d", row)
	}
	if !strings.Contains(row, "2m") {
		t.Errorf("got %q, want the uptime 2m", row)
	}
	if !strings.Contains(row, "UPTIME") {
		t.Errorf("got %q, want an uptime column header", row)
	}
}

// A container that is not running has no uptime. Showing the time since it
// died there would read as though it were still up.
func TestUptimeCellEmptyForStoppedContainers(t *testing.T) {
	app := newTestApp(t)
	app.containers = []docker.Container{{
		ID:      "x",
		Names:   []string{"/api"},
		State:   "exited",
		Status:  "Exited (0) 5 minutes ago",
		Created: time.Now().Add(-72 * time.Hour).Unix(),
	}}
	app.SetShowAll(true)

	row := strings.Join(app.renderContainers(120, 10), "\n")

	if strings.Contains(row, "5m") {
		t.Errorf("got %q, want no uptime for a stopped container", row)
	}
	if !strings.Contains(row, "3d") {
		t.Errorf("got %q, want the creation age still shown", row)
	}
}

// "just now" is eight cells and was being truncated to "just n…" by a
// seven-cell column. It is the single most common value in the uptime column,
// since a container that was restarted is exactly what someone is looking at.
func TestAgeAndUptimeColumnsFitTheirWidestValue(t *testing.T) {
	app := newTestApp(t)

	widest := 0
	for _, d := range []time.Duration{
		0, 30 * time.Second, 59 * time.Minute,
		23 * time.Hour, 364 * 24 * time.Hour, 900 * 24 * time.Hour,
	} {
		if width := len(FormatDuration(d)); width > widest {
			widest = width
		}
	}

	for _, column := range app.containerColumns() {
		if column.Title != "age" && column.Title != "uptime" {
			continue
		}
		if column.Width < widest {
			t.Errorf("column %q is %d cells, want at least %d for %q",
				column.Title, column.Width, widest, FormatDuration(0))
		}
	}
}
