package ui

import (
	"context"
	"fmt"
	"strings"

	"github.com/RchrdHndrcks/muelle/internal/compose"
	"github.com/RchrdHndrcks/muelle/internal/config"
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
		// Hold on to the overlay being dismissed. Its accept callback runs
		// inside HandleKey and may install a follow-up — a menu choice
		// raising a confirmation, say. Clearing unconditionally would wipe
		// that replacement and swallow the action with it.
		current := a.overlay
		closed, consumed := current.HandleKey(key)
		if closed && a.overlay == current {
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
	case ModeProcesses:
		if a.handleProcessesKey(key) {
			return
		}
	case ModeHistory:
		if a.handleHistoryKey(key) {
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
	case key.IsRune('S'):
		a.showSystem = !a.showSystem
		if a.showSystem {
			// Nothing has been measured while it was hidden, so fetch
			// immediately rather than leaving an empty panel until the
			// next tick.
			a.refreshing = false
			a.refresh(ctx)
		}
		a.setStatus("system panel %s", onOff(a.showSystem))
	case key.Type == tui.KeyCtrlR:
		a.refreshing = false
		a.refresh(ctx)
		a.setStatus("refreshing")
	case key.Type == tui.KeyTab, key.Type == tui.KeyRight:
		a.switchView((a.view + 1) % viewCount)
	case key.Type == tui.KeyShiftTab, key.Type == tui.KeyLeft:
		a.switchView((a.view + viewCount - 1) % viewCount)
	case key.IsRune('1'):
		a.switchView(ViewContainers)
	case key.IsRune('2'):
		a.switchView(ViewCompose)
	case key.IsRune('3'):
		a.switchView(ViewImages)
	case key.IsRune('4'):
		a.switchView(ViewVolumes)
	case key.IsRune('5'):
		a.switchView(ViewNetworks)
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
	case ViewNetworks:
		if len(a.networks) == 0 {
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

	case key.IsRune('o'):
		if a.view == ViewContainers {
			// Note which container is under the cursor before the order
			// changes, so the cursor can follow it rather than staying
			// at an index that now points at something else.
			var selectedID string
			if container, ok := a.selectedContainer(); ok {
				selectedID = container.ID
			}
			a.sortKey = a.sortKey.Next()
			a.reselect(selectedID)
			// Remembered, so the ordering someone chose is the one they
			// find next time rather than a setting to reapply on every
			// launch.
			chosen := a.sortKey.Label()
			// Status first: remember reports a failed save by replacing
			// it, and a save that did not happen is the more important
			// of the two things to say.
			a.setStatus("sorted by %s", chosen)
			a.remember(func(c *config.Config) { c.Sort = chosen })
			return true
		}

	case key.IsRune('/'):
		a.openFilterPrompt()
		return true
	case key.Type == tui.KeyEscape:
		// Marks before the filter — deliberate precedence. Esc peels back
		// the most recent layer of intent: marks choose what to act on
		// within an already narrowed list, so they go first, and a second
		// Esc clears the narrowing itself. The other order would widen the
		// list while a pending bulk action still points into it.
		if a.view == ViewContainers && len(a.marked) > 0 {
			a.clearMarks()
			a.setStatus("marks cleared")
			return true
		}
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
	case ViewNetworks:
		return a.handleNetworkKey(ctx, key)
	}
	return false
}

// reselect moves the cursor back onto the container with the given ID.
//
// Without this, reordering leaves the cursor at the same index — which is now
// a different container — and the next keystroke would act on something the
// user did not choose.
func (a *App) reselect(containerID string) {
	// Rows, not containers. With grouping on the list carries headings, so
	// a container's index in the container list is not the row it is drawn
	// on — and putting the cursor there would land it a heading or two
	// above the container it was meant to follow.
	rows := a.rows()
	if len(rows) == 0 {
		return
	}
	if containerID != "" {
		for i, row := range rows {
			if row.Header == nil && row.Container.ID == containerID {
				a.selection[ViewContainers] = i
				return
			}
		}
	}
	a.selection[ViewContainers] = clamp(a.selection[ViewContainers], len(rows))
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
		a.applyFilter(strings.TrimSpace(filter))
	})
}

// handleContainerKey handles the container view's actions.
func (a *App) handleContainerKey(ctx context.Context, key tui.Key) bool {
	// The grouping keys act on rows rather than containers, and a heading is
	// a row that is not one, so they are answered before anything asks for a
	// selected container.
	switch {
	case key.IsRune('A'):
		a.toggleGrouping()
		return true
	case key.IsRune('z'):
		a.toggleCollapse()
		return true
	case key.IsRune(' '):
		// Space marks rows for a bulk action, and on a heading it marks
		// the whole group — so it too must be answered before anything
		// asks for a selected container.
		a.toggleMark()
		return true
	}
	if row, ok := a.selectedRow(); ok && row.Header != nil && key.Type == tui.KeyEnter {
		// Enter is inspect on a container; on a heading there is nothing
		// to inspect, and folding is what the row is for.
		a.toggleCollapse()
		return true
	}

	// With marks in play the lifecycle keys act on the marked set, wherever
	// the cursor happens to be — including on a heading, where there is no
	// selected container to fall back to.
	if len(a.marked) > 0 && a.handleBulkKey(ctx, key) {
		return true
	}

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

	case key.IsRune('T'):
		a.openProcesses(ctx, container)

	case key.IsRune('e'):
		// Straight to a shell, skipping the menu. Same probe the menu
		// entry uses, so the two cannot drift apart.
		a.execInContainer(container, []string{"sh", "-c", quickcmd.ShellProbe})

	case key.IsRune('s'):
		a.runAction(ctx, "started "+name, func(c context.Context) error {
			return a.docker.Start(c, container.ID)
		})

	case key.IsRune('t'):
		a.runAction(ctx, "stopped "+name, func(c context.Context) error {
			return a.docker.Stop(c, container.ID, a.config.StopTimeout)
		})

	case key.IsRune('r'):
		a.openRestartMenu(ctx, container)

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

	case key.IsRune('P'):
		a.openSystemPruneMenu(ctx)

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
		a.previewUp(project)
	case key.IsRune('U'):
		a.confirmUpdate(project)
	case key.IsRune('d'):
		a.confirm("Compose down", "Stop and remove everything in "+project.Name+"?", func() {
			a.runCompose(project, compose.ActionDown)
		})
	case key.IsRune('r'):
		a.runCompose(project, compose.ActionRestart)
	case key.IsRune('e'):
		a.openEditMenu(project)
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
	// The same keys that inspect a container, because the question is the
	// same shape: what is this thing actually made of? For an image the
	// answer worth a full screen is its layers, not its config JSON.
	case (key.Type == tui.KeyEnter || key.IsRune('i')) && ok:
		a.openHistory(ctx, image)

	case key.IsRune('u'):
		a.unusedImagesOnly = !a.unusedImagesOnly
		a.selection[ViewImages] = 0
		a.offset[ViewImages] = 0
		if a.unusedImagesOnly {
			count, reclaimable := a.unusedImages()
			a.setStatus("showing %s, %s reclaimable",
				Plural(count, "unused image", "unused images"), FormatBytes(uint64(reclaimable)))
			break
		}
		a.setStatus("showing all images")

	case key.IsRune('c'):
		a.checkImageUpdates(ctx)

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
	case key.IsRune('b') && ok:
		a.backupVolume(volume)
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

// handleNetworkKey handles the network view's actions.
func (a *App) handleNetworkKey(ctx context.Context, key tui.Key) bool {
	network, ok := a.selectedNetwork()
	switch {
	case key.IsRune('D') && ok:
		if network.Predefined() {
			// The daemon refuses these; saying so beats surfacing its
			// error after a confirmation the user need not have answered.
			a.setError("%s is a predefined network and cannot be removed", network.Name)
			return true
		}
		a.confirm("Remove network", "Remove "+network.Name+"?", func() {
			a.runAction(ctx, "removed "+network.Name, func(c context.Context) error {
				return a.docker.RemoveNetwork(c, network.ID)
			})
		})
	case key.IsRune('P'):
		a.confirm("Prune networks", "Remove all networks not used by a container?", func() {
			a.runPrune(ctx, "networks", a.docker.PruneNetworks)
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
	total := len(RenderLogs(a.logs.Lines(a.logFilter), a.logOptions(a.screenWidth()), a.screen.Style))

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
		a.remember(func(c *config.Config) { c.LogWrap = a.logWrap })
	case key.IsRune('t'):
		a.logStamps = !a.logStamps
		a.setStatus("timestamps %s", onOff(a.logStamps))
		a.remember(func(c *config.Config) { c.LogTimestamps = a.logStamps })
	case key.IsRune('F'):
		a.logFormat = !a.logFormat
		a.setStatus("formatting %s", onOff(a.logFormat))
		a.remember(func(c *config.Config) { c.LogFormat = a.logFormat })
	case key.IsRune('/'):
		a.overlay = NewInput("Filter logs", "match:", a.logFilter, func(value any) {
			filter, _ := value.(string)
			a.logFilter = strings.TrimSpace(filter)
		})
	case key.IsRune('s'):
		a.openLogSavePrompt()
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

// updateChoice marks the menu entry for updating a stack. The other entries
// carry a compose.Action, but update cannot be one: the Action enum maps
// one-to-one onto Compose subcommands, and update is a sequence of them.
type updateChoice struct{}

// openComposeMenu offers the actions available for a project.
func (a *App) openComposeMenu(project compose.Project) {
	actions := compose.Actions(project)
	items := make([]MenuItem, 0, len(actions)+1)
	for _, action := range actions {
		items = append(items, MenuItem{
			Label:  action.Label(),
			Detail: strings.Join(a.composeBinary.Command(project, action), " "),
			Value:  action,
		})
		if action == compose.ActionUp {
			// Beside up, which is the single-step version of it. The
			// detail names the steps rather than an argv, because no
			// one command is what this entry runs.
			items = append(items, MenuItem{
				Label:  "update (pull new images and apply)",
				Detail: "pull, up -d, then prune dangling images",
				Value:  updateChoice{},
			})
		}
	}
	a.overlay = NewMenu("Compose: "+project.Name, items, func(value any) {
		if _, ok := value.(updateChoice); ok {
			a.confirmUpdate(project)
			return
		}
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

// composeReady reports whether Compose actions can run against a project,
// saying why not when they cannot.
func (a *App) composeReady(project compose.Project) bool {
	if a.runner == nil {
		a.setError("compose actions are unavailable in this mode")
		return false
	}
	if !a.composeBinary.Available() {
		a.setError("%v", ErrComposeMissing)
		return false
	}
	if len(project.ConfigFiles) == 0 && project.WorkingDir == "" {
		a.setError("no compose file known for %s", project.Name)
		return false
	}
	return true
}

// runCompose suspends the TUI and runs a Compose command, leaving its output
// on screen until the user acknowledges it.
func (a *App) runCompose(project compose.Project, action compose.Action, services ...string) {
	if !a.composeReady(project) {
		return
	}

	argv := a.composeBinary.Command(project, action, services...)
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

// confirmUpdate asks before updating a stack, naming the three steps.
//
// Not marked destructive: nothing here loses data — recreated containers are
// what up -d always does, and a dangling image can be pulled again — so Enter
// may confirm, the same as running u and then a prune by hand.
func (a *App) confirmUpdate(project compose.Project) {
	a.overlay = NewConfirm("Update "+project.Name,
		"Pull images, apply changes, and prune dangling images for "+project.Name+"?",
		false, func(any) { a.updateStack(project) })
}

// updateStack brings a project up to date in one step: pull its images, apply
// whatever changed with up -d, and prune the dangling images the pull left
// behind.
//
// The chaining lives here rather than in the compose package, whose Action
// enum maps one-to-one onto subcommands: this layer already owns the runner
// and the daemon client the steps need, and an Action pretending to be one
// subcommand would push a sequence into argv building where it cannot fit.
// Both Compose commands run in a single terminal handover, so their output
// reads as one deployment. A failed pull stops everything — "apply changes"
// after images that never arrived would recreate containers onto whatever
// happened to be cached, which is not the update that was asked for.
func (a *App) updateStack(project compose.Project) {
	if !a.composeReady(project) {
		return
	}

	steps := [][]string{
		a.composeBinary.Command(project, compose.ActionPull),
		a.composeBinary.Command(project, compose.ActionUp),
	}
	completed, err := a.runner.RunSequence(steps, true)
	if err != nil {
		failed := compose.ActionPull
		if completed > 0 {
			failed = compose.ActionUp
		}
		a.setError("compose %s: %v", failed, err)
		a.refreshing = false
		a.refresh(context.Background())
		return
	}

	// Pruning goes through the API rather than a third child process: it
	// produces no output worth watching, and the reclaimed figure belongs in
	// the status line the user is returning to.
	a.setStatus("updated %s, pruning dangling images", project.Name)
	go func() {
		result, pruneErr := a.docker.PruneImages(context.Background())
		if pruneErr != nil {
			// The update itself succeeded, and the message must not
			// suggest otherwise.
			a.post(actionDone{err: fmt.Errorf(
				"updated %s, but pruning dangling images failed: %v", project.Name, pruneErr)})
			return
		}
		a.post(actionDone{message: fmt.Sprintf("updated %s: pruned %s, %s reclaimed",
			project.Name,
			Plural(result.Deleted, "dangling image", "dangling images"),
			FormatBytes(uint64(result.SpaceReclaimed)))})
		a.post(refreshRequested{})
	}()
	a.refreshing = false
	a.refresh(context.Background())
}
