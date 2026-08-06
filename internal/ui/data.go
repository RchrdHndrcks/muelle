package ui

import (
	"context"
	"strings"
	"time"

	"github.com/RchrdHndrcks/muelle/internal/autodeploy"
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
	// imageUsage is computed from every container, running or not, so the
	// images view can tell which images are removable.
	imageUsage map[string]int
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
	// Marks are keyed by ID exactly so they survive this replacement; what
	// must not survive is a mark whose container has left the list.
	a.pruneMarks()
	// Remembered so a row can still be drawn for a service whose container
	// has been destroyed and not yet replaced.
	if a.deploys != nil {
		a.deploys.Remember(e.containers)
		a.deploys.Prune(time.Now())
	}
	a.placeSelection()
	if e.imageUsage != nil {
		a.imageUsage = e.imageUsage
	}
	// The stats streams follow the list: a container that started gets a
	// stream, one that stopped loses both its stream and its last sample.
	a.syncStatStreams()
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

// networksLoaded carries a network list refresh.
type networksLoaded struct {
	networks []docker.Network
	err      error
}

func (e networksLoaded) apply(a *App) {
	if e.err != nil {
		a.setError("networks: %v", e.err)
		return
	}
	a.networks = e.networks
}

// statsLoaded carries a full round of resource samples, from the one-shot
// path dump mode uses; the live app receives statSampled events instead.
type statsLoaded struct{ stats map[string]docker.Stat }

func (e statsLoaded) apply(a *App) {
	a.stats = e.stats
	a.recomputeStatTotals()
}

// statSampled carries one decoded sample from a container's stats stream.
//
// Samples arrive one container at a time rather than as a round, so this
// updates a single entry — and drops a sample whose container is no longer
// listed as running, which happens when one was already decoded while its
// stream was being torn down.
type statSampled struct {
	id   string
	stat docker.Stat
}

func (e statSampled) apply(a *App) {
	for _, c := range a.containers {
		if c.ID != e.id {
			continue
		}
		if !c.Running() {
			return
		}
		a.stats[e.id] = e.stat
		a.recomputeStatTotals()
		return
	}
}

// recomputeStatTotals refreshes the host panel's aggregate figures. Docker
// publishes no host-level utilisation, so the sum across containers is the
// closest honest figure, and the panel labels it as such.
func (a *App) recomputeStatTotals() {
	var (
		cpu float64
		mem uint64
	)
	for _, stat := range a.stats {
		cpu += stat.CPUPercent
		mem += stat.MemUsage
	}
	a.metrics.CPUPercent = cpu
	a.metrics.MemBytes = mem
}

// syncStatStreams reconciles the stats streams with the containers now
// running, and drops the samples of those that no longer are. Driven by the
// refreshed container list rather than by a timer of its own, so which
// streams are open can never disagree with what is on screen.
func (a *App) syncStatStreams() {
	if a.statStreams == nil {
		return
	}
	running := make(map[string]bool, len(a.containers))
	ids := make([]string, 0, len(a.containers))
	for _, c := range a.containers {
		if c.Running() {
			running[c.ID] = true
			ids = append(ids, c.ID)
		}
	}
	changed := false
	for id := range a.stats {
		if !running[id] {
			delete(a.stats, id)
			changed = true
		}
	}
	if changed {
		a.recomputeStatTotals()
	}
	// Without a run there is nothing to tie a stream's life to: dump mode
	// and unit tests apply container lists with no event loop behind them,
	// and must not leak connections that nothing will ever close.
	if a.streamCtx == nil {
		return
	}
	a.statStreams.Sync(a.streamCtx, ids)
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

// restartCountsLoaded carries restart counts for the containers that looked
// worth inspecting.
type restartCountsLoaded struct{ counts map[string]int }

func (e restartCountsLoaded) apply(a *App) { a.restartCounts = e.counts }

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

	// The auto-deploy daemon is a separate process; its state file is the
	// only channel it reports through, so it is re-read on the same tick as
	// everything else.
	a.refreshDeployState()

	showAll := a.showAll
	composeDirs := a.config.ComposeDirs

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
		a.post(containersLoaded{
			containers: containers,
			projects:   projects,
			imageUsage: docker.ImageUsage(forProjects),
		})

		// Restart counts need an inspect each, so only containers that
		// already look unwell are worth the call. On a healthy host this
		// list is empty and costs nothing.
		var unwell []string
		for _, c := range containers {
			if c.NeedsAttention() {
				unwell = append(unwell, c.ID)
			}
		}
		if len(unwell) > 0 {
			a.post(restartCountsLoaded{counts: a.docker.RestartCounts(fetchCtx, unwell)})
		}

		// Probing runs against the containers themselves rather than the
		// daemon, so it neither waits for nor delays anything above.
		//
		// Stats need no fetch here at all: each running container has a
		// persistent stream posting samples as the daemon produces them,
		// and the loop reconciles those streams when the container list
		// above is applied.
		if a.probes != nil {
			a.post(probesSwept{results: a.probes.Sweep(fetchCtx, a.docker, containers)})
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
	case ViewNetworks:
		go func() {
			fetchCtx, cancel := context.WithTimeout(ctx, refreshTimeout)
			defer cancel()
			networks, err := a.docker.Networks(fetchCtx)
			a.post(networksLoaded{networks: networks, err: err})
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
	a.pruneMarks()
	a.placeSelection()
	a.imageUsage = docker.ImageUsage(all)
	if images, err := a.docker.Images(ctx); err == nil {
		a.images = images
	}
	if volumes, err := a.docker.Volumes(ctx); err == nil {
		a.volumes = volumes
	}
	if networks, err := a.docker.Networks(ctx); err == nil {
		a.networks = networks
	}
	if version, err := a.docker.Ping(ctx); err == nil {
		a.version = version
	}
	if a.deployStatePath != "" {
		a.deployState = autodeploy.LoadState(a.deployStatePath)
	}
	if a.probes != nil {
		probesSwept{results: a.probes.Sweep(ctx, a.docker, containers)}.apply(a)
	}
	// Stats are sampled one-shot here rather than streamed: a program that
	// renders a single frame and exits has no later moment for a stream's
	// samples to arrive in.
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

// SetBuild records which build of muelle is running, for the header.
func (a *App) SetBuild(build string) { a.build = build }

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

// filteredContainers returns the containers matching the current filter, in
// the current sort order.
func (a *App) filteredContainers() []docker.Container {
	return a.sortOrder(a.matching(a.withGhosts()))
}

// matching narrows a list of containers by the active filter.
func (a *App) matching(containers []docker.Container) []docker.Container {
	if a.filter == "" {
		return containers
	}
	needle := strings.ToLower(a.filter)
	matched := make([]docker.Container, 0, len(containers))
	for _, c := range containers {
		haystack := strings.ToLower(c.Name() + " " + c.Image + " " + c.Project() + " " + c.State)
		if strings.Contains(haystack, needle) {
			matched = append(matched, c)
		}
	}
	return matched
}

// sortedContainers returns everything the view would list, in order, before
// the filter narrows it. Grouping needs this: which application a container
// belongs to must not depend on what is being searched for.
func (a *App) sortedContainers() []docker.Container {
	return a.sortOrder(a.withGhosts())
}

// sortOrder applies the chosen ordering.
func (a *App) sortOrder(containers []docker.Container) []docker.Container {
	if a.sortKey == SortDefault {
		// The daemon response is already in this order, so sorting it
		// again would only cost a copy.
		return containers
	}
	// Copy before sorting: the caller's slice may be a.containers itself,
	// and sorting it in place would reorder the model behind the view.
	ordered := make([]docker.Container, len(containers))
	copy(ordered, containers)
	SortContainers(ordered, a.sortKey, a.stats)
	return ordered
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

// filteredImages returns the images matching the current filter and, when the
// unused-only toggle is on, only those no container references.
func (a *App) filteredImages() []docker.Image {
	needle := strings.ToLower(a.filter)
	matched := make([]docker.Image, 0, len(a.images))

	for _, image := range a.images {
		if a.unusedImagesOnly && a.imageInUse(image) {
			continue
		}
		if needle != "" && !strings.Contains(strings.ToLower(image.Tag()), needle) {
			continue
		}
		matched = append(matched, image)
	}
	return matched
}

// imageInUse reports whether any container references the image.
//
// Containers are counted whether running or not: a stopped container still
// pins its image, and calling it unused would offer a deletion the daemon
// refuses.
func (a *App) imageInUse(image docker.Image) bool {
	return a.imageUsage[image.ID] > 0
}

// unusedImages returns the images no container references, with the total they
// would free.
func (a *App) unusedImages() (count int, reclaimable int64) {
	for _, image := range a.images {
		if !a.imageInUse(image) {
			count++
			reclaimable += image.Reclaimable()
		}
	}
	return count, reclaimable
}

// filteredNetworks returns the networks matching the current filter.
func (a *App) filteredNetworks() []docker.Network {
	if a.filter == "" {
		return a.networks
	}
	needle := strings.ToLower(a.filter)
	matched := make([]docker.Network, 0, len(a.networks))
	for _, n := range a.networks {
		if strings.Contains(strings.ToLower(n.Name+" "+n.Driver+" "+n.Project()), needle) {
			matched = append(matched, n)
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
