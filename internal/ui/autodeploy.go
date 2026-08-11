package ui

import (
	"time"

	"github.com/RchrdHndrcks/muelle/internal/autodeploy"
	"github.com/RchrdHndrcks/muelle/internal/compose"
	"github.com/RchrdHndrcks/muelle/internal/config"
)

// The TUI's half of automatic deployments is deliberately small: enrol a
// project, show what the daemon did. The deploying itself belongs to exactly
// one process — the headless daemon started with "muelle -deploy" — because
// two deployers racing each other is the failure the design rules out. So
// everything here either edits the configuration the daemon reads, or renders
// the state file the daemon writes.

// SetDeployStatePath tells the app where the daemon's state file lives. Empty
// leaves the AUTO column showing enrolment only, with no outcomes.
func (a *App) SetDeployStatePath(path string) {
	a.deployStatePath = path
}

// refreshDeployState re-reads the daemon's state file, off the loop like
// every other fetch. The file is a few hundred bytes and LoadState tolerates
// it being missing or torn, so this is the cheapest refresh the app does.
func (a *App) refreshDeployState() {
	if a.deployStatePath == "" {
		return
	}
	path := a.deployStatePath
	go func() {
		a.post(deployStateLoaded{state: autodeploy.LoadState(path)})
	}()
}

// deployStateLoaded carries a fresh read of the daemon's state file.
type deployStateLoaded struct{ state autodeploy.State }

func (e deployStateLoaded) apply(a *App) { a.deployState = e.state }

// toggleAutoDeploy enrols or withdraws the selected project, persisted so the
// daemon — a separate process reading the same file — sees the change.
func (a *App) toggleAutoDeploy(project compose.Project) {
	enabled := !a.config.AutoDeploy.Enabled(project.Name)
	a.config.AutoDeploy.SetEnabled(project.Name, enabled)

	// The status line carries the reminder that matters: nothing deploys
	// until the daemon is running.
	if enabled {
		a.setStatus("auto deploy on for %s — applied by muelle -deploy", project.Name)
	} else {
		a.setStatus("auto deploy off for %s", project.Name)
	}
	a.remember(func(c *config.Config) { c.AutoDeploy.SetEnabled(project.Name, enabled) })
}

// autoDeployCell renders the AUTO column for a project: nothing when not
// enrolled, "auto" while the daemon has not reported, and the last outcome
// with its age once it has — "auto ok 12m", "auto fail 3m".
func (a *App) autoDeployCell(project compose.Project, now time.Time) string {
	if !a.config.AutoDeploy.Enabled(project.Name) {
		return ""
	}
	style := a.screen.Style

	outcome, reported := a.deployState.Projects[project.Name]
	if !reported {
		// Enrolled but never acted on: either the daemon has not run yet
		// or it is not running at all. Muted, because there is nothing to
		// celebrate or worry about yet.
		return style(styleMuted, "auto")
	}
	if outcome.Failed() {
		return style(styleError, "auto fail "+shortAge(now.Sub(outcome.Time)))
	}
	return style(styleSuccess, "auto ok "+shortAge(now.Sub(outcome.Time)))
}

// shortAge renders an outcome's age in one compact unit. FormatDuration's
// "just now" is the right voice for a container list but too wide for this
// column, where the whole cell must fit beside a verdict.
func shortAge(d time.Duration) string {
	if d < time.Minute {
		return "now"
	}
	return FormatDuration(d)
}
