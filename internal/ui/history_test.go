package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/RchrdHndrcks/muelle/internal/docker"
	"github.com/RchrdHndrcks/muelle/internal/tui"
)

func sampleHistory() []docker.HistoryEntry {
	now := time.Now().Unix()
	return []docker.HistoryEntry{
		{ID: "sha256:top", Created: now - 3600, CreatedBy: `/bin/sh -c #(nop)  CMD ["mysqld"]`, Size: 0},
		{ID: "<missing>", Created: now - 7200, CreatedBy: "/bin/sh -c apt-get update && apt-get install -y mysql-server", Size: 400 << 20},
		{ID: "<missing>", Created: now - 86400, CreatedBy: "ADD file:base.tar in /", Size: 100 << 20},
	}
}

func withHistory(t *testing.T) *App {
	t.Helper()
	app := newTestApp(t)
	app.history = sampleHistory()
	app.historyTitle = "mysql:8.0"
	app.mode = ModeHistory
	return app
}

// The classic builder wraps every instruction in "/bin/sh -c", with a "#(nop)"
// marker on metadata-only steps; half of every row would be that noise.
func TestTrimCreatedByStripsClassicBuilderNoise(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"metadata step loses the wrapper", `/bin/sh -c #(nop)  CMD ["mysqld"]`, `CMD ["mysqld"]`},
		{"a real RUN keeps its shell", "/bin/sh -c apt-get update", "/bin/sh -c apt-get update"},
		{"buildkit records are untouched", "RUN /bin/bash -c set -eux", "RUN /bin/bash -c set -eux"},
		{"tabs and newlines collapse", "/bin/sh -c #(nop)\tADD\nfile:abc in /", "ADD file:abc in /"},
		{"empty stays empty", "", ""},
	}
	for _, c := range cases {
		if got := TrimCreatedBy(c.in); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

// The header carries the two figures the layers only imply: the image's total
// and across how many layers it is spread.
func TestRenderHistoryShowsTotalsAndHeadings(t *testing.T) {
	app := withHistory(t)

	rendered := strings.Join(app.renderHistory(120, 20), "\n")

	if !strings.Contains(rendered, "mysql:8.0") {
		t.Errorf("history is missing the image tag:\n%s", rendered)
	}
	if !strings.Contains(rendered, "500MiB in 3 layers") {
		t.Errorf("history is missing the size total and layer count:\n%s", rendered)
	}
	if !strings.Contains(rendered, "SIZE") || !strings.Contains(rendered, "CREATED BY") {
		t.Errorf("history is missing its column headings:\n%s", rendered)
	}
}

// Newest first, as the daemon reports: the layers your own Dockerfile added
// sit at the top, and they are the ones you can act on.
func TestRenderHistoryKeepsNewestFirst(t *testing.T) {
	app := withHistory(t)

	rendered := strings.Join(app.renderHistory(120, 20), "\n")

	newest := strings.Index(rendered, `CMD ["mysqld"]`)
	oldest := strings.Index(rendered, "ADD file:base.tar")
	if newest < 0 || oldest < 0 {
		t.Fatalf("history is missing its rows:\n%s", rendered)
	}
	if newest > oldest {
		t.Errorf("newest layer should be listed first:\n%s", rendered)
	}
}

// Metadata-only steps add nothing; in a view about where the bytes went a
// loud zero would compete with the layers that answer the question.
func TestRenderHistoryShowsZeroSizeQuietly(t *testing.T) {
	app := withHistory(t)

	rendered := strings.Join(app.renderHistory(120, 20), "\n")

	if !strings.Contains(rendered, "0B") {
		t.Errorf("a metadata-only layer should still show its 0B:\n%s", rendered)
	}
	if !strings.Contains(rendered, "400MiB") {
		t.Errorf("a heavy layer should show its size:\n%s", rendered)
	}
}

// Rows carry SGR escapes, so truncation must be ANSI-aware or a long RUN
// command corrupts the rest of the screen.
func TestHistoryRowsFitTheWidth(t *testing.T) {
	app := withHistory(t)

	for _, width := range []int{40, 80, 200} {
		for _, line := range app.renderHistory(width, 20) {
			if tui.VisibleWidth(line) > width {
				t.Errorf("at width %d a row overflowed: %q", width, line)
			}
		}
	}
}

func TestRenderHistoryHandlesEmptyHistory(t *testing.T) {
	app := newTestApp(t)

	rendered := strings.Join(app.renderHistory(80, 10), "\n")

	if !strings.Contains(rendered, "No history") {
		t.Errorf("got %q, want an empty state", rendered)
	}
}

// Enter and i open the history from the images view, the same keys that
// inspect a container.
func TestEnterOpensHistoryFromImagesView(t *testing.T) {
	for _, key := range []tui.Key{typeKey(tui.KeyEnter), runeKey('i')} {
		app := loadedApp(t)
		app.SetView(ViewImages)

		press(app, key)

		// The fetch runs in a goroutine and reports back as an event, the
		// way everything asynchronous does; the test plays the event loop.
		select {
		case ev := <-app.events:
			ev.apply(app)
		case <-time.After(5 * time.Second):
			t.Fatalf("key %v: no event arrived", key)
		}

		if app.mode != ModeHistory {
			t.Fatalf("key %v: got mode %v, want the history viewer", key, app.mode)
		}
		if app.historyTitle != "mysql:8.0" {
			t.Errorf("key %v: got title %q, want the image tag", key, app.historyTitle)
		}
		if len(app.history) != 2 {
			t.Errorf("key %v: got %d layers, want the daemon's 2", key, len(app.history))
		}
	}
}

func TestEscapeLeavesTheHistoryView(t *testing.T) {
	app := withHistory(t)

	press(app, typeKey(tui.KeyEscape))

	if app.mode != ModeList {
		t.Error("Escape should return to the list")
	}
}

func TestHistoryViewScrolls(t *testing.T) {
	app := withHistory(t)
	for range 100 {
		app.history = append(app.history, docker.HistoryEntry{CreatedBy: "RUN true", Size: 1})
	}

	press(app, runeKey('G'))
	if app.historyPager.Offset() == 0 {
		t.Error("G should scroll to the bottom of a long history")
	}

	press(app, runeKey('g'))
	if app.historyPager.Offset() != 0 {
		t.Error("g should scroll back to the top")
	}
}

func TestStatusBarNamesTheInspectedImage(t *testing.T) {
	app := withHistory(t)

	bar := app.renderStatusBar(120)

	if !strings.Contains(bar, "mysql:8.0") {
		t.Errorf("got %q, want the image tag while its history is open", bar)
	}
}
