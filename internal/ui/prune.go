package ui

import (
	"context"
	"strings"
	"time"

	"github.com/RchrdHndrcks/muelle/internal/docker"
)

// openSystemPruneMenu offers the two prune scopes.
//
// A menu rather than a single confirmation, because the difference between
// them is the difference between reclaiming disk and losing data. Volumes are
// the only prune target that cannot be rebuilt or re-pulled, so choosing to
// include them is a separate, deliberate act rather than a checkbox on a
// confirmation the user is already saying yes to.
func (a *App) openSystemPruneMenu(ctx context.Context) {
	reclaimable := ""
	if a.metrics.DiskKnown {
		if size := a.metrics.Disk.Reclaimable(); size > 0 {
			reclaimable = " (about " + FormatBytes(uint64(size)) + ")"
		}
	}

	items := []MenuItem{
		{
			Label:  "prune unused data" + reclaimable,
			Detail: "stopped containers, unused networks, dangling images, build cache",
			Value:  docker.PruneScope{},
		},
		{
			Label:  "prune unused data and all unused images",
			Detail: "as above, plus every image no container uses",
			Value:  docker.PruneScope{AllImages: true},
		},
		{
			Label:  "prune everything including volumes",
			Detail: "as above, plus unreferenced volumes — DATA WILL BE LOST",
			Value:  docker.PruneScope{AllImages: true, Volumes: true},
		},
	}

	a.overlay = NewMenu("System prune", items, func(value any) {
		scope, ok := value.(docker.PruneScope)
		if !ok {
			return
		}
		a.confirmSystemPrune(ctx, scope)
	})
}

// confirmSystemPrune asks before running, spelling out what will go.
func (a *App) confirmSystemPrune(ctx context.Context, scope docker.PruneScope) {
	targets := []string{"stopped containers", "unused networks", "build cache"}
	if scope.AllImages {
		targets = append(targets, "all unused images")
	} else {
		targets = append(targets, "dangling images")
	}
	if scope.Volumes {
		targets = append(targets, "unreferenced volumes and their data")
	}

	prompt := "Remove " + strings.Join(targets, ", ") + "?"
	if scope.Volumes {
		prompt += " Volume data cannot be recovered."
	}

	a.confirm("System prune", prompt, func() { a.runSystemPrune(ctx, scope) })
}

// runSystemPrune performs the prune and reports what it removed.
func (a *App) runSystemPrune(ctx context.Context, scope docker.PruneScope) {
	a.setStatus("pruning...")
	go func() {
		pruneCtx, cancel := context.WithTimeout(ctx, prunePatience)
		defer cancel()

		result := a.docker.SystemPrune(pruneCtx, scope)
		a.post(systemPruned{result: result})
		a.post(refreshRequested{})
	}()
}

// prunePatience bounds a system prune. Reclaiming space on a large host takes
// minutes, far longer than any other call this tool makes.
const prunePatience = 10 * time.Minute

// zeroTime forces the disk measurement to be considered stale.
var zeroTime time.Time

// systemPruned reports the outcome of a system prune.
type systemPruned struct{ result docker.SystemPruneResult }

func (e systemPruned) apply(a *App) {
	// Force the next tick to re-measure: a prune is precisely when the disk
	// figures change, and waiting a minute to reflect that would make the
	// panel look broken.
	a.diskMeasuredAt = zeroTime

	summary := e.result.Summary()
	if len(e.result.Errors) > 0 {
		// Report what was reclaimed as well as what failed: a partial
		// prune still removed things, and hiding that would leave the
		// user unsure what state the host is in.
		a.setError("%s; %d step(s) failed: %v", summary, len(e.result.Errors), e.result.Errors[0])
		return
	}
	a.setStatus("%s", summary)
}
