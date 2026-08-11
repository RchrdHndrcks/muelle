package ui

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/RchrdHndrcks/muelle/internal/autodeploy"
	"github.com/RchrdHndrcks/muelle/internal/compose"
	"github.com/RchrdHndrcks/muelle/internal/config"
	"github.com/RchrdHndrcks/muelle/internal/deploy"
	"github.com/RchrdHndrcks/muelle/internal/docker"
	"github.com/RchrdHndrcks/muelle/internal/probe"
	"github.com/RchrdHndrcks/muelle/internal/registry"
	"github.com/RchrdHndrcks/muelle/internal/tui"
)

// View is one of the top-level lists.
type View int

// The available views, in tab order.
const (
	ViewContainers View = iota
	ViewCompose
	ViewImages
	ViewVolumes
	ViewNetworks
	ViewEvents
	viewCount
)

// Title returns the view's name for the tab bar.
func (v View) Title() string {
	switch v {
	case ViewContainers:
		return "Containers"
	case ViewCompose:
		return "Compose"
	case ViewImages:
		return "Images"
	case ViewVolumes:
		return "Volumes"
	case ViewNetworks:
		return "Networks"
	case ViewEvents:
		return "Events"
	default:
		return "?"
	}
}

// Mode is what currently has the screen and the keyboard.
type Mode int

const (
	// ModeList is the normal state: a view's list is showing.
	ModeList Mode = iota
	// ModeLogs is the full-screen log viewer.
	ModeLogs
	// ModeInspect is the full-screen JSON viewer.
	ModeInspect
	// ModeHelp is the key reference.
	ModeHelp
	// ModeProcesses lists the processes inside a container.
	ModeProcesses
	// ModeHistory is the full-screen image layer history viewer.
	ModeHistory
)

// App holds all mutable state and the event loop.
//
// One goroutine owns this struct. Everything asynchronous — API calls, log
// streams, the refresh ticker — delivers results as events on a channel, so
// there are no locks around the model and no chance of a half-updated frame.
type App struct {
	config config.Config
	docker *docker.Client
	screen *tui.Screen
	runner *Runner

	view View
	mode Mode

	// Data, replaced wholesale by refreshes.
	containers []docker.Container
	projects   []compose.Project
	images     []docker.Image
	// imageUsage counts containers per image ID. Derived from the container
	// list because the images endpoint reports -1 for every image.
	imageUsage map[string]int
	// unusedImagesOnly narrows the images view to removal candidates.
	unusedImagesOnly bool
	// updates remembers, per image reference, whether its registry holds a
	// newer version of the tag. Keyed by reference rather than image ID so a
	// list refresh, which replaces the image structs, does not wipe results
	// that took a round trip per image to earn.
	updates map[string]registry.Verdict
	// updateChecker answers those questions; an interface so tests need no
	// network.
	updateChecker imageChecker
	// checkingUpdates keeps a slow sweep from being started twice.
	checkingUpdates bool
	volumes         []docker.Volume
	networks        []docker.Network
	stats           map[string]docker.Stat
	// statStreams holds one persistent stats connection per running
	// container, reconciled against each refreshed container list. Nil when
	// the stats columns are switched off. Owned, like everything else here,
	// by the loop goroutine; the streams themselves only post events.
	statStreams *docker.StatsStreamer
	// streamCtx is what every stats stream derives from, so cancelling the
	// app's run tears them all down. Set once at the top of Run; nil in dump
	// mode and in tests, where no stream is ever opened.
	streamCtx context.Context
	// cpuHistory remembers each container's recent CPU readings, which the
	// stats map — holding only the latest sample per container — cannot. It
	// feeds the sparkline in the CPU column; see sparkline.go for why a row
	// wants one.
	cpuHistory map[string]*history
	// restartCounts is populated only for containers that look unwell;
	// see docker.Client.RestartCounts for why it is not fetched for all.
	restartCounts map[string]int
	// probes asks containers that opt in whether they are well; nil when
	// the feature is switched off.
	probes *probe.Watcher
	// probeResults is what the last sweep found, by container ID.
	probeResults map[string]probe.Result
	// deploys follows services through being replaced, so a row can say
	// what is happening to it rather than vanishing.
	deploys *deploy.Tracker
	// daemonEvents is the timeline behind the events view, fed by the same
	// stream the deploy tracker follows.
	daemonEvents *EventRing
	// deployState is the auto-deploy daemon's last word, re-read from its
	// state file on the refresh tick. The TUI only ever reads it: exactly
	// one process — the headless daemon — deploys automatically, and this
	// view is how you watch it without being able to race it.
	deployState autodeploy.State
	// deployStatePath is where that file lives, or "" when unknown (no
	// config path), which turns the whole feature's display off.
	deployStatePath string

	// Per-view selection and scroll, kept separately so switching tabs
	// returns you to where you were.
	selection [viewCount]int
	offset    [viewCount]int

	// showAll toggles between running-only and every container.
	showAll bool
	// grouped gathers the list under one heading per application.
	grouped bool
	// collapsed names the applications folded away.
	collapsed map[string]bool
	// selectionPlaced records that the cursor has been put somewhere
	// sensible for the first time; see placeSelection.
	selectionPlaced bool
	// metrics feeds the host summary panel.
	metrics HostMetrics
	// showSystem controls whether that panel is drawn.
	showSystem bool
	// diskMeasuredAt and measuringDisk pace the expensive /system/df call.
	diskMeasuredAt time.Time
	measuringDisk  bool
	// filter narrows the current list by substring.
	filter string
	// marked is the containers view's multi-select, keyed by container ID
	// so a mark survives refresh; see marks.go for the whole story.
	marked map[string]bool
	// sortKey orders the container list.
	sortKey SortKey
	// persist records a preference that should outlive the session. Nil
	// when there is nowhere to write, as in the headless dump mode.
	persist func(func(*config.Config)) error

	overlay *Overlay
	status  status

	// Log viewer state.
	logs     *LogBuffer
	logPager *Pager
	logTitle string
	// logName is the bare container or project name, for default save
	// filenames. logTitle carries decoration — "(2 services)" — that has no
	// business in a path.
	logName   string
	logFilter string
	logWrap   bool
	logStamps bool
	// logFormat renders structured lines as their parts rather than raw.
	logFormat   bool
	logCancel   context.CancelFunc
	logToken    int
	logStreamed bool

	// Inspect viewer state.
	inspect      []string
	inspectPager *Pager
	inspectTitle string

	// Process viewer state.
	processes      docker.Processes
	processesPager *Pager
	processesTitle string

	// Image history viewer state.
	history      []docker.HistoryEntry
	historyPager *Pager
	historyTitle string

	events   chan event
	quit     chan struct{}
	quitOnce sync.Once

	// refreshing guards against a slow daemon letting refreshes pile up
	// behind the ticker.
	refreshing bool
	// dockerCLI records whether exec actions are available.
	dockerCLI bool
	// composeBinary is how Compose is invoked on this machine, or nil when
	// it is not installed. Detected separately from dockerCLI: the docker
	// binary being present says nothing about whether the compose plugin
	// alongside it is.
	composeBinary compose.Binary
	version       docker.Version
	// build identifies the running binary, so a stale install is visible
	// rather than looking like a bug that was never fixed.
	build string
}

// status is a transient message shown in the status bar.
type status struct {
	text    string
	isError bool
	at      time.Time
}

// statusLifetime is how long a status message stays before fading out. Long
// enough to read, short enough that it does not become permanent furniture.
const statusLifetime = 6 * time.Second

// New creates an app bound to a daemon and a screen.
func New(cfg config.Config, client *docker.Client, screen *tui.Screen, runner *Runner) *App {
	app := &App{
		config:         cfg,
		docker:         client,
		screen:         screen,
		runner:         runner,
		stats:          make(map[string]docker.Stat),
		cpuHistory:     make(map[string]*history),
		restartCounts:  make(map[string]int),
		probes:         newProbeWatcher(cfg),
		probeResults:   make(map[string]probe.Result),
		deploys:        deploy.New(deployGrace, deployPatience),
		daemonEvents:   NewEventRing(eventHistory),
		grouped:        cfg.GroupContainers,
		collapsed:      collapsedSet(cfg.CollapsedGroups),
		marked:         make(map[string]bool),
		logs:           NewLogBuffer(5000),
		logPager:       NewPager(true),
		inspectPager:   NewPager(false),
		processesPager: NewPager(false),
		historyPager:   NewPager(false),
		logWrap:        cfg.LogWrap,
		logStamps:      cfg.LogTimestamps,
		showSystem:     cfg.SystemPanel,
		imageUsage:     make(map[string]int),
		updates:        make(map[string]registry.Verdict),
		updateChecker:  registry.NewChecker(),
		logFormat:      cfg.LogFormat,
		sortKey:        parseConfiguredSort(cfg.Sort),
		events:         make(chan event, 64),
		quit:           make(chan struct{}),
		dockerCLI:      DockerCLIAvailable(),
		composeBinary:  compose.Detect(),
	}
	// The off-switch is the streamer never existing, so nothing downstream
	// needs to consult the configuration again. Samples come back through
	// the event channel like every other asynchronous result, keeping the
	// model single-goroutine.
	if cfg.Stats {
		app.statStreams = docker.NewStatsStreamer(client, func(id string, stat docker.Stat) {
			app.post(statSampled{id: id, stat: stat})
		})
	}
	return app
}

// parseConfiguredSort reads the stored ordering, falling back to the default
// rather than refusing to start over a value someone mistyped.
func parseConfiguredSort(name string) SortKey {
	key, _ := ParseSortKey(name)
	return key
}

// SetPreferenceWriter supplies somewhere to record settings that should
// outlive the session.
//
// Injected rather than built in, so this package needs to know nothing about
// where configuration lives — and so the headless dump mode, which should
// never write anything, simply does not provide one.
func (a *App) SetPreferenceWriter(write func(func(*config.Config)) error) {
	a.persist = write
}

// remember records a preference change, if there is anywhere to put it.
//
// A failure is reported rather than swallowed. Silently not saving is how a
// preference comes to look like it works until the next launch, which is a
// worse outcome than a line in the status bar.
func (a *App) remember(change func(*config.Config)) {
	if a.persist == nil {
		return
	}
	if err := a.persist(change); err != nil {
		a.setError("could not save preference: %v", err)
	}
}

// event is anything the loop must react to besides a key press.
type event interface{ apply(*App) }

// Run starts the event loop and blocks until the user quits or the input
// stream ends.
func (a *App) Run(ctx context.Context, keys <-chan tui.Key, resize <-chan struct{}) error {
	if version, err := a.docker.Ping(ctx); err == nil {
		a.version = version
	}
	// Said at startup rather than when an action is picked: a user who knows
	// up front that exec or compose will not work can go and install the
	// missing piece, instead of finding out from a failure at the moment
	// they needed the thing to work.
	switch {
	case !a.dockerCLI:
		a.setError("docker CLI not found on PATH: exec and compose actions are unavailable")
	case !a.composeBinary.Available():
		a.setError("%v", ErrComposeMissing)
	}

	// Streams opened during this run derive from its context, and are torn
	// down when it ends. The deferred order matters: defers run last-in
	// first-out, so Stop closes the quit channel first, releasing any stream
	// goroutine parked in post, and only then does Close wait for them.
	a.streamCtx = ctx
	if a.statStreams != nil {
		defer a.statStreams.Close()
		defer a.Stop()
	}

	a.watchEvents(ctx)
	a.refresh(ctx)
	ticker := time.NewTicker(a.config.RefreshInterval())
	defer ticker.Stop()

	if err := a.draw(); err != nil {
		return err
	}

	for {
		select {
		case <-ctx.Done():
			return nil

		case <-a.quit:
			return nil

		case key, ok := <-keys:
			if !ok {
				return nil
			}
			a.handleKey(ctx, key)

		case <-resize:
			width, height := tui.SizeOrDefault(tui.StdoutFd)
			a.screen.Resize(width, height)

		case <-ticker.C:
			a.refresh(ctx)

		case ev := <-a.events:
			ev.apply(a)
			// Drain anything else already queued before redrawing, so a
			// burst of log lines costs one frame rather than dozens.
			for {
				select {
				case next := <-a.events:
					next.apply(a)
					continue
				default:
				}
				break
			}
		}

		if err := a.draw(); err != nil {
			return err
		}
	}
}

// Stop ends the event loop.
func (a *App) Stop() {
	a.quitOnce.Do(func() { close(a.quit) })
}

// post delivers an event to the loop without blocking the sender. Dropping an
// event under extreme load is preferable to deadlocking a producer goroutine
// against a loop that is itself waiting.
func (a *App) post(ev event) {
	select {
	case a.events <- ev:
	case <-a.quit:
	}
}

// setStatus shows an informational message.
func (a *App) setStatus(format string, args ...any) {
	a.status = status{text: fmt.Sprintf(format, args...), at: time.Now()}
}

// setError shows an error message.
func (a *App) setError(format string, args ...any) {
	a.status = status{text: fmt.Sprintf(format, args...), isError: true, at: time.Now()}
}

// draw renders the current state to the screen.
func (a *App) draw() error {
	width, height := a.screen.Size()
	lines := a.frame(width, height)

	if err := a.screen.Render(lines); err != nil {
		return err
	}
	// An input overlay needs a visible caret; everything else hides it.
	if a.overlay != nil && a.overlay.Kind == OverlayInput {
		_, col, row := a.overlay.Render(a.screen, width, height)
		if col >= 0 {
			return a.screen.MoveCursor(col, row)
		}
	}
	return a.screen.HideCursor()
}

// Frame renders one complete frame at the given size. It is exported so the
// headless dump mode can render without a terminal.
func (a *App) Frame(width, height int) []string { return a.frame(width, height) }

// frame composes the full screen: header, body, status bar, and any overlay on
// top.
func (a *App) frame(width, height int) []string {
	lines := make([]string, 0, height)
	lines = append(lines, a.renderHeader(width))

	// Header takes one row, status bar one, leaving the rest for the body.
	bodyHeight := height - 2
	if bodyHeight < 1 {
		bodyHeight = 1
	}

	// The system panel eats into the list, never the header or status bar,
	// and is dropped entirely when the terminal is too short to leave a
	// usable list behind it.
	panel := a.systemPanel(width, bodyHeight)
	bodyHeight -= len(panel)

	var body []string
	switch a.mode {
	case ModeLogs:
		body = a.renderLogs(width, bodyHeight)
	case ModeInspect:
		body = a.renderInspect(width, bodyHeight)
	case ModeProcesses:
		body = a.renderProcesses(width, bodyHeight)
	case ModeHistory:
		body = a.renderHistory(width, bodyHeight)
	case ModeHelp:
		body = a.renderHelp(width, bodyHeight)
	default:
		body = a.renderList(width, bodyHeight)
	}
	for i := 0; i < bodyHeight; i++ {
		if i < len(body) {
			lines = append(lines, body[i])
			continue
		}
		lines = append(lines, "")
	}

	lines = append(lines, panel...)
	lines = append(lines, a.renderStatusBar(width))

	if a.overlay != nil {
		overlay, _, _ := a.overlay.Render(a.screen, width, height)
		lines = mergeOverlay(lines, overlay, height)
	}

	// Clip every row to the frame. Screen.Render would do this on the way
	// out, but Frame is also the headless dump path, which prints the rows
	// directly — an over-long row there wraps and pushes the whole frame
	// out of shape. Truncating here keeps one guarantee for both callers:
	// a frame is exactly height rows of at most width cells.
	for i, line := range lines {
		if tui.VisibleWidth(line) > width {
			lines[i] = tui.TruncateEllipsis(line, width)
		}
	}
	return lines
}

// mergeOverlay draws overlay lines over the base frame, leaving base content
// visible wherever the overlay has nothing to draw.
func mergeOverlay(base, overlay []string, height int) []string {
	merged := make([]string, height)
	for i := range height {
		switch {
		case i < len(overlay) && overlay[i] != "":
			merged[i] = overlay[i]
		case i < len(base):
			merged[i] = base[i]
		}
	}
	return merged
}

// currentLength returns how many rows the active view's list has, after
// filtering.
func (a *App) currentLength() int {
	switch a.view {
	case ViewContainers:
		// Rows, not containers: with grouping on the list carries
		// headings too, and bounding the cursor by the container count
		// would strand it on one whenever the list shrank.
		return len(a.rows())
	case ViewCompose:
		return len(a.filteredProjects())
	case ViewImages:
		return len(a.filteredImages())
	case ViewVolumes:
		return len(a.filteredVolumes())
	case ViewNetworks:
		return len(a.filteredNetworks())
	case ViewEvents:
		return len(a.filteredEvents())
	}
	return 0
}

// selected returns the selection index for the active view, clamped to the
// current list length. Lists shrink under the cursor whenever a container
// exits, so this is read rather than trusted from the stored value.
func (a *App) selected() int {
	index := clamp(a.selection[a.view], a.currentLength())
	a.selection[a.view] = index
	return index
}

// selectedContainer returns the highlighted container, if any.
func (a *App) selectedContainer() (docker.Container, bool) {
	// A group heading is a row but not a container. Saying so here is what
	// makes every container key a no-op while one is selected: they all
	// already handle there being nothing selected, so a restart cannot land
	// on six containers because the cursor was one line high.
	row, ok := a.selectedRow()
	if !ok || row.Header != nil {
		return docker.Container{}, false
	}
	return row.Container, true
}

// selectedProject returns the highlighted Compose project, if any.
func (a *App) selectedProject() (compose.Project, bool) {
	list := a.filteredProjects()
	if len(list) == 0 {
		return compose.Project{}, false
	}
	return list[clamp(a.selection[ViewCompose], len(list))], true
}

// selectedImage returns the highlighted image, if any.
func (a *App) selectedImage() (docker.Image, bool) {
	list := a.filteredImages()
	if len(list) == 0 {
		return docker.Image{}, false
	}
	return list[clamp(a.selection[ViewImages], len(list))], true
}

// selectedNetwork returns the highlighted network, if any.
func (a *App) selectedNetwork() (docker.Network, bool) {
	list := a.filteredNetworks()
	if len(list) == 0 {
		return docker.Network{}, false
	}
	return list[clamp(a.selection[ViewNetworks], len(list))], true
}

// selectedVolume returns the highlighted volume, if any.
func (a *App) selectedVolume() (docker.Volume, bool) {
	list := a.filteredVolumes()
	if len(list) == 0 {
		return docker.Volume{}, false
	}
	return list[clamp(a.selection[ViewVolumes], len(list))], true
}
