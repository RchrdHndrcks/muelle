package ui

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"

	"github.com/RchrdHndrcks/muelle/internal/compose"
	"github.com/RchrdHndrcks/muelle/internal/docker"
)

// openEditMenu offers the files that define a project, opening the only one
// straight away when there is no choice to make.
func (a *App) openEditMenu(project compose.Project) {
	if a.runner == nil {
		a.setError("editing is unavailable in this mode")
		return
	}
	files := compose.Files(project)
	switch len(files) {
	case 0:
		a.setError("no compose file known for %s", project.Name)
	case 1:
		a.editFile(project, files[0])
	default:
		items := make([]MenuItem, 0, len(files))
		for _, file := range files {
			items = append(items, MenuItem{Label: file.Name, Detail: file.Path, Value: file})
		}
		a.overlay = NewMenu("Edit: "+project.Name, items, func(value any) {
			if file, ok := value.(compose.File); ok {
				a.editFile(project, file)
			}
		})
	}
}

// editFile hands the terminal to the user's editor, then decides what to do
// with whatever came back.
func (a *App) editFile(project compose.Project, file compose.File) {
	before, err := os.ReadFile(file.Path)
	if err != nil {
		a.setError("read %s: %v", file.Name, err)
		return
	}
	// A non-zero exit from the editor is not a failure worth reporting: vim
	// exits 1 in ordinary situations and $EDITOR can be anything at all.
	// What the file now contains is the only trustworthy signal. An editor
	// that could not be started is a different matter.
	if err := a.runner.Run(EditorArgv(a.config.Editor, file.Path), false); err != nil && !ran(err) {
		a.setError("editor: %v", err)
		return
	}
	a.reviewEdit(project, file, before)
}

// reviewEdit validates a changed file and offers to apply it.
func (a *App) reviewEdit(project compose.Project, file compose.File, before []byte) {
	after, err := os.ReadFile(file.Path)
	if err != nil {
		a.setError("%s can no longer be read: %v", file.Name, err)
		return
	}
	if bytes.Equal(before, after) {
		a.setStatus("%s unchanged", file.Name)
		return
	}
	if !a.composeBinary.Available() {
		a.setStatus("%s saved, but compose is not installed to apply it", file.Name)
		return
	}

	// Validating first inverts the cost of a typo. "config" renders the
	// project in memory and touches no container, so a mistake is reported
	// in milliseconds instead of once half the stack is already down.
	output, err := a.runner.Capture(a.composeBinary.Command(project, compose.ActionConfig))
	if err != nil {
		a.overlay = NewConfirm("Invalid configuration",
			diagnostic(output)+"\n\nBack to the editor?", false,
			func(any) { a.editFile(project, file) })
		return
	}
	a.offerApply(project, file)
}

// offerApply asks which form of "up" should carry the change.
//
// Plain "up" is right almost always: Compose compares each service's config
// hash against the rendered configuration and replaces only what changed. The
// forced variant is there for the case where it decides nothing changed and
// the user knows better.
func (a *App) offerApply(project compose.Project, file compose.File) {
	items := []MenuItem{
		{
			Label:  "apply (recreate what changed)",
			Detail: strings.Join(a.composeBinary.Command(project, compose.ActionUp), " "),
			Value:  compose.ActionUp,
		},
		{
			Label:  "force (recreate every service)",
			Detail: strings.Join(a.composeBinary.Command(project, compose.ActionRecreate), " "),
			Value:  compose.ActionRecreate,
		},
	}
	a.overlay = NewMenu(file.Name+" changed", items, func(value any) {
		if action, ok := value.(compose.Action); ok {
			a.runCompose(project, action)
		}
	})
}

// openRestartMenu offers recreating alongside restarting, for containers where
// recreating is possible.
//
// The two are not variations of one action. A restart starts the same
// container, whose environment and image were fixed when it was created, so it
// cannot apply an edited compose file; recreating replaces it. Anywhere the
// second is not available, "r" restarts directly — a menu that always appears
// is friction, and one offering an action that would fail is worse.
func (a *App) openRestartMenu(ctx context.Context, container docker.Container) {
	project, ok := a.recreatableProject(container)
	if !ok {
		a.restartContainer(ctx, container)
		return
	}
	service := container.Service()
	items := []MenuItem{
		{
			Label:  "restart (does not apply config changes)",
			Detail: "same container, kept as it was created",
			Value:  compose.ActionRestart,
		},
		{
			Label:  "recreate (apply current config)",
			Detail: strings.Join(a.composeBinary.Command(project, compose.ActionRecreate, service), " "),
			Value:  compose.ActionRecreate,
		},
	}
	a.overlay = NewMenu(container.Name(), items, func(value any) {
		if value == compose.ActionRecreate {
			a.runCompose(project, compose.ActionRecreate, service)
			return
		}
		a.restartContainer(ctx, container)
	})
}

// restartContainer restarts through the Engine API, which needs no subprocess
// and no handover of the terminal.
func (a *App) restartContainer(ctx context.Context, container docker.Container) {
	name := container.Name()
	a.runAction(ctx, "restarted "+name, func(c context.Context) error {
		return a.docker.Restart(c, container.ID, a.config.StopTimeout)
	})
}

// recreatableProject returns the project a container can be recreated in, if
// everything recreating needs is present.
func (a *App) recreatableProject(container docker.Container) (compose.Project, bool) {
	if a.runner == nil || !a.composeBinary.Available() {
		return compose.Project{}, false
	}
	if container.Project() == "" || container.Service() == "" {
		return compose.Project{}, false
	}
	for _, project := range a.projects {
		if project.Name != container.Project() {
			continue
		}
		// Labels stamped by an older Compose can omit the paths, leaving
		// nothing to run the command against.
		if len(project.ConfigFiles) == 0 && project.WorkingDir == "" {
			return compose.Project{}, false
		}
		return project, true
	}
	return compose.Project{}, false
}

// diagnosticLines is how much of Compose's complaint is shown. Enough for the
// error and its context, not so much that the overlay becomes a pager.
const diagnosticLines = 10

// diagnostic trims a command's output to what fits in an overlay.
func diagnostic(output string) string {
	var lines []string
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if len(lines) == diagnosticLines {
			return strings.Join(lines, "\n") + "\n…"
		}
		lines = append(lines, strings.TrimRight(line, " "))
	}
	if len(lines) == 0 {
		return "compose rejected the configuration without saying why"
	}
	return strings.Join(lines, "\n")
}

// ran reports whether a command started and chose its own exit status, as
// opposed to failing to start at all.
func ran(err error) bool {
	var exitErr *exec.ExitError
	return errors.As(err, &exitErr)
}
