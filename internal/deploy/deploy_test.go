package deploy

import (
	"testing"
	"time"

	"github.com/RchrdHndrcks/muelle/internal/docker"
)

var base = time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)

// A deployment replaces a container: the old one is destroyed and a new one
// created under a different ID. Keying on the Compose service is what lets the
// row survive that, because the service is the thing that persists.
func TestAServiceIsFollowedAcrossTheContainersThatServeIt(t *testing.T) {
	tracker := New(15*time.Second, time.Minute)

	tracker.Observe(event("destroy", "old", "shop", "api"), base)
	tracker.Observe(event("create", "new", "shop", "api"), base.Add(time.Second))

	state, tracked := tracker.State("shop/api")
	if !tracked || state.Phase != Recreating {
		t.Fatalf("got %+v (tracked=%v), want the service recreating", state, tracked)
	}
	if state.Since != base {
		t.Errorf("got since=%v, want the clock to start at the first sign of the deploy", state.Since)
	}
}

// Stopping a container is not deploying it. A stop emits a die and leaves the
// container in place; only a destroy means something is being replaced.
func TestStoppingAContainerIsNotADeployment(t *testing.T) {
	tracker := New(15*time.Second, time.Minute)

	tracker.Observe(event("die", "abc", "shop", "api"), base)

	if _, tracked := tracker.State("shop/api"); tracked {
		t.Error("a container that died but was not removed is not being deployed")
	}
}

func TestStartingMovesTheServiceOnFromRecreating(t *testing.T) {
	tracker := New(15*time.Second, time.Minute)

	tracker.Observe(event("destroy", "old", "shop", "api"), base)
	tracker.Observe(event("start", "new", "shop", "api"), base.Add(2*time.Second))

	state, _ := tracker.State("shop/api")
	if state.Phase != Starting {
		t.Errorf("got %v, want starting once the new container is running", state.Phase)
	}
	// The phase's clock restarts: "starting 0:02" answers a different
	// question from how long the whole deploy has taken.
	if state.Since != base.Add(2*time.Second) {
		t.Errorf("got since=%v, want the clock restarted at the start", state.Since)
	}
}

// A container that says it is healthy has finished deploying, whatever the
// clock says. This is the only end that is a fact rather than an assumption.
func TestAHealthyReportEndsTheDeployment(t *testing.T) {
	tracker := New(15*time.Second, time.Minute)
	tracker.Observe(event("create", "new", "shop", "api"), base)
	tracker.Observe(event("start", "new", "shop", "api"), base)

	tracker.Observe(health("healthy", "new", "shop", "api"), base.Add(3*time.Second))

	if _, tracked := tracker.State("shop/api"); tracked {
		t.Error("a healthy service is not still deploying")
	}
}

// An unhealthy report also ends it: the deploy is over, and the state column
// has its own way of saying the result was bad.
func TestAnUnhealthyReportAlsoEndsTheDeployment(t *testing.T) {
	tracker := New(15*time.Second, time.Minute)
	tracker.Observe(event("create", "new", "shop", "api"), base)
	tracker.Observe(event("start", "new", "shop", "api"), base)

	tracker.Observe(health("unhealthy", "new", "shop", "api"), base.Add(3*time.Second))

	if _, tracked := tracker.State("shop/api"); tracked {
		t.Error("an unhealthy service has finished deploying, badly")
	}
}

// Without a healthcheck there is nothing to wait for, so the grace window is
// the only honest answer. It is long enough that a crash-loop shows itself:
// a container that dies in three seconds goes back to recreating rather than
// ever being called up.
func TestWithoutAHealthcheckTheGraceWindowEndsIt(t *testing.T) {
	tracker := New(15*time.Second, time.Minute)
	tracker.Observe(event("create", "new", "shop", "api"), base)
	tracker.Observe(event("start", "new", "shop", "api"), base)

	tracker.Prune(base.Add(14 * time.Second))
	if _, tracked := tracker.State("shop/api"); !tracked {
		t.Fatal("the window should not have closed yet")
	}

	tracker.Prune(base.Add(16 * time.Second))
	if _, tracked := tracker.State("shop/api"); tracked {
		t.Error("the grace window should have closed")
	}
}

// A deploy that never produces a running container must not leave the row
// saying "recreating" for the rest of the session.
func TestARecreateThatNeverArrivesIsGivenUpOn(t *testing.T) {
	tracker := New(15*time.Second, time.Minute)
	tracker.Observe(event("destroy", "old", "shop", "api"), base)

	tracker.Prune(base.Add(61 * time.Second))

	if _, tracked := tracker.State("shop/api"); tracked {
		t.Error("a deploy that produced nothing in a minute is over")
	}
}

// The grace window is a fallback, not a rule. A caller that knows the service
// is answering — because it probed it — can end the wait early.
func TestClearEndsTheWaitEarly(t *testing.T) {
	tracker := New(15*time.Second, time.Minute)
	tracker.Observe(event("create", "new", "shop", "api"), base)
	tracker.Observe(event("start", "new", "shop", "api"), base)

	tracker.Clear("shop/api")

	if _, tracked := tracker.State("shop/api"); tracked {
		t.Error("a service known to be answering is not still starting")
	}
}

// Containers outside Compose are deployed too — "docker rm" then "docker run"
// under the same name is the same story with fewer labels.
func TestAStandaloneContainerIsFollowedByName(t *testing.T) {
	tracker := New(15*time.Second, time.Minute)

	tracker.Observe(event("destroy", "old", "", ""), base)

	if _, tracked := tracker.State("container/shop-api"); !tracked {
		t.Error("a container with no compose labels should be followed by its name")
	}
}

// The row has to be drawn while no container exists for it, which means
// remembering the one that used to.
func TestTheRowSurvivesWhileNoContainerExists(t *testing.T) {
	tracker := New(15*time.Second, time.Minute)
	tracker.Remember([]docker.Container{{
		ID:     "old",
		Names:  []string{"/shop-api-1"},
		Image:  "shop/api:1.4",
		Labels: map[string]string{docker.LabelProject: "shop", docker.LabelService: "api"},
	}})

	tracker.Observe(event("destroy", "old", "shop", "api"), base)

	ghosts := tracker.Ghosts(nil)
	if len(ghosts) != 1 {
		t.Fatalf("got %d rows, want the vanished service still listed", len(ghosts))
	}
	if ghosts[0].Image != "shop/api:1.4" || ghosts[0].Name() != "shop-api-1" {
		t.Errorf("got %+v, want what the service looked like before it went", ghosts[0])
	}
}

// Once the replacement is in the list, the remembered row must give way to it
// or the service appears twice.
func TestTheRememberedRowStepsAsideForTheRealOne(t *testing.T) {
	tracker := New(15*time.Second, time.Minute)
	live := docker.Container{
		ID:     "new",
		Names:  []string{"/shop-api-1"},
		Labels: map[string]string{docker.LabelProject: "shop", docker.LabelService: "api"},
	}
	tracker.Remember([]docker.Container{{
		ID:     "old",
		Names:  []string{"/shop-api-1"},
		Labels: map[string]string{docker.LabelProject: "shop", docker.LabelService: "api"},
	}})
	tracker.Observe(event("destroy", "old", "shop", "api"), base)

	if ghosts := tracker.Ghosts([]docker.Container{live}); len(ghosts) != 0 {
		t.Errorf("got %d rows, want none while the real container is listed", len(ghosts))
	}
}

func event(action, id, project, service string) docker.Event {
	var e docker.Event
	e.Type = "container"
	e.Action = action
	e.Actor.ID = id
	e.Actor.Attributes = map[string]string{"name": "shop-api"}
	if project != "" {
		e.Actor.Attributes[docker.LabelProject] = project
		e.Actor.Attributes[docker.LabelService] = service
	}
	return e
}

func health(status, id, project, service string) docker.Event {
	e := event("health_status: "+status, id, project, service)
	return e
}

// A service deployed for the first time has nothing to be re-created from, and
// saying "recreating" would claim a replacement that never happened.
func TestAFirstDeployIsCreatingRatherThanRecreating(t *testing.T) {
	tracker := New(15*time.Second, time.Minute)

	tracker.Observe(event("create", "new", "shop", "api"), base)

	state, tracked := tracker.State("shop/api")
	if !tracked || state.Phase != Creating {
		t.Errorf("got %+v (tracked=%v), want creating", state, tracked)
	}
}

// Starting a container that already existed is not a deployment; nothing was
// built or replaced.
func TestAPlainStartIsNotADeployment(t *testing.T) {
	tracker := New(15*time.Second, time.Minute)

	tracker.Observe(event("start", "abc", "shop", "api"), base)

	if _, tracked := tracker.State("shop/api"); tracked {
		t.Error("starting an existing container is not deploying it")
	}
}

// Compose creates the replacement before destroying the original — the new
// container appears first, under a temporary name, and the old one is removed
// afterwards. So a create for a service that already has a container is a
// replacement, and calling it "creating" would tell the user the opposite of
// what is happening.
func TestACreateForAServiceThatAlreadyExistsIsARecreate(t *testing.T) {
	tracker := New(15*time.Second, time.Minute)
	tracker.Remember([]docker.Container{{
		ID:     "old",
		Names:  []string{"/shop-api-1"},
		Labels: map[string]string{docker.LabelProject: "shop", docker.LabelService: "api"},
	}})

	tracker.Observe(event("create", "new", "shop", "api"), base)

	state, tracked := tracker.State("shop/api")
	if !tracked || state.Phase != Recreating {
		t.Errorf("got %+v (tracked=%v), want recreating", state, tracked)
	}
}
