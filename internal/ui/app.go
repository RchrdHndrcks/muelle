package ui

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/RchrdHndrcks/muelle/internal/compose"
	"github.com/RchrdHndrcks/muelle/internal/config"
	"github.com/RchrdHndrcks/muelle/internal/docker"
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
	volumes    []docker.Volume
	stats      map[string]docker.Stat

	// Per-view selection and scroll, kept separately so switching tabs
	// returns you to where you were.
	selection [viewCount]int
	offset    [viewCount]int

	// showAll toggles between running-only and every container.
	showAll bool
	// filter narrows the current list by substring.
	filter string

	overlay *Overlay
	status  status

	// Log viewer state.
	logs        *LogBuffer
	logPager    *Pager
	logTitle    string
	logFilter   string
	logWrap     bool
	logStamps   bool
	logCancel   context.CancelFunc
	logToken    int
	logStreamed bool

	// Inspect viewer state.
	inspect      []string
	inspectPager *Pager
	inspectTitle string

	events   chan event
	quit     chan struct{}
	quitOnce sync.Once

	// refreshing guards against a slow daemon letting refreshes pile up
	// behind the ticker.
	refreshing bool
	// dockerCLI records whether exec and Compose actions are available.
	dockerCLI bool
	version   docker.Version
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
	return &App{
		config:       cfg,
		docker:       client,
		screen:       screen,
		runner:       runner,
		stats:        make(map[string]docker.Stat),
		logs:         NewLogBuffer(5000),
		logPager:     NewPager(true),
		inspectPager: NewPager(false),
		logWrap:      true,
		logStamps:    false,
		events:       make(chan event, 64),
		quit:         make(chan struct{}),
		dockerCLI:    DockerCLIAvailable(),
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
	if !a.dockerCLI {
		a.setError("docker CLI not found on PATH: exec and compose actions are unavailable")
	}

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

	var body []string
	switch a.mode {
	case ModeLogs:
		body = a.renderLogs(width, bodyHeight)
	case ModeInspect:
		body = a.renderInspect(width, bodyHeight)
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

	lines = append(lines, a.renderStatusBar(width))

	if a.overlay != nil {
		overlay, _, _ := a.overlay.Render(a.screen, width, height)
		lines = mergeOverlay(lines, overlay, height)
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
		return len(a.filteredContainers())
	case ViewCompose:
		return len(a.filteredProjects())
	case ViewImages:
		return len(a.filteredImages())
	case ViewVolumes:
		return len(a.filteredVolumes())
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
	list := a.filteredContainers()
	if len(list) == 0 {
		return docker.Container{}, false
	}
	return list[clamp(a.selection[ViewContainers], len(list))], true
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

// selectedVolume returns the highlighted volume, if any.
func (a *App) selectedVolume() (docker.Volume, bool) {
	list := a.filteredVolumes()
	if len(list) == 0 {
		return docker.Volume{}, false
	}
	return list[clamp(a.selection[ViewVolumes], len(list))], true
}
