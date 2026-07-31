// Package deploy follows a service across the containers that serve it.
//
// Deploying does not restart a container, it replaces one: the old container
// is destroyed and a new one created under a different ID. Anything keyed on
// the container ID therefore loses track exactly when a user most wants to be
// told what is happening — the row vanishes from the list and a different row
// appears in its place, with nothing to say the two are the same service.
//
// Keying on the Compose service instead follows the thing that persists. The
// daemon copies Compose's labels into every event it emits, so this needs no
// inspecting and no YAML: the events say which service each container belonged
// to, all the way through.
//
// There is no percentage here, and there cannot be. Docker has no notion of a
// container being partly started, and the only figure in the whole sequence
// that is a real fraction — the bytes of an image pull — is known solely to
// whoever ran the pull. What can be said honestly is which phase a service is
// in and how long it has been there.
package deploy

import (
	"sync"
	"time"

	"github.com/RchrdHndrcks/muelle/internal/docker"
)

// Phase is where a service is in the course of being replaced.
type Phase string

const (
	// Creating means a container has been made for a service that had none
	// and is not running yet — a first deploy rather than a replacement.
	Creating Phase = "creating"
	// Recreating means the old container is gone and the new one is not
	// running yet.
	Recreating Phase = "recreating"
	// Starting means the new container is running but has not yet been
	// shown to be serving.
	Starting Phase = "starting"
)

// State is what is known about a service being deployed.
type State struct {
	Phase Phase
	// Since is when the current phase began, not when the deploy did. The
	// question a user asks of a row that says "starting" is how long it has
	// been starting.
	Since time.Time
	// Last is the container that served this key before the current phase,
	// kept so the row can still be drawn while none exists.
	Last docker.Container
}

// Tracker follows every service that is being deployed.
//
// Safe for concurrent use: events arrive on a stream goroutine while the UI
// reads state to draw a frame.
type Tracker struct {
	// grace is how long a started container is given to show itself before
	// it is simply called up. It exists because a container without a
	// healthcheck offers nothing to wait for.
	grace time.Duration
	// patience is how long a recreate is followed before it is written off.
	patience time.Duration

	mu     sync.Mutex
	states map[string]State
	// last remembers the container most recently seen for each key.
	last map[string]docker.Container
}

// New builds a tracker.
func New(grace, patience time.Duration) *Tracker {
	return &Tracker{
		grace:    grace,
		patience: patience,
		states:   make(map[string]State),
		last:     make(map[string]docker.Container),
	}
}

// Key names the thing being deployed: a Compose service, or a standalone
// container by the name it is run under.
func Key(project, service, name string) string {
	if project != "" && service != "" {
		return project + "/" + service
	}
	if name == "" {
		return ""
	}
	return "container/" + name
}

// KeyFor returns the key a container belongs to.
func KeyFor(container docker.Container) string {
	return Key(container.Project(), container.Service(), container.Name())
}

// Observe folds an event into what is known.
func (t *Tracker) Observe(event docker.Event, now time.Time) {
	key := Key(event.Project(), event.Service(), event.Name())
	if key == "" {
		return
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	// A health report is the only ending that is a fact rather than an
	// assumption. Unhealthy ends it too: the deploy is over, and the state
	// column has its own way of saying the result was bad.
	if _, isHealth := event.HealthStatus(); isHealth {
		delete(t.states, key)
		return
	}

	switch event.Action {
	case "destroy":
		// Destroy, not die. Stopping a container emits a die and leaves
		// the container in place; only a destroy means something is
		// being replaced. Without that distinction, pressing stop would
		// leave the row claiming a deploy that is not happening.
		current, deploying := t.states[key]
		if !deploying {
			current = State{Since: now, Last: t.last[key]}
		}
		current.Phase = Recreating
		t.states[key] = current

	case "create":
		// Compose builds the replacement before removing the original:
		// the new container is created first, under a temporary name,
		// and the old one is destroyed afterwards. So a create for a
		// service that already has a container is a replacement, and
		// only a create for one that has none is a first deploy.
		current, deploying := t.states[key]
		if !deploying {
			phase := Creating
			if _, existed := t.last[key]; existed {
				phase = Recreating
			}
			current = State{Phase: phase, Since: now, Last: t.last[key]}
		}
		t.states[key] = current

	case "start":
		current, deploying := t.states[key]
		if !deploying {
			return
		}
		current.Phase = Starting
		// The phase's clock restarts. How long a service has been
		// starting is a different question from how long the whole
		// deploy has taken, and it is the one being asked of the row.
		current.Since = now
		t.states[key] = current
	}
}

// Remember records the containers currently on the host, so a row can still be
// drawn for a service whose container has gone.
func (t *Tracker) Remember(containers []docker.Container) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, container := range containers {
		if key := KeyFor(container); key != "" {
			t.last[key] = container
		}
	}
}

// State returns what is known about a key.
func (t *Tracker) State(key string) (State, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	state, tracked := t.states[key]
	return state, tracked
}

// Clear ends the deploy for a key.
//
// For callers that know more than the clock does — that the service is
// answering its health endpoint, say — and can end the wait on evidence
// instead of on a timer.
func (t *Tracker) Clear(key string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.states, key)
}

// Prune ends deploys that have run out of time.
//
// A started container is given the grace window because without a healthcheck
// there is nothing to wait for; it is long enough that a crash-loop shows
// itself, since a container that dies in three seconds is destroyed and
// recreated before the window closes. A recreate that has produced nothing at
// all is written off after the longer patience: something went wrong that the
// row cannot describe.
func (t *Tracker) Prune(now time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for key, state := range t.states {
		limit := t.patience
		if state.Phase == Starting {
			limit = t.grace
		}
		if now.Sub(state.Since) > limit {
			delete(t.states, key)
		}
	}
}

// Ghosts returns rows for services that are mid-deploy with no container of
// their own in the list.
//
// This is what keeps a row on screen through the moment that would otherwise
// make a deployment invisible: between the destroy and the create there is no
// container to list, so without this the row disappears and comes back as a
// stranger.
func (t *Tracker) Ghosts(live []docker.Container) []docker.Container {
	present := make(map[string]bool, len(live))
	for _, container := range live {
		present[KeyFor(container)] = true
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	var ghosts []docker.Container
	for key, state := range t.states {
		if present[key] || state.Last.ID == "" {
			continue
		}
		ghosts = append(ghosts, state.Last)
	}
	return ghosts
}

// Keys returns every key currently being deployed.
func (t *Tracker) Keys() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	keys := make([]string, 0, len(t.states))
	for key := range t.states {
		keys = append(keys, key)
	}
	return keys
}
