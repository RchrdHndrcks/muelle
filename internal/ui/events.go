package ui

import (
	"context"
	"strings"
	"time"

	"github.com/RchrdHndrcks/muelle/internal/docker"
	"github.com/RchrdHndrcks/muelle/internal/tui"
)

// eventHistory is how many daemon events are kept for the events view.
//
// Bounded for the same reason the log buffer is: the feed never ends, so an
// unbounded list would grow the process for as long as it runs. Five hundred
// covers hours of an ordinary host and several deployments of a busy one,
// which is as far back as "what just happened here?" ever reaches.
const eventHistory = 500

// eventBackfill is how far into the past the stream is opened.
//
// The daemon keeps a log of recent events and replays it when asked, so the
// view has history at launch instead of starting empty — muelle is usually
// opened after the thing worth investigating has already happened. An hour is
// enough to cover the restart someone came to ask about without dredging up a
// morning's worth of routine churn.
const eventBackfill = time.Hour

// EventRing holds the most recent daemon events, oldest first.
//
// A fixed capacity with the oldest evicted on overflow, like the log buffer:
// what happened most recently is the question this exists to answer, and
// anything pushed off the end had already scrolled out of relevance.
type EventRing struct {
	events   []docker.Event
	capacity int
}

// NewEventRing creates a ring holding at most capacity events.
func NewEventRing(capacity int) *EventRing {
	if capacity < 1 {
		capacity = 1
	}
	return &EventRing{capacity: capacity, events: make([]docker.Event, 0, capacity)}
}

// Append adds an event, evicting the oldest once full.
func (r *EventRing) Append(event docker.Event) {
	if len(r.events) < r.capacity {
		r.events = append(r.events, event)
		return
	}
	// Shift down by one. Copy rather than reslice so the backing array
	// stays a fixed size instead of creeping forward in memory.
	copy(r.events, r.events[1:])
	r.events[len(r.events)-1] = event
}

// Len returns how many events are held.
func (r *EventRing) Len() int { return len(r.events) }

// Snapshot returns the events newest first.
//
// Newest first because that is how the view reads: the event someone opened
// the view for is the most recent one, and it should be the first row rather
// than five hundred rows down.
func (r *EventRing) Snapshot() []docker.Event {
	snapshot := make([]docker.Event, len(r.events))
	for i, event := range r.events {
		snapshot[len(r.events)-1-i] = event
	}
	return snapshot
}

// eventObserved carries one entry from the daemon's event stream.
//
// This is the fan-out point: the daemon serves one stream, and both the
// events view and the deploy tracker need it. Rather than two connections or
// a broadcaster with subscribers, the event lands here — on the goroutine
// that owns the model, like every other asynchronous result — and its apply
// hands it to each consumer in turn.
type eventObserved struct {
	event docker.Event
	// live distinguishes an event that just happened from one the daemon
	// replayed as backfill. History belongs in the events view, but
	// feeding it to the deploy tracker would resurrect deployments that
	// finished long before muelle started, complete with ghost rows
	// waiting for replacements that arrived an hour ago.
	live bool
}

func (e eventObserved) apply(a *App) {
	if a.daemonEvents != nil {
		a.daemonEvents.Append(e.event)
	}
	// The tracker follows container lifecycle only: an image pull or a
	// network create says nothing about a service being replaced.
	if e.live && e.event.Type == "container" {
		deployObserved{event: e.event}.apply(a)
	}
}

// watchEvents follows the daemon's event stream for the life of the app.
//
// This cannot be polled: a deployment is over in less time than the refresh
// interval, so a poll would show the aftermath and never the act.
func (a *App) watchEvents(ctx context.Context) {
	// Whether an event is history or news is decided by its own timestamp
	// against the moment the stream opened. The daemon's clock stamps the
	// events and ours draws the line, so a skewed remote daemon can misfile
	// one near the boundary — tolerable, because the deploy tracker writes
	// off a deployment that never concludes on its own.
	startedAt := time.Now()
	stream, err := a.docker.Events(ctx, startedAt.Add(-eventBackfill))
	if err != nil {
		// Not fatal, and not worth an error banner: everything else still
		// works, the list just goes back to being as current as the last
		// poll. An old daemon or a proxy that will not stream is the
		// usual cause.
		return
	}
	go func() {
		defer stream.Close()
		for event := range stream.Events() {
			a.post(eventObserved{
				event: event,
				live:  event.TimeNano >= startedAt.UnixNano(),
			})
		}
	}()
}

// filteredEvents returns the buffered events matching the current filter,
// newest first.
func (a *App) filteredEvents() []docker.Event {
	events := a.daemonEvents.Snapshot()
	if a.filter == "" {
		return events
	}
	needle := strings.ToLower(a.filter)
	matched := make([]docker.Event, 0, len(events))
	for _, event := range events {
		haystack := strings.ToLower(event.Type + " " + event.Action + " " + eventName(event))
		if strings.Contains(haystack, needle) {
			matched = append(matched, event)
		}
	}
	return matched
}

// renderEvents draws the events view: a timeline of daemon activity, newest
// first. Selection moves like any other list, but the rows are records rather
// than objects, so there is nothing to act on yet.
func (a *App) renderEvents(width, height int) []string {
	style := a.screen.Style
	events := a.filteredEvents()

	if len(events) == 0 {
		return a.emptyState(width, height, "No events yet. Daemon activity appears here as it happens.")
	}

	columns := []Column{
		{Title: "time", Width: 8},
		{Title: "type", Width: 9},
		// Wide enough for "health_status: unhealthy", the longest action
		// the daemon emits, so the one that matters is never the one cut.
		{Title: "action", Width: 24},
		{Title: "name", Width: 30, Flex: true},
	}
	widths := LayoutColumns(columns, width)

	lines := []string{style(styleColumn, RenderHeader(columns, widths))}
	selected := a.selected()
	start, end, offset := visibleWindow(len(events), height-1, selected, a.offset[ViewEvents])
	a.offset[ViewEvents] = offset

	for i := start; i < end; i++ {
		row := RenderRow(a.eventCells(events[i]), widths)
		if i == selected {
			row = style(styleSelected, tui.Pad(row, width))
		}
		lines = append(lines, row)
	}
	return lines
}

// eventCells renders one event as the columns of the list.
func (a *App) eventCells(event docker.Event) []string {
	style := a.screen.Style
	return []string{
		style(styleTimestamp, FormatEventTime(event.TimeNano)),
		style(styleMuted, event.Type),
		style(eventActionStyle(event.Action), event.Action),
		eventName(event),
	}
}

// eventName identifies the object an event happened to.
//
// The daemon copies a name into the Actor attributes for every type muelle
// asks for — container and volume names, image references, network names —
// so the ID is only a fallback for an event that arrives without one.
func eventName(event docker.Event) string {
	if name := event.Name(); name != "" {
		return name
	}
	if len(event.Actor.ID) > 12 {
		return event.Actor.ID[:12]
	}
	return event.Actor.ID
}

// FormatEventTime renders an event's timestamp as a clock time.
//
// A clock time rather than a relative age: events are read as a timeline, and
// "14:02:07 above 14:02:03" orders and spaces itself in a way that "2m ago
// above 2m ago" does not. The date is dropped because the buffer rarely spans
// one — and never spans two.
func FormatEventTime(nano int64) string {
	return time.Unix(0, nano).Format("15:04:05")
}

// eventActionStyle colours the actions worth noticing.
//
// Only the endings are coloured: a die or an oom is what someone opens this
// view to find, and painting every start and create as well would leave the
// whole timeline coloured, which is the same as none of it being coloured.
func eventActionStyle(action string) tui.Style {
	switch action {
	case "die", "oom", "kill", "destroy", "delete":
		return tui.Foreground(colourRed)
	default:
		return tui.StyleNone
	}
}
