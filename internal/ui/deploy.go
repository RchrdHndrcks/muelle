package ui

import (
	"context"
	"time"

	"github.com/RchrdHndrcks/muelle/internal/deploy"
	"github.com/RchrdHndrcks/muelle/internal/docker"
)

// deployGrace is how long a started container is called "starting" when
// nothing can tell us otherwise.
//
// A container with no healthcheck and no probe offers nothing to wait for, so
// this is the honest answer rather than a claim. It is long enough that a
// crash-loop shows itself: a container that dies after three seconds is
// destroyed and recreated well inside the window, so the row cycles through
// recreating instead of ever settling on up.
const deployGrace = 15 * time.Second

// deployPatience is how long a replacement is waited for before the deploy is
// written off. Long enough to cover a slow image load, short enough that a
// failed deploy does not leave the row lying about it.
const deployPatience = time.Minute

// deployObserved carries one event from the daemon's stream.
//
// Folded in on the goroutine that owns the model, like every other
// asynchronous result, rather than mutating the tracker from the stream. The
// tracker is safe either way; doing it here is what makes the event redraw the
// frame it changed.
type deployObserved struct{ event docker.Event }

func (e deployObserved) apply(a *App) {
	if a.deploys == nil {
		return
	}
	a.deploys.Observe(e.event, time.Now())
}

// watchEvents follows the daemon's event stream for the life of the app.
//
// This cannot be polled: a deployment is over in less time than the refresh
// interval, so a poll would show the aftermath and never the act.
func (a *App) watchEvents(ctx context.Context) {
	stream, err := a.docker.Events(ctx)
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
			a.post(deployObserved{event: event})
		}
	}()
}

// deployPhase reports what is happening to a container's service, if anything.
func (a *App) deployPhase(container docker.Container) (deploy.State, bool) {
	if a.deploys == nil {
		return deploy.State{}, false
	}
	return a.deploys.State(deploy.KeyFor(container))
}

// withGhosts adds a row for every service being replaced that has no container
// of its own on the host.
//
// Between the destroy and the create there is nothing to list, and that gap is
// exactly what makes a deployment invisible: the row disappears and comes back
// under a new ID, looking like a different container that happens to share a
// name.
func (a *App) withGhosts() []docker.Container {
	if a.deploys == nil {
		return a.containers
	}
	ghosts := a.deploys.Ghosts(a.containers)
	if len(ghosts) == 0 {
		return a.containers
	}
	combined := make([]docker.Container, 0, len(a.containers)+len(ghosts))
	combined = append(combined, a.containers...)
	return append(combined, ghosts...)
}
