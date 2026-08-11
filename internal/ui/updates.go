package ui

import (
	"context"
	"fmt"
	"time"

	"github.com/RchrdHndrcks/muelle/internal/docker"
	"github.com/RchrdHndrcks/muelle/internal/registry"
)

// updateCheckTimeout bounds a whole update sweep. Each registry request
// already gives up after a few seconds; this is the backstop that keeps a
// sweep over a large image list from lingering all session.
const updateCheckTimeout = 2 * time.Minute

// imageChecker is the part of the registry checker the app uses. An interface
// so tests can exercise the key routing and result handling without a network.
type imageChecker interface {
	CheckAll(ctx context.Context, images []docker.Image) []registry.Result
}

// checkImageUpdates asks each image's registry whether its tag has moved on,
// in the background, and reports the verdicts back as one event.
//
// Deliberately on a key rather than on the refresh cycle: this is the one
// fetch in muelle that leaves the host, and polling public registries every
// few seconds is both slow and the way to meet Docker Hub's rate limits. The
// answer changes when someone pushes, not every three seconds.
func (a *App) checkImageUpdates(ctx context.Context) {
	if a.checkingUpdates {
		a.setStatus("update check already running")
		return
	}
	if len(a.images) == 0 {
		a.setStatus("no images to check")
		return
	}
	a.checkingUpdates = true
	a.setStatus("checking %s for updates...", Plural(len(a.images), "image", "images"))

	// Snapshot the list: the checker runs off the loop, and the model's
	// slice is replaced under it by every refresh.
	images := make([]docker.Image, len(a.images))
	copy(images, a.images)
	checker := a.updateChecker

	go func() {
		checkCtx, cancel := context.WithTimeout(ctx, updateCheckTimeout)
		defer cancel()
		a.post(updatesChecked{results: checker.CheckAll(checkCtx, images)})
	}()
}

// updatesChecked carries the verdicts of an update sweep.
type updatesChecked struct{ results []registry.Result }

func (e updatesChecked) apply(a *App) {
	a.checkingUpdates = false

	outdated, unknown := 0, 0
	for _, result := range e.results {
		// Merged rather than replaced, and keyed by reference rather than
		// image ID: a list refresh replaces the image structs wholesale, and
		// results that survive it are the point of caching them at all.
		if result.Reference != "" {
			a.updates[result.Reference] = result.Verdict
		}
		switch result.Verdict {
		case registry.VerdictOutdated:
			outdated++
		case registry.VerdictUnknown:
			unknown++
		}
	}

	summary := fmt.Sprintf("updates: %d of %d", outdated, len(e.results))
	if unknown > 0 {
		summary += fmt.Sprintf(", %d unknown", unknown)
	}
	a.setStatus("%s", summary)
}

// outdatedImages counts the listed images whose registry holds something
// newer, for the status bar.
func (a *App) outdatedImages() int {
	count := 0
	for _, image := range a.images {
		if a.updates[image.Tag()] == registry.VerdictOutdated {
			count++
		}
	}
	return count
}
