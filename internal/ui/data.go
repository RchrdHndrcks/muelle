package ui

import (
	"context"
	"strings"
	"time"

	"github.com/RchrdHndrcks/muelle/internal/compose"
	"github.com/RchrdHndrcks/muelle/internal/docker"
)

// refreshTimeout bounds a refresh cycle. A daemon that stops answering must
// not leave the list stale forever with no indication why.
const refreshTimeout = 20 * time.Second

// diskUsageInterval is how often /system/df is measured. That endpoint walks
// the storage driver and can take seconds on a host with many layers, so it
// runs far less often than the container list — disk totals move slowly
// enough that a stale reading costs nothing.
const diskUsageInterval = 60 * time.Second

// diskUsageTimeout is generous for the same reason.
const diskUsageTimeout = 90 * time.Second

// containersLoaded carries the result of a container list refresh.
type containersLoaded struct {
	containers []docker.Container
	projects   []compose.Project
	err        error
}

func (e containersLoaded) apply(a *App) {
	a.refreshing = false
	if e.err != nil {
		a.setError("refresh failed: %v", e.err)
		return
	}
	a.containers = e.containers
	a.projects = e.projects
	// A failed refresh leaves the previous data on screen; a successful one
	// clears the error that reported it.
	if a.status.isError && strings.HasPrefix(a.status.text, "refresh failed") {
		a.status = status{}
	}
}

// imagesLoaded carries an image list refresh.
type imagesLoaded struct {
	images []docker.Image
	err    error
}

func (e imagesLoaded) apply(a *App) {
	if e.err != nil {
		a.setError("images: %v", e.err)
		return
	}
	a.images = e.images
}

// volumesLoaded carries a volume list refresh.
type volumesLoaded struct {
	volumes []docker.Volume
	err     error
}

func (e volumesLoaded) apply(a *App) {
	if e.err != nil {
		a.setError("volumes: %v", e.err)
		return
	}
	a.volumes = e.volumes
}

// statsLoaded carries a round of resource samples.
type statsLoaded struct{ stats map[string]docker.Stat }

func (e statsLoaded) apply(a *App) {
	a.stats = e.stats

	// Aggregate for the host panel. Docker publishes no host-level
	// utilisation, so the sum across containers is the closest honest
	// figure, and the panel labels it as such.
	var (
		cpu float64
		mem uint64
	)
	for _, stat := range e.stats {
		cpu += stat.CPUPercent
		mem += stat.MemUsage
	}
	a.metrics.CPUPercent = cpu
	a.metrics.MemBytes = mem
}

// hostInfoLoaded carries a refresh of the daemon's host information.
type hostInfoLoaded struct {
	info docker.Info
	err  error
}

func (e hostInfoLoaded) apply(a *App) {
	if e.err != nil {
		return
	}
	a.metrics.Info = e.info
}

// diskUsageLoaded carries a disk usage measurement.
type diskUsageLoaded struct {
	usage docker.DiskUsage
	err   error
}

func (e diskUsageLoaded) apply(a *App) {
	a.measuringDisk = false
	if e.err != nil {
		// Leave the previous measurement on screen and try again on the
		// next cycle rather than blanking the line.
		return
	}
	a.metrics.Disk = e.usage
	a.metrics.DiskKnown = true
	a.diskMeasuredAt = time.Now()
}

// actionDone reports the outcome of a lifecycle action.
type actionDone struct {
	message string
	err     error
}

func (e actionDone) apply(a *App) {
	if e.err != nil {
		a.setError("%s", e.err.Error())
		return
	}
	a.setStatus("%s", e.message)
}

// logLines carries a batch of streamed log lines.
//
// The token identifies which stream produced them, so lines still in flight
// when the user switches containers are discarded rather than mixed into the
// new container's output.
type logLines struct {
	token int
	lines []docker.LogLine
}

func (e logLines) apply(a *App) {
	if e.token != a.logToken {
		return
	}
	a.logs.Append(e.lines...)
	a.logStreamed = true
}

// logFailed reports that a log stream could not be opened or ended badly.
type logFailed struct {
	token int
	err   error
}

func (e logFailed) apply(a *App) {
	if e.token != a.logToken {
		return
	}
	a.setError("logs: %v", e.err)
}

// inspectLoaded carries a container's formatted inspect output.
type inspectLoaded struct {
	title string
	lines []string
	err   error
}

func (e inspectLoaded) apply(a *App) {
	if e.err != nil {
		a.setError("inspect: %v", e.err)
		return
	}
	a.inspect = e.lines
	a.inspectTitle = e.title
	a.inspectPager = NewPager(false)
	a.mode = ModeInspect
}

// refresh reloads the data for the current view in the background.
//
// Only the active view's data is fetched: polling images and volumes while the
// user is watching containers would triple the request rate for information
// nobody is looking at.
func (a *App) refresh(ctx context.Context) {
	if a.refreshing {
		return
	}
	a.refreshing = true

	showAll := a.showAll
	composeDirs := a.config.ComposeDirs
	wantStats := a.config.Stats && a.view == ViewContainers

	go func() {
		fetchCtx, cancel := context.WithTimeout(ctx, refreshTimeout)
		defer cancel()

		containers, err := a.docker.Containers(fetchCtx, showAll)
		if err != nil {
			a.post(containersLoaded{err: err})
			return
		}
		// Compose projects need every container, including stopped ones,
		// or a partially-running project would look fully up.
		forProjects := containers
		if !showAll {
			if all, allErr := a.docker.Containers(fetchCtx, true); allErr == nil {
				forProjects = all
			}
		}
		projects := compose.Merge(
			compose.FromContainers(forProjects),
			compose.Discover(composeDirs),
		)
		a.post(containersLoaded{containers: containers, projects: projects})

		if wantStats {
			var running []string
			for _, c := range containers {
				if c.Running() {
					running = append(running, c.ID)
				}
			}
			if len(running) > 0 {
				a.post(statsLoaded{stats: a.docker.StatsFor(fetchCtx, running)})
			}
		}
	}()

	if a.showSystem {
		go func() {
			fetchCtx, cancel := context.WithTimeout(ctx, refreshTimeout)
			defer cancel()
			info, err := a.docker.SystemInfo(fetchCtx)
			a.post(hostInfoLoaded{info: info, err: err})
		}()
		if time.Since(a.diskMeasuredAt) > diskUsageInterval && !a.measuringDisk {
			a.measuringDisk = true
			go func() {
				fetchCtx, cancel := context.WithTimeout(ctx, diskUsageTimeout)
				defer cancel()
				usage, err := a.docker.DiskUsage(fetchCtx)
				a.post(diskUsageLoaded{usage: usage, err: err})
			}()
		}
	}

	switch a.view {
	case ViewImages:
		go func() {
			fetchCtx, cancel := context.WithTimeout(ctx, refreshTimeout)
			defer cancel()
			images, err := a.docker.Images(fetchCtx)
			a.post(imagesLoaded{images: images, err: err})
		}()
	case ViewVolumes:
		go func() {
			fetchCtx, cancel := context.WithTimeout(ctx, refreshTimeout)
			defer cancel()
			volumes, err := a.docker.Volumes(fetchCtx)
			a.post(volumesLoaded{volumes: volumes, err: err})
		}()
	}
}

// LoadOnce fetches everything synchronously, for the headless dump mode where
// there is no event loop to deliver results to.
func (a *App) LoadOnce(ctx context.Context) error {
	// Fetch everything once: projects need stopped containers to report an
	// accurate status, while the list itself honours the running-only
	// toggle.
	all, err := a.docker.Containers(ctx, true)
	if err != nil {
		return err
	}
	a.projects = compose.Merge(
		compose.FromContainers(all),
		compose.Discover(a.config.ComposeDirs),
	)

	containers := all
	if !a.showAll {
		containers = make([]docker.Container, 0, len(all))
		for _, c := range all {
			if c.Running() {
				containers = append(containers, c)
			}
		}
	}
	a.containers = containers
	if images, err := a.docker.Images(ctx); err == nil {
		a.images = images
	}
	if volumes, err := a.docker.Volumes(ctx); err == nil {
		a.volumes = volumes
	}
	if version, err := a.docker.Ping(ctx); err == nil {
		a.version = version
	}
	if a.config.Stats {
		var running []string
		for _, c := range containers {
			if c.Running() {
				running = append(running, c.ID)
			}
		}
		if len(running) > 0 {
			statsLoaded{stats: a.docker.StatsFor(ctx, running)}.apply(a)
		}
	}
	if a.showSystem {
		if info, err := a.docker.SystemInfo(ctx); err == nil {
			a.metrics.Info = info
		}
		if usage, err := a.docker.DiskUsage(ctx); err == nil {
			a.metrics.Disk = usage
			a.metrics.DiskKnown = true
		}
	}
	return nil
}

// SetShowAll controls whether stopped containers are listed.
func (a *App) SetShowAll(showAll bool) { a.showAll = showAll }

// SetView switches the active view, for dump mode.
func (a *App) SetView(view View) { a.view = view }

// runAction performs a lifecycle call in the background and reports the
// outcome. Running it off the loop keeps the UI responsive while the daemon
// takes its time stopping a container.
func (a *App) runAction(ctx context.Context, description string, action func(context.Context) error) {
	go func() {
		actionCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		defer cancel()

		err := action(actionCtx)
		if err != nil && docker.NotModified(err) {
			// The container was already in the requested state, which
			// is what the user wanted.
			err = nil
		}
		a.post(actionDone{message: description, err: err})
		a.post(refreshRequested{})
	}()
}

// refreshRequested asks the loop to reload data, used after an action changes
// something so the list updates without waiting for the next tick.
type refreshRequested struct{}

func (refreshRequested) apply(a *App) {
	a.refreshing = false
	a.refresh(context.Background())
}

// filteredContainers returns the containers matching the current filter.
func (a *App) filteredContainers() []docker.Container {
	if a.filter == "" {
		return a.containers
	}
	needle := strings.ToLower(a.filter)
	matched := make([]docker.Container, 0, len(a.containers))
	for _, c := range a.containers {
		haystack := strings.ToLower(c.Name() + " " + c.Image + " " + c.Project() + " " + c.State)
		if strings.Contains(haystack, needle) {
			matched = append(matched, c)
		}
	}
	return matched
}

// filteredProjects returns the projects matching the current filter.
func (a *App) filteredProjects() []compose.Project {
	if a.filter == "" {
		return a.projects
	}
	needle := strings.ToLower(a.filter)
	matched := make([]compose.Project, 0, len(a.projects))
	for _, p := range a.projects {
		if strings.Contains(strings.ToLower(p.Name), needle) {
			matched = append(matched, p)
		}
	}
	return matched
}

// filteredImages returns the images matching the current filter.
func (a *App) filteredImages() []docker.Image {
	if a.filter == "" {
		return a.images
	}
	needle := strings.ToLower(a.filter)
	matched := make([]docker.Image, 0, len(a.images))
	for _, i := range a.images {
		if strings.Contains(strings.ToLower(i.Tag()), needle) {
			matched = append(matched, i)
		}
	}
	return matched
}

// filteredVolumes returns the volumes matching the current filter.
func (a *App) filteredVolumes() []docker.Volume {
	if a.filter == "" {
		return a.volumes
	}
	needle := strings.ToLower(a.filter)
	matched := make([]docker.Volume, 0, len(a.volumes))
	for _, v := range a.volumes {
		if strings.Contains(strings.ToLower(v.Name), needle) {
			matched = append(matched, v)
		}
	}
	return matched
}
