package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/RchrdHndrcks/muelle/internal/deploy"
	"github.com/RchrdHndrcks/muelle/internal/docker"
	"github.com/RchrdHndrcks/muelle/internal/probe"
)

// A deployment is measured in seconds, which is exactly the range
// FormatDuration collapses into "just now". Watching a number climb is most of
// what tells a user the thing is progressing rather than stuck.
func TestFormatElapsedCountsInSeconds(t *testing.T) {
	cases := map[time.Duration]string{
		0:                              "0:00",
		3 * time.Second:                "0:03",
		74 * time.Second:               "1:14",
		10 * time.Minute:               "10:00",
		-1 * time.Second:               "0:00",
		90*time.Minute + 5*time.Second: "90:05",
	}

	for given, want := range cases {
		if got := FormatElapsed(given); got != want {
			t.Errorf("%v: got %q, want %q", given, got, want)
		}
	}
}

// The whole point: while a service is being replaced its row says so, instead
// of vanishing and coming back as a stranger.
func TestStateCellShowsTheDeploymentPhase(t *testing.T) {
	app := newTestApp(t)
	container := docker.Container{
		ID:     "abc",
		Names:  []string{"/shop-api-1"},
		State:  "running",
		Status: "Up 2 hours",
		Labels: map[string]string{docker.LabelProject: "shop", docker.LabelService: "api"},
	}
	app.deploys = deploy.New(15*time.Second, time.Minute)
	app.deploys.Observe(destroyOf("shop", "api"), time.Now().Add(-3*time.Second))

	cell := app.stateCell(container)

	if !strings.Contains(cell, "recreating") {
		t.Errorf("got %q, want the phase", cell)
	}
	if !strings.Contains(cell, "0:03") {
		t.Errorf("got %q, want how long it has been in that phase", cell)
	}
}

// A container nobody is deploying looks exactly as it did before.
func TestStateCellIsUntouchedWithNoDeployment(t *testing.T) {
	app := newTestApp(t)
	app.deploys = deploy.New(15*time.Second, time.Minute)
	container := docker.Container{ID: "abc", State: "running", Status: "Up 2 hours"}

	if cell := app.stateCell(container); !strings.Contains(cell, "up") {
		t.Errorf("got %q, want the ordinary state", cell)
	}
}

// Between the destroy and the create there is no container to list. Without a
// row held open, that gap is exactly what makes a deployment invisible.
func TestTheListKeepsARowForAServiceBeingReplaced(t *testing.T) {
	app := newTestApp(t)
	app.deploys = deploy.New(15*time.Second, time.Minute)
	app.deploys.Remember([]docker.Container{{
		ID:     "old",
		Names:  []string{"/shop-api-1"},
		Image:  "shop/api:1.4",
		Labels: map[string]string{docker.LabelProject: "shop", docker.LabelService: "api"},
	}})
	app.deploys.Observe(destroyOf("shop", "api"), time.Now())
	// The daemon no longer lists it.
	app.containers = nil

	rows := app.filteredContainers()

	if len(rows) != 1 || rows[0].Name() != "shop-api-1" {
		t.Fatalf("got %+v, want the service still listed while it is replaced", rows)
	}
}

// Events arrive on a stream goroutine but are folded in on the one that owns
// the model, the same way every other asynchronous result is.
func TestAnObservedEventReachesTheTracker(t *testing.T) {
	app := newTestApp(t)
	app.deploys = deploy.New(15*time.Second, time.Minute)

	deployObserved{event: destroyOf("shop", "api")}.apply(app)

	if _, tracked := app.deploys.State("shop/api"); !tracked {
		t.Error("the event should have been folded into the tracker")
	}
}

// The grace window is a guess. A probe that answered is not, so it ends the
// wait as soon as it lands.
func TestAHealthyProbeEndsTheDeployment(t *testing.T) {
	app := newTestApp(t)
	app.deploys = deploy.New(15*time.Second, time.Minute)
	app.containers = []docker.Container{{
		ID:     "abc",
		Names:  []string{"/shop-api-1"},
		Labels: map[string]string{docker.LabelProject: "shop", docker.LabelService: "api"},
	}}
	app.deploys.Observe(destroyOf("shop", "api"), time.Now())
	app.deploys.Observe(startOf("shop", "api"), time.Now())

	probesSwept{results: map[string]probe.Result{"abc": {Healthy: true}}}.apply(app)

	if _, tracked := app.deploys.State("shop/api"); tracked {
		t.Error("a service answering its health endpoint has finished deploying")
	}
}

func destroyOf(project, service string) docker.Event {
	return composeEvent("destroy", project, service)
}

func startOf(project, service string) docker.Event {
	return composeEvent("start", project, service)
}

func composeEvent(action, project, service string) docker.Event {
	var e docker.Event
	e.Type = "container"
	e.Action = action
	e.Actor.ID = "abc"
	e.Actor.Attributes = map[string]string{
		"name":              project + "-" + service + "-1",
		docker.LabelProject: project,
		docker.LabelService: service,
	}
	return e
}
