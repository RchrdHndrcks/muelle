package ui

// Multi-select for the containers view.
//
// A mark is a container set aside for a bulk action: while any container is
// marked, the lifecycle keys act on the whole marked set instead of the row
// under the cursor. Marks are keyed by container ID rather than by list index,
// so a refresh that reorders the list — or a filter that hides half of it —
// does not silently move a mark onto a different container. The price of that
// choice is paid in pruneMarks: an ID whose container has left the list would
// otherwise linger invisibly and rejoin the next bulk action.

import (
	"context"
	"fmt"
	"time"

	"github.com/RchrdHndrcks/muelle/internal/docker"
	"github.com/RchrdHndrcks/muelle/internal/group"
	"github.com/RchrdHndrcks/muelle/internal/tui"
)

// toggleMark flips the mark on the row under the cursor.
//
// On a group heading it toggles the whole group at once: marking an
// application's containers one by one is exactly the busywork grouping exists
// to remove.
func (a *App) toggleMark() {
	row, ok := a.selectedRow()
	if !ok {
		return
	}
	if row.Header != nil {
		a.toggleGroupMarks(row.Header.Name)
		return
	}
	if a.marked[row.Container.ID] {
		delete(a.marked, row.Container.ID)
		return
	}
	a.marked[row.Container.ID] = true
}

// toggleGroupMarks marks every container listed under a heading, or unmarks
// them all when every one is already marked.
//
// A partially marked group becomes fully marked rather than inverted: the
// gesture means "all of these", and completing the set is the reading that
// honours it. Only a group with nothing left to mark reads as "none of them".
func (a *App) toggleGroupMarks(name string) {
	members := a.groupMembers(name)
	if len(members) == 0 {
		return
	}
	everyMemberMarked := true
	for _, container := range members {
		if !a.marked[container.ID] {
			everyMemberMarked = false
			break
		}
	}
	for _, container := range members {
		if everyMemberMarked {
			delete(a.marked, container.ID)
			continue
		}
		a.marked[container.ID] = true
	}
}

// groupMembers returns the containers a heading stands for, honouring the
// filter the same way rows does — space on a heading must toggle the rows the
// user can see beneath it, not containers the filter has hidden.
func (a *App) groupMembers(name string) []docker.Container {
	for _, found := range group.Build(a.sortedContainers()) {
		if found.Name == name {
			return a.matching(found.Containers)
		}
	}
	return nil
}

// clearMarks forgets every mark.
func (a *App) clearMarks() {
	clear(a.marked)
}

// markedContainers returns the marked containers in the order the daemon
// lists them, which is the order a bulk action visits them in.
func (a *App) markedContainers() []docker.Container {
	if len(a.marked) == 0 {
		return nil
	}
	picked := make([]docker.Container, 0, len(a.marked))
	for _, container := range a.containers {
		if a.marked[container.ID] {
			picked = append(picked, container)
		}
	}
	return picked
}

// pruneMarks drops marks whose containers are no longer listed.
//
// Keying marks by ID is what lets them survive a refresh; this is the other
// half of that decision. A removed container's ID never comes back, so its
// mark can only go stale — and a stale mark is worse than a lost one, because
// it silently widens the next bulk action to something no longer on screen.
func (a *App) pruneMarks() {
	if len(a.marked) == 0 {
		return
	}
	listed := make(map[string]bool, len(a.containers))
	for _, container := range a.containers {
		listed[container.ID] = true
	}
	for id := range a.marked {
		if !listed[id] {
			delete(a.marked, id)
		}
	}
}

// handleBulkKey routes the lifecycle keys to the marked set. It reports
// whether the key was one of them.
//
// Only start, stop, kill and remove act in bulk: they are idempotent per
// container and mean the same thing applied to many. Kill and remove keep
// their confirmations — now stating the count, which is the figure that makes
// the confirmation worth reading — while start and stop stay unconfirmed,
// exactly as they are for a single container.
func (a *App) handleBulkKey(ctx context.Context, key tui.Key) bool {
	targets := a.markedContainers()
	if len(targets) == 0 {
		return false
	}
	ids := make([]string, len(targets))
	for i, container := range targets {
		ids[i] = container.ID
	}
	count := Plural(len(ids), "container", "containers")

	switch {
	case key.IsRune('s'):
		a.runBulkAction(ctx, "started", ids, func(c context.Context, id string) error {
			return a.docker.Start(c, id)
		})

	case key.IsRune('t'):
		a.runBulkAction(ctx, "stopped", ids, func(c context.Context, id string) error {
			return a.docker.Stop(c, id, a.config.StopTimeout)
		})

	case key.IsRune('K'):
		a.confirm("Kill containers", "Send SIGKILL to "+count+"?", func() {
			a.runBulkAction(ctx, "killed", ids, func(c context.Context, id string) error {
				return a.docker.Kill(c, id)
			})
		})

	case key.IsRune('D'):
		a.confirm("Remove containers", "Remove "+count+"? This cannot be undone.", func() {
			a.runBulkAction(ctx, "removed", ids, func(c context.Context, id string) error {
				return a.docker.Remove(c, id, true, false)
			})
		})

	default:
		return false
	}
	return true
}

// runBulkAction performs one lifecycle call per container, sequentially, in a
// single goroutine, and reports one aggregated outcome.
//
// Sequential rather than fanned out: the order is deterministic, the daemon is
// never hit with a burst of concurrent stops, and a failure can be attributed
// to a container rather than to a race. One container failing does not stop
// the rest — half a bulk stop is still worth having — so the report counts
// both outcomes and quotes the first error, which is almost always the story
// behind the others too. The timeout scales with the count because a stop
// alone is allowed StopTimeout seconds per container.
func (a *App) runBulkAction(ctx context.Context, verb string, ids []string, action func(context.Context, string) error) {
	go func() {
		actionCtx, cancel := context.WithTimeout(ctx, time.Duration(len(ids))*2*time.Minute)
		defer cancel()

		succeeded, failed := 0, 0
		var firstErr error
		for _, id := range ids {
			err := action(actionCtx, id)
			if err != nil && docker.NotModified(err) {
				// The container was already in the requested state,
				// which is what the user wanted.
				err = nil
			}
			if err != nil {
				failed++
				if firstErr == nil {
					firstErr = err
				}
				continue
			}
			succeeded++
		}

		if failed > 0 {
			a.post(actionDone{err: fmt.Errorf("%s %d, %d failed: %v", verb, succeeded, failed, firstErr)})
		} else {
			a.post(actionDone{message: fmt.Sprintf("%s %s", verb, Plural(succeeded, "container", "containers"))})
		}
		a.post(refreshRequested{})
	}()
}
