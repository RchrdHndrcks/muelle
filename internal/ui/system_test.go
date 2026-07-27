package ui

import (
	"strings"
	"testing"

	"github.com/RchrdHndrcks/muelle/internal/docker"
	"github.com/RchrdHndrcks/muelle/internal/tui"
)

// withMetrics returns a loaded app carrying plausible host metrics.
func withMetrics(t *testing.T) *App {
	t.Helper()
	app := newTestApp(t)
	app.showSystem = true
	app.metrics = HostMetrics{
		Info: docker.Info{
			Name:              "vm-host",
			ServerVersion:     "27.4.0",
			OperatingSystem:   "Ubuntu 24.04.1 LTS",
			OSType:            "linux",
			Architecture:      "aarch64",
			NCPU:              4,
			MemTotal:          8 << 30,
			ContainersRunning: 9,
			ContainersStopped: 3,
			Images:            40,
		},
		Disk: docker.DiskUsage{
			ImagesSize:        10 << 30,
			ImagesReclaimable: 4 << 30,
			ImagesUnused:      7,
			VolumesSize:       2 << 30,
			VolumesCount:      12,
			VolumeSizesKnown:  true,
		},
		CPUPercent: 120.0,
		MemBytes:   4 << 30,
		DiskKnown:  true,
	}
	return app
}

func TestSystemPanelReportsHostFacts(t *testing.T) {
	app := withMetrics(t)

	panel := strings.Join(app.renderSystemPanel(120), "\n")

	for _, want := range []string{"vm-host", "Ubuntu 24.04.1 LTS", "linux/aarch64", "docker 27.4.0"} {
		if !strings.Contains(panel, want) {
			t.Errorf("panel is missing %q:\n%s", want, panel)
		}
	}
}

// A container can exceed one core, so the aggregate is measured against
// NCPU * 100%, not 100%.
func TestSystemPanelScalesCPUByCoreCount(t *testing.T) {
	app := withMetrics(t)

	panel := strings.Join(app.renderSystemPanel(120), "\n")

	if !strings.Contains(panel, "120.0% of 4 cores") {
		t.Errorf("panel should state the aggregate against the core count:\n%s", panel)
	}
}

func TestSystemPanelShowsMemoryAgainstCapacity(t *testing.T) {
	app := withMetrics(t)

	panel := strings.Join(app.renderSystemPanel(120), "\n")

	if !strings.Contains(panel, "4.0GiB / 8.0GiB") {
		t.Errorf("panel should show usage against total:\n%s", panel)
	}
}

// Reclaimable space is the actionable number on the disk line — but only if it
// says where the space is. The count of unused images it used to print
// described the wrong thing entirely once a build cache was involved.
func TestSystemPanelHighlightsReclaimableSpace(t *testing.T) {
	app := withMetrics(t)

	panel := strings.Join(app.renderSystemPanel(160), "\n")

	if !strings.Contains(panel, "4.0GiB reclaimable") {
		t.Errorf("panel should surface reclaimable space:\n%s", panel)
	}
	if !strings.Contains(panel, "unused images") {
		t.Errorf("panel should attribute the space to its source:\n%s", panel)
	}
}

func TestSystemPanelOmitsVolumesWhenNotMeasured(t *testing.T) {
	app := withMetrics(t)
	app.metrics.Disk.VolumeSizesKnown = false

	panel := strings.Join(app.renderSystemPanel(120), "\n")

	if strings.Contains(panel, "volumes 0B") {
		t.Errorf("an unmeasured volume total should be omitted, not shown as zero:\n%s", panel)
	}
}

func TestSystemPanelReportsWhileDiskIsStillMeasuring(t *testing.T) {
	app := withMetrics(t)
	app.metrics.DiskKnown = false

	panel := strings.Join(app.renderSystemPanel(120), "\n")

	if !strings.Contains(panel, "measuring") {
		t.Errorf("the slow disk measurement should be acknowledged:\n%s", panel)
	}
}

func TestSystemPanelHasFixedHeight(t *testing.T) {
	app := withMetrics(t)

	if got := len(app.renderSystemPanel(120)); got != systemPanelHeight {
		t.Errorf("got %d rows, want a fixed %d so the list does not shift", got, systemPanelHeight)
	}
}

func TestSystemPanelHiddenWhenDisabled(t *testing.T) {
	app := withMetrics(t)
	app.showSystem = false

	if panel := app.systemPanel(120, 30); panel != nil {
		t.Errorf("got %v, want nothing when the panel is switched off", panel)
	}
}

// Someone reading a stack trace does not want five rows of host metrics.
func TestSystemPanelHiddenInFullScreenViewers(t *testing.T) {
	app := withMetrics(t)

	for _, mode := range []Mode{ModeLogs, ModeInspect, ModeHelp} {
		app.mode = mode
		if panel := app.systemPanel(120, 30); panel != nil {
			t.Errorf("mode %v should not draw the panel", mode)
		}
	}
}

// On a short terminal the list matters more than the metrics.
func TestSystemPanelDroppedWhenItWouldCrowdTheList(t *testing.T) {
	app := withMetrics(t)

	if panel := app.systemPanel(120, systemPanelHeight+2); panel != nil {
		t.Error("the panel should yield to the list when space is tight")
	}
	if panel := app.systemPanel(120, systemPanelHeight+minListRowsWithPanel); panel == nil {
		t.Error("the panel should appear once the list keeps enough rows")
	}
}

// The panel must never push the frame past the screen.
func TestFrameStaysWithinBoundsWithPanel(t *testing.T) {
	app := withMetrics(t)

	for _, size := range [][2]int{{80, 24}, {120, 40}, {60, 12}, {40, 8}} {
		lines := app.Frame(size[0], size[1])

		if len(lines) != size[1] {
			t.Errorf("at %dx%d got %d lines, want %d", size[0], size[1], len(lines), size[1])
		}
		for _, line := range lines {
			if tui.VisibleWidth(line) > size[0] {
				t.Errorf("at %dx%d a line overflowed: %q", size[0], size[1], line)
			}
		}
	}
}

func TestRenderBarFillsProportionally(t *testing.T) {
	app := newTestApp(t)

	empty := app.renderBar(0, 12)
	half := app.renderBar(0.5, 12)
	full := app.renderBar(1, 12)

	if strings.Count(empty, barFilled) != 0 {
		t.Errorf("got %q, want no filled cells at zero", empty)
	}
	if got := strings.Count(half, barFilled); got != 5 {
		t.Errorf("got %d filled cells at 50%% of 10 inner cells, want 5", got)
	}
	if got := strings.Count(full, barFilled); got != 10 {
		t.Errorf("got %d filled cells at 100%%, want the bar full", got)
	}
}

// An idle host and a lightly loaded one should not look identical.
func TestRenderBarShowsAtLeastOneCellForAnyUsage(t *testing.T) {
	app := newTestApp(t)

	bar := app.renderBar(0.001, 12)

	if strings.Count(bar, barFilled) != 1 {
		t.Errorf("got %q, want a single filled cell for a small non-zero value", bar)
	}
}

func TestRenderBarClampsOutOfRangeValues(t *testing.T) {
	app := newTestApp(t)

	for _, fraction := range []float64{-1, 1.5, 99} {
		bar := app.renderBar(fraction, 12)
		if width := tui.VisibleWidth(bar); width != 12 {
			t.Errorf("fraction %v produced width %d, want 12", fraction, width)
		}
	}
}

func TestRenderBarHasConsistentWidth(t *testing.T) {
	app := newTestApp(t)

	for _, fraction := range []float64{0, 0.33, 0.5, 1} {
		if got := tui.VisibleWidth(app.renderBar(fraction, 20)); got != 20 {
			t.Errorf("fraction %v produced width %d, want 20", fraction, got)
		}
	}
}

// Docker exposes no host-level utilisation, so the panel's figures come from
// summing the per-container samples.
func TestStatsAggregateIntoHostMetrics(t *testing.T) {
	app := newTestApp(t)

	statsLoaded{stats: map[string]docker.Stat{
		"a": {CPUPercent: 10.5, MemUsage: 1 << 30},
		"b": {CPUPercent: 4.5, MemUsage: 2 << 30},
	}}.apply(app)

	if app.metrics.CPUPercent != 15.0 {
		t.Errorf("got %v, want the container percentages summed", app.metrics.CPUPercent)
	}
	if app.metrics.MemBytes != 3<<30 {
		t.Errorf("got %d, want the memory summed", app.metrics.MemBytes)
	}
}

// A failed measurement should leave the last good reading on screen rather
// than blanking the line.
func TestFailedDiskMeasurementKeepsPreviousReading(t *testing.T) {
	app := withMetrics(t)
	previous := app.metrics.Disk.ImagesSize

	diskUsageLoaded{err: errFake}.apply(app)

	if app.metrics.Disk.ImagesSize != previous {
		t.Errorf("got %d, want the previous measurement retained", app.metrics.Disk.ImagesSize)
	}
	if !app.metrics.DiskKnown {
		t.Error("a failed refresh should not discard a known measurement")
	}
	if app.measuringDisk {
		t.Error("the in-flight flag must be cleared or no further attempt is made")
	}
}

// errFake is a stand-in error for event tests.
var errFake = fakeError("boom")

type fakeError string

func (e fakeError) Error() string { return string(e) }

// Answering "7GiB reclaimable, of what?" is the whole point of the line.
func TestDiskLineAttributesReclaimableSpace(t *testing.T) {
	app := withMetrics(t)
	app.metrics.Disk = docker.DiskUsage{
		ImagesSize:        5 << 30,
		ImagesReclaimable: 180 << 20,
		BuildCacheSize:    7 << 30,
		VolumesSize:       8 << 30,
		VolumeSizesKnown:  true,
	}

	line := app.renderDiskLine()

	if !strings.Contains(line, "build cache") {
		t.Errorf("got %q, want the build cache named as the source", line)
	}
	if !strings.Contains(line, "unused images") {
		t.Errorf("got %q, want images named too", line)
	}
	// Build cache dwarfs the images here and must be named first, since a
	// narrow terminal truncates the tail.
	if strings.Index(line, "build cache") > strings.Index(line, "unused images") {
		t.Errorf("got %q, want the largest source first", line)
	}
}

// The state the user actually hit: images all cleared, and a large build cache
// still reported. The line must not still say the space is in images.
func TestDiskLineDoesNotBlameImagesForBuildCache(t *testing.T) {
	app := withMetrics(t)
	app.metrics.Disk = docker.DiskUsage{
		ImagesSize:        5 << 30,
		ImagesReclaimable: 0,
		ImagesUnused:      0,
		BuildCacheSize:    7 << 30,
	}

	line := app.renderDiskLine()

	if strings.Contains(line, "unused images") {
		t.Errorf("got %q, want no mention of images when none are reclaimable", line)
	}
	if !strings.Contains(line, "build cache") {
		t.Errorf("got %q, want the build cache named", line)
	}
}

func TestDiskLineSaysNothingWhenNothingToReclaim(t *testing.T) {
	app := withMetrics(t)
	app.metrics.Disk = docker.DiskUsage{ImagesSize: 5 << 30, VolumeSizesKnown: true}

	line := app.renderDiskLine()

	if strings.Contains(line, "reclaimable") {
		t.Errorf("got %q, want no reclaimable claim when there is none", line)
	}
}
