package ui

import (
	"strings"
	"testing"

	"github.com/RchrdHndrcks/muelle/internal/docker"
)

// sparkAnyRunes is every character the sparkline can produce, for asserting
// its presence or absence in a rendered row.
const sparkAnyRunes = "▁▂▃▄▅▆▇█"

func containsSparkRune(s string) bool {
	return strings.ContainsAny(s, sparkAnyRunes)
}

func TestHistoryKeepsNewestSamplesOldestFirst(t *testing.T) {
	h := &history{}
	for i := 0; i < cpuHistoryLen+10; i++ {
		h.push(float64(i))
	}

	got := h.last(cpuHistoryLen)
	if len(got) != cpuHistoryLen {
		t.Fatalf("got %d samples, want the ring full at %d", len(got), cpuHistoryLen)
	}
	// The first ten pushes must have been evicted, and the survivors must
	// read oldest to newest — the order a sparkline draws in.
	if got[0] != 10 || got[len(got)-1] != float64(cpuHistoryLen+9) {
		t.Errorf("got %v..%v, want 10..%d", got[0], got[len(got)-1], cpuHistoryLen+9)
	}
	for i := 1; i < len(got); i++ {
		if got[i] != got[i-1]+1 {
			t.Fatalf("samples out of order at %d: %v", i, got)
		}
	}
}

func TestHistoryReturnsOnlyWhatItHolds(t *testing.T) {
	h := &history{}
	h.push(1)
	h.push(2)
	h.push(3)

	got := h.last(sparkCells)
	if len(got) != 3 {
		t.Fatalf("got %d samples, want the 3 that were pushed", len(got))
	}
	if got[0] != 1 || got[2] != 3 {
		t.Errorf("got %v, want [1 2 3]", got)
	}
}

// A container the stats have not reached yet has no history at all; asking a
// nil ring must answer with nothing rather than panic, since the view indexes
// the history map without checking.
func TestHistoryNilHasNoSamples(t *testing.T) {
	var h *history
	if got := h.last(sparkCells); got != nil {
		t.Errorf("got %v from a nil history, want nothing", got)
	}
}

// The scale follows the row: the window's highest reading is the full bar,
// so a quiet container's small movements are as visible as a busy one's.
func TestSparklineScalesToTheRowsOwnRange(t *testing.T) {
	if got := Sparkline([]float64{0, 50, 100}); got != "▁▅█" {
		t.Errorf("got %q, want %q", got, "▁▅█")
	}
	// The reading that says a row is busy is the same on an idle host:
	// the bars must differ, not render a row of dashed lowest bars.
	if got := Sparkline([]float64{0.4, 0.5, 0.6}); got != "▆▇█" {
		t.Errorf("got %q, want %q", got, "▆▇█")
	}
}

// A window of nothing but zeroes must render as the lowest bar rather than
// dividing by a zero axis.
func TestSparklineAllZeroesStayAtTheBottom(t *testing.T) {
	if got := Sparkline([]float64{0, 0, 0}); got != "▁▁▁" {
		t.Errorf("got %q, want %q", got, "▁▁▁")
	}
}

// A container on a multi-core host can report several hundred percent, and a
// glitched delta could go negative. Both must clamp to the scale's ends
// rather than index outside the rune table.
func TestSparklineClampsOutOfRangeReadings(t *testing.T) {
	if got := Sparkline([]float64{450, -3}); got != "█▁" {
		t.Errorf("got %q, want %q", got, "█▁")
	}
}

func TestSparklineEmptyHistoryRendersNothing(t *testing.T) {
	if got := Sparkline(nil); got != "" {
		t.Errorf("got %q, want an empty string", got)
	}
}

func TestSparklineSingleSampleIsOneCell(t *testing.T) {
	got := Sparkline([]float64{100})
	if got != "█" {
		t.Errorf("got %q, want a single full bar", got)
	}
}

// feedStats delivers a whole round of samples the way the one-shot path dump
// mode uses does. The live app receives statSampled events instead; see
// TestStreamedSamplesFeedTheHistory for that path.
func feedStats(app *App, stats map[string]docker.Stat) {
	statsLoaded{stats: stats}.apply(app)
}

func TestStatsSamplesFeedTheHistory(t *testing.T) {
	app := newTestApp(t)

	feedStats(app, map[string]docker.Stat{"x": {CPUPercent: 10}})
	feedStats(app, map[string]docker.Stat{"x": {CPUPercent: 90}})

	got := app.cpuHistory["x"].last(sparkCells)
	if len(got) != 2 || got[0] != 10 || got[1] != 90 {
		t.Errorf("got %v, want the two samples in arrival order", got)
	}
}

// The running app is fed by the stats streams, one container at a time, and
// that is the path the sparkline is actually drawn from — a history filled
// only by the dump-mode round would stay empty in the app.
func TestStreamedSamplesFeedTheHistory(t *testing.T) {
	app := newTestApp(t)
	app.containers = []docker.Container{
		{ID: "x", Names: []string{"/x"}, State: "running", Status: "Up 1 minute"},
	}

	statSampled{id: "x", stat: docker.Stat{CPUPercent: 10}}.apply(app)
	statSampled{id: "x", stat: docker.Stat{CPUPercent: 90}}.apply(app)

	got := app.cpuHistory["x"].last(sparkCells)
	if len(got) != 2 || got[0] != 10 || got[1] != 90 {
		t.Errorf("got %v, want the two streamed samples in arrival order", got)
	}
}

// A container that disappears must take its buffer with it, or a host cycling
// through short-lived containers grows the history map forever.
func TestHistoryEvictedWithTheContainer(t *testing.T) {
	app := newTestApp(t)
	feedStats(app, map[string]docker.Stat{
		"kept": {CPUPercent: 5},
		"gone": {CPUPercent: 5},
	})

	containersLoaded{containers: []docker.Container{
		{ID: "kept", Names: []string{"/kept"}, State: "running", Status: "Up 1 minute"},
	}}.apply(app)

	if app.cpuHistory["kept"] == nil {
		t.Errorf("the listed container's history was evicted")
	}
	if _, held := app.cpuHistory["gone"]; held {
		t.Errorf("the vanished container's history was kept")
	}
}

// sparklineTestApp builds an app with stats on, one running container, and a
// few rounds of samples in its history.
func sparklineTestApp(t *testing.T) *App {
	t.Helper()
	app := newTestApp(t)
	app.config.Stats = true
	app.containers = []docker.Container{{
		ID: "x", Names: []string{"/api"}, State: "running", Status: "Up 2 minutes",
	}}
	for _, percent := range []float64{5, 60, 100} {
		feedStats(app, map[string]docker.Stat{"x": {CPUPercent: percent}})
	}
	return app
}

// The default test screen has colour off, so these renders also prove the
// sparkline survives NO_COLOR: the runes carry the reading on their own.
func TestWideTerminalShowsCPUSparkline(t *testing.T) {
	app := sparklineTestApp(t)

	rendered := strings.Join(app.renderContainers(160, 10), "\n")

	if !containsSparkRune(rendered) {
		t.Fatalf("got %q, want the CPU history drawn at 160 columns", rendered)
	}
	// The current reading must survive next to the history, not be replaced
	// by it.
	if !strings.Contains(rendered, "100.0%") {
		t.Errorf("got %q, want the current reading still shown", rendered)
	}
}

// On a narrow terminal the CPU cell must fall back to the number alone —
// exactly the rendering the column had before the sparkline existed — rather
// than squeeze or displace another column.
func TestNarrowTerminalFallsBackToNumericCPU(t *testing.T) {
	app := sparklineTestApp(t)

	rendered := strings.Join(app.renderContainers(120, 10), "\n")

	if containsSparkRune(rendered) {
		t.Fatalf("got %q, want no sparkline at 120 columns", rendered)
	}
	if !strings.Contains(rendered, "100.0%") {
		t.Errorf("got %q, want the numeric reading kept", rendered)
	}
}

// The sparkline appears by widening the CPU column, and only when every other
// column already fits at its preferred width — it must never be the reason a
// column is dropped.
func TestSparklineNeverDisplacesAColumn(t *testing.T) {
	app := sparklineTestApp(t)

	for _, width := range []int{80, 100, 120, 131, 160, 200} {
		columns := app.containerColumns(width)
		widths := LayoutColumns(columns, width)
		if app.sparklineShown(width) && len(widths) < len(columns) {
			t.Errorf("at %d columns the sparkline dropped %d trailing columns",
				width, len(columns)-len(widths))
		}
	}
}

// A container with no samples yet renders its usual blank reading, sparkline
// column or not.
func TestSparklineColumnHandlesMissingHistory(t *testing.T) {
	app := sparklineTestApp(t)
	app.containers = append(app.containers, docker.Container{
		ID: "fresh", Names: []string{"/fresh"}, State: "running", Status: "Up 1 second",
	})

	rows := app.renderContainers(160, 10)

	var freshRow string
	for _, row := range rows {
		if strings.Contains(row, "fresh") {
			freshRow = row
		}
	}
	if freshRow == "" {
		t.Fatalf("the fresh container was not rendered")
	}
	if containsSparkRune(freshRow) {
		t.Errorf("got %q, want no sparkline for a container with no samples", freshRow)
	}
	if !strings.Contains(freshRow, "-") {
		t.Errorf("got %q, want the usual blank reading", freshRow)
	}
}
