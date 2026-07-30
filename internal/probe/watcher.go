package probe

import (
	"context"
	"sync"
	"time"

	"github.com/RchrdHndrcks/muelle/internal/docker"
)

// Inspector is the part of the Docker client a Watcher needs.
type Inspector interface {
	Inspect(ctx context.Context, id string) (docker.ContainerDetail, error)
}

// concurrency bounds how many probes are in flight at once, for the same
// reason the stats sweep is bounded: a host with fifty containers should not
// open fifty sockets on every refresh.
const concurrency = 8

// Watcher probes the containers that asked to be probed, once per refresh.
//
// The environment variable is only readable through inspect, which the rest of
// muelle avoids doing per refresh on purpose. It does not have to: a
// container's environment is fixed when it is created, and a recreated
// container arrives with a new ID. So each ID is inspected once and the answer
// — endpoint, or "this one does not opt in" — is kept for as long as the
// container exists.
type Watcher struct {
	prober Prober

	mu      sync.Mutex
	targets map[string]target
}

// target is what one inspect taught us about a container.
type target struct {
	spec Spec
	// address is the container's own address on its network, used when the
	// port it serves on is not published. It is the one part of this that
	// can go stale, since a container can come back on a different address.
	address string
	// probe is false for the great majority of containers, which never set
	// the variable, and for those whose value could not be read.
	probe bool
}

// NewWatcher builds a watcher whose probes give up after timeout.
func NewWatcher(timeout time.Duration) *Watcher {
	return &Watcher{prober: New(timeout), targets: make(map[string]target)}
}

// Sweep probes every running container that opts in and returns the results by
// container ID.
//
// Containers that have gone are forgotten, so what the watcher remembers stays
// proportional to what is actually on the host.
func (w *Watcher) Sweep(ctx context.Context, inspector Inspector, containers []docker.Container) map[string]Result {
	w.forgetAllBut(containers)

	results := make(map[string]Result)
	var (
		mu sync.Mutex
		wg sync.WaitGroup
	)
	sem := make(chan struct{}, concurrency)

	for _, container := range containers {
		if !container.Running() {
			continue
		}
		found, known := w.target(ctx, inspector, container)
		if !known || !found.probe {
			continue
		}
		url, reachable := Resolve(found.spec, container, found.address)
		if !reachable {
			continue
		}

		wg.Add(1)
		go func(id, url string) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				return
			}
			result := w.prober.Do(ctx, url)
			// A probe that never reached the service may have been
			// sent to an address the container no longer has. Drop
			// what we know so the next sweep looks again. An answer,
			// even a 503, proves the address is current.
			if result.Err != nil {
				w.forget(id)
			}
			mu.Lock()
			results[id] = result
			mu.Unlock()
		}(container.ID, url)
	}
	wg.Wait()
	return results
}

// target returns what is known about a container, inspecting it the first time.
func (w *Watcher) target(ctx context.Context, inspector Inspector, container docker.Container) (target, bool) {
	w.mu.Lock()
	known, cached := w.targets[container.ID]
	w.mu.Unlock()
	if cached {
		return known, true
	}

	detail, err := inspector.Inspect(ctx, container.ID)
	if err != nil {
		// Not cached: a container that could not be inspected this time
		// is ordinary — it may have been mid-start — and deserves
		// another chance rather than being written off for its lifetime.
		return target{}, false
	}

	found := target{address: address(detail)}
	if spec, err := Parse(detail.Env()[EnvVar]); err == nil {
		found.spec, found.probe = spec, true
	}

	w.mu.Lock()
	w.targets[container.ID] = found
	w.mu.Unlock()
	return found, true
}

// address returns the container's address on the first network that has one.
//
// Which network is not a meaningful choice: a container on several networks is
// reachable on all of them from a host that can reach any of them.
func address(detail docker.ContainerDetail) string {
	for _, network := range detail.NetworkSettings.Networks {
		if network.IPAddress != "" {
			return network.IPAddress
		}
	}
	return ""
}

func (w *Watcher) forget(id string) {
	w.mu.Lock()
	delete(w.targets, id)
	w.mu.Unlock()
}

// forgetAllBut drops everything remembered about containers that no longer
// exist.
func (w *Watcher) forgetAllBut(containers []docker.Container) {
	live := make(map[string]bool, len(containers))
	for _, container := range containers {
		live[container.ID] = true
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	for id := range w.targets {
		if !live[id] {
			delete(w.targets, id)
		}
	}
}

// known reports how many containers the watcher is remembering, for tests.
func (w *Watcher) known() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.targets)
}
