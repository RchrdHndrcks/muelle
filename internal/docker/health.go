package docker

import (
	"context"
	"strings"
	"sync"
)

// Health is a container's healthcheck state.
type Health string

// Possible healthcheck states. HealthNone covers the common case of an image
// that defines no healthcheck at all.
const (
	HealthNone      Health = ""
	HealthStarting  Health = "starting"
	HealthHealthy   Health = "healthy"
	HealthUnhealthy Health = "unhealthy"
)

// Health reports the container's healthcheck state.
//
// The list endpoint does not expose health as its own field; it appends it to
// the human-readable status, as in "Up 2 hours (healthy)". Reading it from
// there costs nothing, whereas fetching it properly would mean inspecting
// every container on every refresh.
func (c Container) Health() Health {
	switch {
	case strings.Contains(c.Status, "(healthy)"):
		return HealthHealthy
	case strings.Contains(c.Status, "(unhealthy)"):
		return HealthUnhealthy
	case strings.Contains(c.Status, "(health: starting)"):
		return HealthStarting
	default:
		return HealthNone
	}
}

// Unhealthy reports whether the container's healthcheck is failing.
func (c Container) Unhealthy() bool { return c.Health() == HealthUnhealthy }

// Restarting reports whether the container is in the middle of a restart.
//
// A container caught in a crash loop spends most of its life here, which is
// what makes this worth surfacing: without it, a service failing every thirty
// seconds looks the same as one that has been up for a month.
func (c Container) Restarting() bool { return c.State == "restarting" }

// NeedsAttention reports whether something about the container looks wrong.
// It is what decides which containers are worth the extra inspect call to
// retrieve a restart count.
func (c Container) NeedsAttention() bool {
	return c.Restarting() || c.Unhealthy() || c.State == "dead"
}

// RestartCounts fetches the restart count for the given containers.
//
// This needs one inspect call per container, so callers pass only the ones
// where the number changes a decision — a container that is restarting or
// unhealthy. Inspecting every container on every refresh to render a column
// that is almost always zero would triple the request rate for nothing.
//
// Containers that cannot be inspected are simply absent from the result: one
// that exits mid-sweep is ordinary, not an error.
func (c *Client) RestartCounts(ctx context.Context, ids []string) map[string]int {
	counts := make(map[string]int, len(ids))
	var (
		mu sync.Mutex
		wg sync.WaitGroup
	)
	sem := make(chan struct{}, statsConcurrency)

	for _, id := range ids {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				return
			}
			detail, err := c.Inspect(ctx, id)
			if err != nil {
				return
			}
			mu.Lock()
			counts[id] = detail.RestartCount
			mu.Unlock()
		}(id)
	}
	wg.Wait()
	return counts
}
