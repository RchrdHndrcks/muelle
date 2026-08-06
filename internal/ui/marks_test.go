package ui

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/RchrdHndrcks/muelle/internal/tui"
)

// The fake daemon's container IDs, named for readability in assertions.
const (
	idShopAPI = "aaaaaaaaaaaa1111"
	idShopDB  = "bbbbbbbbbbbb2222"
	idCache   = "cccccccccccc3333"
)

// waitEvent receives one event from the app's channel, for tests that exercise
// work posted from a goroutine.
func waitEvent(t *testing.T, app *App) event {
	t.Helper()
	select {
	case ev := <-app.events:
		return ev
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for an event")
		return nil
	}
}

func TestSpaceTogglesMarkOnContainer(t *testing.T) {
	app := loadedApp(t)

	container, ok := app.selectedContainer()
	if !ok {
		t.Fatal("expected a selected container")
	}

	press(app, runeKey(' '))
	if !app.marked[container.ID] {
		t.Fatal("space should mark the selected container")
	}

	press(app, runeKey(' '))
	if len(app.marked) != 0 {
		t.Error("space again should unmark it")
	}
}

// Space on a heading toggles the whole group: marking an application's
// containers one by one is the busywork grouping exists to remove.
func TestSpaceOnHeadingTogglesWholeGroup(t *testing.T) {
	app := loadedApp(t)

	app.selection[ViewContainers] = 0 // the "shop" heading, first with grouping on
	if row, ok := app.selectedRow(); !ok || row.Header == nil {
		t.Fatal("expected the first row to be a group heading")
	}

	press(app, runeKey(' '))
	if !app.marked[idShopAPI] || !app.marked[idShopDB] {
		t.Fatalf("space on the heading should mark both shop containers, got %v", app.marked)
	}
	if app.marked[idCache] {
		t.Error("containers outside the group must not be marked")
	}

	press(app, runeKey(' '))
	if len(app.marked) != 0 {
		t.Errorf("space again should unmark the whole group, got %v", app.marked)
	}
}

// A partially marked group completes rather than inverts: the gesture means
// "all of these".
func TestSpaceOnHeadingCompletesPartialGroup(t *testing.T) {
	app := loadedApp(t)
	app.marked[idShopAPI] = true

	app.selection[ViewContainers] = 0
	press(app, runeKey(' '))

	if !app.marked[idShopAPI] || !app.marked[idShopDB] {
		t.Errorf("a partially marked group should become fully marked, got %v", app.marked)
	}
}

// Esc peels back the most recent layer of intent: marks first, filter second.
func TestEscapeClearsMarksBeforeFilter(t *testing.T) {
	app := loadedApp(t)
	app.filter = "shop"
	app.marked[idShopAPI] = true

	press(app, typeKey(tui.KeyEscape))

	if len(app.marked) != 0 {
		t.Fatal("the first Esc should clear the marks")
	}
	if app.filter != "shop" {
		t.Fatalf("the first Esc must leave the filter alone, got %q", app.filter)
	}

	press(app, typeKey(tui.KeyEscape))
	if app.filter != "" {
		t.Errorf("the second Esc should clear the filter, got %q", app.filter)
	}
}

// The destructive bulk keys still confirm, and the prompt states the count —
// that number is what makes the confirmation worth reading.
func TestBulkRemoveConfirmsWithCount(t *testing.T) {
	app := loadedApp(t)
	app.marked[idShopAPI] = true
	app.marked[idShopDB] = true

	press(app, runeKey('D'))

	if app.overlay == nil || app.overlay.Kind != OverlayConfirm {
		t.Fatal("D with marks should raise a confirmation")
	}
	if app.overlay.Prompt != "Remove 2 containers? This cannot be undone." {
		t.Errorf("got prompt %q, want the count stated", app.overlay.Prompt)
	}
}

func TestBulkKillConfirmsWithCount(t *testing.T) {
	app := loadedApp(t)
	app.marked[idShopAPI] = true
	app.marked[idCache] = true

	press(app, runeKey('K'))

	if app.overlay == nil || app.overlay.Kind != OverlayConfirm {
		t.Fatal("K with marks should raise a confirmation")
	}
	if app.overlay.Prompt != "Send SIGKILL to 2 containers?" {
		t.Errorf("got prompt %q, want the count stated", app.overlay.Prompt)
	}
}

// Start and stop stay unconfirmed in bulk, exactly as for a single container.
func TestBulkStartAndStopDoNotConfirm(t *testing.T) {
	for _, key := range []rune{'s', 't'} {
		app := loadedApp(t)
		app.marked[idShopDB] = true

		press(app, runeKey(key))

		if app.overlay != nil {
			t.Errorf("%q with marks should act without confirmation", key)
		}
	}
}

// With marks, the lifecycle keys act on the marked set even while the cursor
// sits on a heading, where a single-container action would be a no-op.
func TestBulkKeysWorkFromAHeading(t *testing.T) {
	app := loadedApp(t)
	app.marked[idCache] = true
	app.selection[ViewContainers] = 0 // the "shop" heading

	press(app, runeKey('D'))

	if app.overlay == nil || app.overlay.Kind != OverlayConfirm {
		t.Error("D should reach the marked set from a heading")
	}
}

// The bulk worker visits every ID sequentially and reports one aggregate.
func TestBulkActionAggregatesSuccess(t *testing.T) {
	app := loadedApp(t)
	ids := []string{"one", "two", "three", "four"}
	var visited []string

	app.runBulkAction(context.Background(), "stopped", ids, func(_ context.Context, id string) error {
		visited = append(visited, id)
		return nil
	})

	done, ok := waitEvent(t, app).(actionDone)
	if !ok {
		t.Fatal("expected an actionDone event")
	}
	done.apply(app)

	if app.status.isError {
		t.Fatalf("got an error status: %q", app.status.text)
	}
	if app.status.text != "stopped 4 containers" {
		t.Errorf("got %q, want the aggregated count", app.status.text)
	}
	if !reflect.DeepEqual(visited, ids) {
		t.Errorf("got visit order %v, want the IDs in order", visited)
	}
}

// One container failing must not stop the rest, and the report counts both
// outcomes so the user knows what state the host is in.
func TestBulkActionReportsPartialFailure(t *testing.T) {
	app := loadedApp(t)
	ids := []string{"one", "two", "three", "four"}
	var visited []string

	app.runBulkAction(context.Background(), "stopped", ids, func(_ context.Context, id string) error {
		visited = append(visited, id)
		if id == "two" {
			return errors.New("no such container")
		}
		return nil
	})

	done, ok := waitEvent(t, app).(actionDone)
	if !ok {
		t.Fatal("expected an actionDone event")
	}
	done.apply(app)

	if !app.status.isError {
		t.Error("a partial failure should be reported as an error")
	}
	if app.status.text != "stopped 3, 1 failed: no such container" {
		t.Errorf("got %q, want both outcomes and the first error", app.status.text)
	}
	if len(visited) != len(ids) {
		t.Errorf("visited %d of %d IDs; a failure must not stop the rest", len(visited), len(ids))
	}
}

// Marks are keyed by ID so a refresh that replaces the list wholesale keeps
// them — and drops the ones whose containers are gone.
func TestMarksSurviveRefreshAndDropVanished(t *testing.T) {
	app := loadedApp(t)
	app.marked[idShopAPI] = true
	app.marked["deadbeefdeadbeef"] = true // no longer listed

	containersLoaded{containers: app.containers, projects: app.projects}.apply(app)

	if !app.marked[idShopAPI] {
		t.Error("a mark whose container is still listed should survive the refresh")
	}
	if app.marked["deadbeefdeadbeef"] {
		t.Error("a mark whose container vanished should be dropped")
	}
}

// The mark is drawn in the gutter, so it is visible without shifting the name
// column around.
func TestMarkedRowShowsGutterStar(t *testing.T) {
	app := loadedApp(t)
	app.grouped = false
	app.marked[idCache] = true

	frame := frameText(app)

	if !strings.Contains(frame, "* standalone-cache") {
		t.Errorf("the marked row should carry a gutter star:\n%s", frame)
	}
	if strings.Contains(frame, "* shop-api") {
		t.Errorf("unmarked rows must not carry the star:\n%s", frame)
	}
}

func TestStatusLineCountsMarks(t *testing.T) {
	app := loadedApp(t)
	app.marked[idShopAPI] = true
	app.marked[idCache] = true

	if !strings.Contains(frameText(app), "2 marked") {
		t.Error("the status line should count the marks while any exist")
	}
}
