package ui

import (
	"time"

	"github.com/RchrdHndrcks/muelle/internal/config"
	"github.com/RchrdHndrcks/muelle/internal/deploy"
	"github.com/RchrdHndrcks/muelle/internal/docker"
	"github.com/RchrdHndrcks/muelle/internal/probe"
)

// probeTimeout is how long a health endpoint has to answer.
//
// Short on purpose: this runs inside the refresh cycle, against a service on
// the same host. An endpoint that needs longer than this to say it is alive is
// telling you something either way.
const probeTimeout = 2 * time.Second

// newProbeWatcher builds the watcher, or nil when probing is switched off.
func newProbeWatcher(cfg config.Config) *probe.Watcher {
	if !cfg.HealthProbe {
		return nil
	}
	return probe.NewWatcher(probeTimeout)
}

// probesSwept carries the results of a probe sweep.
type probesSwept struct {
	results map[string]probe.Result
}

func (e probesSwept) apply(a *App) {
	// Replaced wholesale rather than merged: a container that has gone must
	// not leave its last verdict behind for whatever takes its place in the
	// list.
	a.probeResults = e.results

	// A service that answered its health endpoint has finished deploying.
	// The grace window is a guess made when there is nothing better; this is
	// evidence, so it ends the wait as soon as it lands.
	if a.deploys == nil {
		return
	}
	for _, container := range a.containers {
		if result, probed := e.results[container.ID]; probed && result.Healthy {
			a.deploys.Clear(deploy.KeyFor(container))
		}
	}
}

// probeHealth reports what the last sweep found for a container, and whether
// it was probed at all.
//
// A container that opted in outranks the image's own healthcheck. Setting the
// variable is a deliberate instruction to muelle, usually made by someone who
// cannot add a HEALTHCHECK to the image — or does not trust the one that is
// there.
func (a *App) probeHealth(container docker.Container) (docker.Health, bool) {
	result, probed := a.probeResults[container.ID]
	if !probed {
		return docker.HealthNone, false
	}
	if result.Healthy {
		return docker.HealthHealthy, true
	}
	return docker.HealthUnhealthy, true
}
