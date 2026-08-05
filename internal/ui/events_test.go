package ui

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/RchrdHndrcks/muelle/internal/docker"
	"github.com/RchrdHndrcks/muelle/internal/tui"
)

// daemonEvent builds an event the way the daemon reports one.
func daemonEvent(kind, action, name string, at time.Time) docker.Event {
	var e docker.Event
	e.Type = kind
	e.Action = action
	e.Actor.ID = "0123456789abcdef"
	e.Actor.Attributes = map[string]string{"name": name}
	e.TimeNano = at.UnixNano()
	return e
}

// The ring is what makes watching the feed forever safe: the stream never
// ends, so an unbounded list would grow the process for as long as it runs.
func TestEventRingEvictsTheOldestOnceFull(t *testing.T) {
	ring := NewEventRing(3)
	for i := range 5 {
		ring.Append(daemonEvent("container", "start", "c"+strconv.Itoa(i), time.Now()))
	}

	if ring.Len() != 3 {
		t.Fatalf("got %d events, want the capacity of 3", ring.Len())
	}
	snapshot := ring.Snapshot()
	if got := snapshot[len(snapshot)-1].Name(); got != "c2" {
		t.Errorf("got oldest %q, want c0 and c1 evicted, leaving c2", got)
	}
}

// Newest first, because the event someone opened the view for is the most
// recent one, and it should be the first row rather than hundreds down.
func TestEventRingSnapshotIsNewestFirst(t *testing.T) {
	ring := NewEventRing(10)
	ring.Append(daemonEvent("container", "create", "first", time.Now()))
	ring.Append(daemonEvent("container", "start", "second", time.Now()))

	snapshot := ring.Snapshot()

	if snapshot[0].Action != "start" || snapshot[1].Action != "create" {
		t.Errorf("got %q then %q, want the later event first", snapshot[0].Action, snapshot[1].Action)
	}
}

func TestEventRingClampsASillyCapacity(t *testing.T) {
	ring := NewEventRing(0)
	ring.Append(daemonEvent("container", "start", "c", time.Now()))
	if ring.Len() != 1 {
		t.Errorf("got %d, want a usable ring even from a zero capacity", ring.Len())
	}
}

// One stream feeds two consumers: the ring for the view, the tracker for the
// deploy phases. A live container event must reach both.
func TestALiveContainerEventFeedsTheRingAndTheTracker(t *testing.T) {
	app := newTestApp(t)

	eventObserved{event: destroyOf("shop", "api"), live: true}.apply(app)

	if app.daemonEvents.Len() != 1 {
		t.Error("the event should have landed in the ring for the events view")
	}
	if _, tracked := app.deploys.State("shop/api"); !tracked {
		t.Error("the event should have been folded into the deploy tracker")
	}
}

// Backfill is history. It belongs in the events view, but feeding it to the
// tracker would resurrect deployments that finished long before muelle
// started.
func TestABackfilledEventIsShownButNotTracked(t *testing.T) {
	app := newTestApp(t)

	eventObserved{event: destroyOf("shop", "api"), live: false}.apply(app)

	if app.daemonEvents.Len() != 1 {
		t.Error("history still belongs in the events view")
	}
	if _, tracked := app.deploys.State("shop/api"); tracked {
		t.Error("a replayed destroy must not start a phantom deployment")
	}
}

// The stream now carries images, volumes and networks for the view; the
// tracker only ever cared about containers.
func TestANonContainerEventSkipsTheTracker(t *testing.T) {
	app := newTestApp(t)
	event := daemonEvent("network", "destroy", "shop_default", time.Now())
	event.Actor.Attributes[docker.LabelProject] = "shop"
	event.Actor.Attributes[docker.LabelService] = "api"

	eventObserved{event: event, live: true}.apply(app)

	if app.daemonEvents.Len() != 1 {
		t.Error("the network event should still appear in the events view")
	}
	if _, tracked := app.deploys.State("shop/api"); tracked {
		t.Error("a network destroy is not a service being replaced")
	}
}

func TestEventCellsCarryTimeTypeActionAndName(t *testing.T) {
	app := newTestApp(t)
	at := time.Date(2026, 8, 5, 14, 2, 7, 0, time.Local)

	cells := app.eventCells(daemonEvent("container", "oom", "shop-api", at))

	want := []string{"14:02:07", "container", "oom", "shop-api"}
	if len(cells) != len(want) {
		t.Fatalf("got %d cells, want %d", len(cells), len(want))
	}
	for i, cell := range cells {
		if cell != want[i] {
			t.Errorf("cell %d: got %q, want %q", i, cell, want[i])
		}
	}
}

// An event with no name attribute still identifies its object by ID, kept to
// the twelve characters everything else in docker abbreviates IDs to.
func TestEventCellsFallBackToAShortID(t *testing.T) {
	app := newTestApp(t)
	event := daemonEvent("container", "die", "", time.Now())
	delete(event.Actor.Attributes, "name")

	cells := app.eventCells(event)

	if got := cells[len(cells)-1]; got != "0123456789ab" {
		t.Errorf("got name %q, want the shortened actor ID", got)
	}
}

func TestEventsViewRendersNewestFirst(t *testing.T) {
	app := newTestApp(t)
	app.daemonEvents.Append(daemonEvent("container", "create", "older", time.Now()))
	app.daemonEvents.Append(daemonEvent("container", "die", "newer", time.Now()))
	app.SetView(ViewEvents)

	rendered := app.renderEvents(120, 20)

	joined := strings.Join(rendered, "\n")
	if !strings.Contains(joined, "older") || !strings.Contains(joined, "newer") {
		t.Fatalf("events view is missing rows:\n%s", joined)
	}
	if strings.Index(joined, "newer") > strings.Index(joined, "older") {
		t.Errorf("want the newest event on top:\n%s", joined)
	}
}

func TestEventsViewEmptyState(t *testing.T) {
	app := newTestApp(t)
	app.SetView(ViewEvents)

	rendered := strings.Join(app.renderEvents(80, 10), "\n")

	if !strings.Contains(rendered, "No events yet") {
		t.Errorf("got %q, want an empty state", rendered)
	}
}

func TestEventsViewIsReachableByKey(t *testing.T) {
	app := loadedApp(t)

	press(app, runeKey('6'))

	if app.view != ViewEvents {
		t.Errorf("got %v, want the events view", app.view.Title())
	}
}

func TestEventsViewIsInTheTabCycle(t *testing.T) {
	app := loadedApp(t)
	app.SetView(ViewNetworks)

	press(app, typeKey(tui.KeyTab))

	if app.view != ViewEvents {
		t.Errorf("got %v, want events after networks", app.view.Title())
	}
}

// The rows are records rather than objects, so Enter has nothing to act on —
// and must not act on something by accident.
func TestEnterDoesNothingOnAnEvent(t *testing.T) {
	app := loadedApp(t)
	app.daemonEvents.Append(daemonEvent("container", "die", "shop-api", time.Now()))
	app.SetView(ViewEvents)

	press(app, typeKey(tui.KeyEnter))

	if app.mode != ModeList {
		t.Errorf("got mode %v, want the list untouched", app.mode)
	}
	if app.overlay != nil {
		t.Error("no overlay should open from an event row")
	}
}

func TestEventFilterMatchesTypeActionAndName(t *testing.T) {
	app := newTestApp(t)
	app.daemonEvents.Append(daemonEvent("container", "die", "shop-api", time.Now()))
	app.daemonEvents.Append(daemonEvent("image", "pull", "shop/api:1.5", time.Now()))
	app.SetView(ViewEvents)

	for filter, want := range map[string]int{"die": 1, "image": 1, "shop": 2, "nothing-matches": 0} {
		app.filter = filter
		if got := len(app.filteredEvents()); got != want {
			t.Errorf("filter %q: got %d matches, want %d", filter, got, want)
		}
	}
}

func TestEventRowsFitTheWidth(t *testing.T) {
	app := newTestApp(t)
	app.daemonEvents.Append(daemonEvent("container", "health_status: unhealthy",
		"a-container-with-a-deliberately-long-name", time.Now()))
	app.SetView(ViewEvents)

	for _, width := range []int{40, 80, 200} {
		for _, line := range app.renderEvents(width, 20) {
			if tui.VisibleWidth(line) > width {
				t.Errorf("at width %d a row overflowed: %q", width, line)
			}
		}
	}
}

// Selection moves like any other list even though the rows are inert.
func TestEventSelectionMovesAndClamps(t *testing.T) {
	app := newTestApp(t)
	app.daemonEvents.Append(daemonEvent("container", "create", "one", time.Now()))
	app.daemonEvents.Append(daemonEvent("container", "start", "two", time.Now()))
	app.SetView(ViewEvents)

	press(app, runeKey('j'))
	if got := app.selected(); got != 1 {
		t.Errorf("got %d after j, want 1", got)
	}
	press(app, runeKey('j'))
	if got := app.selected(); got != 1 {
		t.Errorf("got %d, want the selection to stop at the last row", got)
	}
}
