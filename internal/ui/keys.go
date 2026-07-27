package ui

import (
	"context"
	"fmt"
	"strings"

	"github.com/RchrdHndrcks/muelle/internal/compose"
	"github.com/RchrdHndrcks/muelle/internal/docker"
	"github.com/RchrdHndrcks/muelle/internal/quickcmd"
	"github.com/RchrdHndrcks/muelle/internal/tui"
)

// handleKey routes a key press to whatever currently owns the keyboard.
//
// The order is the precedence: an open overlay swallows everything, then the
// full-screen viewers, then the list. Only keys nothing else claimed reach the
// global bindings, so "q" quits from a list but types a "q" into a filter.
func (a *App) handleKey(ctx context.Context, key tui.Key) {
	if a.overlay != nil {
		closed, consumed := a.overlay.HandleKey(key)
		if closed {
			a.overlay = nil
		}
		if consumed {
			return
		}
	}

	switch a.mode {
	case ModeLogs:
		if a.handleLogsKey(ctx, key) {
			return
		}
	case ModeInspect:
		if a.handleInspectKey(key) {
			return
		}
	case ModeHelp:
		if key.Type == tui.KeyEscape || key.IsRune('q') || key.IsRune('?') {
			a.mode = ModeList
			return
		}
		return
	default:
		if a.handleListKey(ctx, key) {
			return
		}
	}

	a.handleGlobalKey(ctx, key)
}

// handleGlobalKey handles bindings that work everywhere.
func (a *App) handleGlobalKey(ctx context.Context, key tui.Key) {
	switch {
	case key.IsRune('q'), key.Type == tui.KeyCtrlC:
		a.Stop()
	case key.IsRune('?'):
		a.mode = ModeHelp
	case key.Type == tui.KeyCtrlR:
		a.refreshing = false
		a.refresh(ctx)
		a.setStatus("refreshing")
	case key.Type == tui.KeyTab:
		a.switchView((a.view + 1) % viewCount)
	case key.Type == tui.KeyShiftTab:
		a.switchView((a.view + viewCount - 1) % viewCount)
	case key.IsRune('1'):
		a.switchView(ViewContainers)
	case key.IsRune('2'):
		a.switchView(ViewCompose)
	case key.IsRune('3'):
		a.switchView(ViewImages)
	case key.IsRune('4'):
		a.switchView(ViewVolumes)
	}
}

// switchView changes tab and refreshes if the new view's data has not been
// loaded yet.
func (a *App) switchView(view View) {
	if a.view == view {
		return
	}
	a.view = view
	a.mode = ModeList
	// Images and volumes are only fetched while their view is active, so
	// the first visit needs an immediate load rather than a wait for the
	// next tick.
	switch view {
	case ViewImages:
		if len(a.images) == 0 {
			a.refreshing = false
			a.refresh(context.Background())
		}
	case ViewVolumes:
		if len(a.volumes) == 0 {
			a.refreshing = false
			a.refresh(context.Background())
		}
	}
}

// handleListKey handles keys for the list views. It reports whether the key
// was consumed.
func (a *App) handleListKey(ctx context.Context, key tui.Key) bool {
	length := a.currentLength()

	switch {
	case key.Type == tui.KeyDown, key.IsRune('j'):
		a.selection[a.view] = clamp(a.selected()+1, length)
		return true
	case key.Type == tui.KeyUp, key.IsRune('k'):
		a.selection[a.view] = clamp(a.selected()-1, length)
		return true
	case key.IsRune('g'), key.Type == tui.KeyHome:
		a.selection[a.view] = 0
		return true
	case key.IsRune('G'), key.Type == tui.KeyEnd:
		a.selection[a.view] = clamp(length-1, length)
		return true
	case key.Type == tui.KeyPageDown, key.Type == tui.KeyCtrlD:
		a.selection[a.view] = clamp(a.selected()+a.pageSize(), length)
		return true
	case key.Type == tui.KeyPageUp, key.Type == tui.KeyCtrlU:
		a.selection[a.view] = clamp(a.selected()-a.pageSize(), length)
		return true

	case key.IsRune('/'):
		a.openFilterPrompt()
		return true
	case key.Type == tui.KeyEscape:
		if a.filter != "" {
			a.filter = ""
			a.setStatus("filter cleared")
		}
		return true
	}

	switch a.view {
	case ViewContainers:
		return a.handleContainerKey(ctx, key)
	case ViewCompose:
		return a.handleComposeKey(ctx, key)
	case ViewImages:
		return a.handleImageKey(ctx, key)
	case ViewVolumes:
		return a.handleVolumeKey(ctx, key)
	}
	return false
}

// pageSize is how far Ctrl-D and Page Down move: half a screen, matching the
// convention every pager uses.
func (a *App) pageSize() int {
	_, height := a.screen.Size()
	size := (height - 3) / 2
	if size < 1 {
		return 1
	}
	return size
}

// openFilterPrompt opens the filter input for the current list.
func (a *App) openFilterPrompt() {
	a.overlay = NewInput("Filter "+a.view.Title(), "match:", a.filter, func(value any) {
		filter, _ := value.(string)
		a.filter = strings.TrimSpace(filter)
		a.selection[a.view] = 0
		a.offset[a.view] = 0
	})
}

// handleContainerKey handles the container view's actions.
func (a *App) handleContainerKey(ctx context.Context, key tui.Key) bool {
	container, ok := a.selectedContainer()
	if !ok {
		// The toggle still works with an empty list; it is usually what
		// fills it.
		if key.IsRune('a') {
			a.showAll = !a.showAll
			a.refreshing = false
			a.refresh(ctx)
			return true
		}
		return false
	}
	name := container.Name()

	switch {
	case key.IsRune('a'):
		a.showAll = !a.showAll
		a.refreshing = false
		a.refresh(ctx)
		a.setStatus("showing %s containers", map[bool]string{true: "all", false: "running"}[a.showAll])

	case key.Type == tui.KeyEnter, key.IsRune('i'):
		a.openInspect(ctx, container)

	case key.IsRune('l'):
		a.openLogs(ctx, container)

	case key.IsRune('x'):
		a.openExecMenu(ctx, container)

	case key.IsRune('e'):
		// Straight to a shell, skipping the menu.
		a.execInContainer(container, []string{"sh", "-c", "command -v bash >/dev/null && exec bash || exec sh"})

	case key.IsRune('s'):
		a.runAction(ctx, "started "+name, func(c context.Context) error {
			return a.docker.Start(c, container.ID)
		})

	case key.IsRune('t'):
		a.runAction(ctx, "stopped "+name, func(c context.Context) error {
			return a.docker.Stop(c, container.ID, a.config.StopTimeout)
		})

	case key.IsRune('r'):
		a.runAction(ctx, "restarted "+name, func(c context.Context) error {
			return a.docker.Restart(c, container.ID, a.config.StopTimeout)
		})

	case key.IsRune('p'):
		if container.State == "paused" {
			a.runAction(ctx, "unpaused "+name, func(c context.Context) error {
				return a.docker.Unpause(c, container.ID)
			})
			break
		}
		a.runAction(ctx, "paused "+name, func(c context.Context) error {
			return a.docker.Pause(c, container.ID)
		})

	case key.IsRune('K'):
		a.confirm("Kill container", "Send SIGKILL to "+name+"?", func() {
			a.runAction(ctx, "killed "+name, func(c context.Context) error {
				return a.docker.Kill(c, container.ID)
			})
		})

	case key.IsRune('D'):
		a.confirm("Remove container", "Remove "+name+"? This cannot be undone.", func() {
			a.runAction(ctx, "removed "+name, func(c context.Context) error {
				return a.docker.Remove(c, container.ID, true, false)
			})
		})

	default:
		return false
	}
	return true
}

// handleComposeKey handles the Compose view's actions.
func (a *App) handleComposeKey(ctx context.Context, key tui.Key) bool {
	project, ok := a.selectedProject()
	if !ok {
		return false
	}

	switch {
	case key.Type == tui.KeyEnter:
		a.openComposeMenu(project)
	case key.IsRune('u'):
		a.runCompose(project, compose.ActionUp)
	case key.IsRune('d'):
		a.confirm("Compose down", "Stop and remove everything in "+project.Name+"?", func() {
			a.runCompose(project, compose.ActionDown)
		})
	case key.IsRune('r'):
		a.runCompose(project, compose.ActionRestart)
	case key.IsRune('l'):
		a.openProjectLogs(ctx, project)
	default:
		return false
	}
	return true
}

// handleImageKey handles the image view's actions.
func (a *App) handleImageKey(ctx context.Context, key tui.Key) bool {
	image, ok := a.selectedImage()
	switch {
	case key.IsRune('D') && ok:
		a.confirm("Remove image", "Remove "+image.Tag()+"?", func() {
			a.runAction(ctx, "removed "+image.Tag(), func(c context.Context) error {
				return a.docker.RemoveImage(c, image.ID, false)
			})
		})
	case key.IsRune('P'):
		a.confirm("Prune images", "Remove all dangling images?", func() {
			a.runPrune(ctx, "images", a.docker.PruneImages)
		})
	default:
		return false
	}
	return true
}

// handleVolumeKey handles the volume view's actions.
func (a *App) handleVolumeKey(ctx context.Context, key tui.Key) bool {
	volume, ok := a.selectedVolume()
	switch {
	case key.IsRune('D') && ok:
		a.confirm("Remove volume", "Remove "+volume.Name+"? Its data will be lost.", func() {
			a.runAction(ctx, "removed "+volume.Name, func(c context.Context) error {
				return a.docker.RemoveVolume(c, volume.Name, false)
			})
		})
	case key.IsRune('P'):
		a.confirm("Prune volumes", "Remove all volumes not used by a container? Their data will be lost.", func() {
			a.runPrune(ctx, "volumes", a.docker.PruneVolumes)
		})
	default:
		return false
	}
	return true
}

// runPrune performs a prune and reports what it reclaimed.
func (a *App) runPrune(ctx context.Context, what string, prune func(context.Context) (docker.PruneResult, error)) {
	go func() {
		result, err := prune(ctx)
		if err != nil {
			a.post(actionDone{err: err})
			return
		}
		a.post(actionDone{message: fmt.Sprintf("pruned %s: %d removed, %s reclaimed",
			what, result.Deleted, FormatBytes(uint64(result.SpaceReclaimed)))})
		a.post(refreshRequested{})
	}()
}

// handleLogsKey handles the log viewer's keys.
func (a *App) handleLogsKey(ctx context.Context, key tui.Key) bool {
	_, height := a.screen.Size()
	viewport := height - 2
	total := len(RenderLogs(a.logs.Lines(a.logFilter), a.screenWidth(), a.logWrap, a.logStamps, a.screen.Style))

	switch {
	case key.Type == tui.KeyEscape, key.IsRune('q'):
		a.closeLogs()
	case key.Type == tui.KeyDown, key.IsRune('j'):
		a.logPager.ScrollBy(1, total, viewport)
	case key.Type == tui.KeyUp, key.IsRune('k'):
		a.logPager.ScrollBy(-1, total, viewport)
	case key.Type == tui.KeyPageDown, key.Type == tui.KeyCtrlD:
		a.logPager.ScrollBy(viewport/2, total, viewport)
	case key.Type == tui.KeyPageUp, key.Type == tui.KeyCtrlU:
		a.logPager.ScrollBy(-viewport/2, total, viewport)
	case key.IsRune('g'), key.Type == tui.KeyHome:
		a.logPager.ScrollToTop()
	case key.IsRune('G'), key.Type == tui.KeyEnd:
		a.logPager.ScrollToBottom(total, viewport)
	case key.IsRune('f'):
		if a.logPager.ToggleFollow() {
			a.setStatus("following")
			break
		}
		a.setStatus("follow paused")
	case key.IsRune('w'):
		a.logWrap = !a.logWrap
		a.setStatus("wrap %s", onOff(a.logWrap))
	case key.IsRune('t'):
		a.logStamps = !a.logStamps
		a.setStatus("timestamps %s", onOff(a.logStamps))
	case key.IsRune('/'):
		a.overlay = NewInput("Filter logs", "match:", a.logFilter, func(value any) {
			filter, _ := value.(string)
			a.logFilter = strings.TrimSpace(filter)
		})
	case key.IsRune('c'):
		a.logs.Reset()
		a.setStatus("cleared")
	default:
		return false
	}
	return true
}

// handleInspectKey handles the JSON viewer's keys.
func (a *App) handleInspectKey(key tui.Key) bool {
	_, height := a.screen.Size()
	viewport := height - 2
	total := len(a.inspect)

	switch {
	case key.Type == tui.KeyEscape, key.IsRune('q'):
		a.mode = ModeList
	case key.Type == tui.KeyDown, key.IsRune('j'):
		a.inspectPager.ScrollBy(1, total, viewport)
	case key.Type == tui.KeyUp, key.IsRune('k'):
		a.inspectPager.ScrollBy(-1, total, viewport)
	case key.Type == tui.KeyPageDown, key.Type == tui.KeyCtrlD:
		a.inspectPager.ScrollBy(viewport/2, total, viewport)
	case key.Type == tui.KeyPageUp, key.Type == tui.KeyCtrlU:
		a.inspectPager.ScrollBy(-viewport/2, total, viewport)
	case key.IsRune('g'), key.Type == tui.KeyHome:
		a.inspectPager.ScrollToTop()
	case key.IsRune('G'), key.Type == tui.KeyEnd:
		a.inspectPager.ScrollToBottom(total, viewport)
	default:
		return false
	}
	return true
}

// screenWidth returns the current screen width.
func (a *App) screenWidth() int {
	width, _ := a.screen.Size()
	return width
}

// confirm opens a destructive-action confirmation.
func (a *App) confirm(title, prompt string, onConfirm func()) {
	a.overlay = NewConfirm(title, prompt, true, func(any) { onConfirm() })
}

// onOff renders a toggle state.
func onOff(on bool) string {
	if on {
		return "on"
	}
	return "off"
}

// openExecMenu offers the commands worth running inside a container.
func (a *App) openExecMenu(ctx context.Context, container docker.Container) {
	if !a.dockerCLI {
		a.setError("%v", ErrDockerCLIMissing)
		return
	}
	if !container.Running() {
		a.setError("%s is not running", container.Name())
		return
	}

	// The environment lives in the inspect payload, so the menu is built
	// asynchronously rather than blocking the loop on a daemon round trip.
	go func() {
		fetchCtx, cancel := context.WithTimeout(ctx, refreshTimeout)
		defer cancel()

		detail, err := a.docker.Inspect(fetchCtx, container.ID)
		env := map[string]string{}
		if err == nil {
			env = detail.Env()
		}
		commands := quickcmd.Suggest(container.Image, env)

		items := make([]MenuItem, 0, len(commands))
		for _, command := range commands {
			items = append(items, MenuItem{
				Label:  command.Label,
				Detail: command.String(),
				Value:  command,
			})
		}
		a.post(showExecMenu{container: container, items: items})
	}()
}

// showExecMenu opens the exec menu once its commands have been built.
type showExecMenu struct {
	container docker.Container
	items     []MenuItem
}

func (e showExecMenu) apply(a *App) {
	a.overlay = NewMenu("Exec in "+e.container.Name(), e.items, func(value any) {
		command, ok := value.(quickcmd.Command)
		if !ok {
			return
		}
		a.execInContainer(e.container, command.Argv)
	})
}

// execInContainer suspends the TUI and runs a command inside a container.
func (a *App) execInContainer(container docker.Container, command []string) {
	if a.runner == nil {
		a.setError("exec is unavailable in this mode")
		return
	}
	// No pause afterwards: the user ended the session themselves, so they
	// do not need a prompt telling them it ended.
	if err := a.runner.Run(ExecArgv(container.ID, command), false); err != nil {
		a.setError("exec: %v", err)
		return
	}
	a.setStatus("exec session ended")
}

// openComposeMenu offers the actions available for a project.
func (a *App) openComposeMenu(project compose.Project) {
	actions := compose.Actions(project)
	items := make([]MenuItem, 0, len(actions))
	for _, action := range actions {
		items = append(items, MenuItem{
			Label:  action.Label(),
			Detail: strings.Join(compose.Command(project, action), " "),
			Value:  action,
		})
	}
	a.overlay = NewMenu("Compose: "+project.Name, items, func(value any) {
		action, ok := value.(compose.Action)
		if !ok {
			return
		}
		if action.Destructive() {
			a.confirm("Compose "+string(action),
				"Stop and remove everything in "+project.Name+"?",
				func() { a.runCompose(project, action) })
			return
		}
		a.runCompose(project, action)
	})
}

// runCompose suspends the TUI and runs a Compose command, leaving its output
// on screen until the user acknowledges it.
func (a *App) runCompose(project compose.Project, action compose.Action) {
	if a.runner == nil {
		a.setError("compose actions are unavailable in this mode")
		return
	}
	if !a.dockerCLI {
		a.setError("%v", ErrDockerCLIMissing)
		return
	}
	if len(project.ConfigFiles) == 0 && project.WorkingDir == "" {
		a.setError("no compose file known for %s", project.Name)
		return
	}

	argv := compose.Command(project, action)
	// Compose output is worth reading — pulled layers, build errors, why a
	// service refused to start — so the child's output stays until
	// dismissed.
	if err := a.runner.Run(argv, true); err != nil {
		a.setError("compose %s: %v", action, err)
	} else {
		a.setStatus("compose %s finished for %s", action, project.Name)
	}
	a.refreshing = false
	a.refresh(context.Background())
}
