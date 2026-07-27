package ui

import (
	"strings"
	"testing"

	"github.com/RchrdHndrcks/muelle/internal/docker"
)

func withImages(t *testing.T) *App {
	t.Helper()
	app := newTestApp(t)
	app.images = []docker.Image{
		{ID: "sha256:used", RepoTags: []string{"app:1"}, Size: 500, SharedSize: 100},
		{ID: "sha256:idle", RepoTags: []string{"old:2"}, Size: 800, SharedSize: 200},
		{ID: "sha256:dangling", Size: 300, SharedSize: 0},
	}
	app.imageUsage = map[string]int{"sha256:used": 2}
	app.SetView(ViewImages)
	return app
}

// The panel reports a reclaimable total but names no images, which leaves the
// figure impossible to act on.
func TestImagesViewMarksUnusedImages(t *testing.T) {
	app := withImages(t)

	rendered := strings.Join(app.renderImages(140, 20), "\n")

	if !strings.Contains(rendered, "unused") {
		t.Errorf("removal candidates should be marked:\n%s", rendered)
	}
	if !strings.Contains(rendered, "2 used") {
		t.Errorf("pinned images should show their container count:\n%s", rendered)
	}
}

// Marking every image "unused" before the container list arrives would invite
// deletions the daemon then refuses.
func TestImagesViewWithholdsJudgementBeforeContainersLoad(t *testing.T) {
	app := withImages(t)
	app.imageUsage = nil

	rendered := strings.Join(app.renderImages(140, 20), "\n")

	if strings.Contains(rendered, "unused") {
		t.Errorf("nothing should be called unused before usage is known:\n%s", rendered)
	}
}

func TestUnusedFilterNarrowsToCandidates(t *testing.T) {
	app := withImages(t)

	app.unusedImagesOnly = true
	images := app.filteredImages()

	if len(images) != 2 {
		t.Fatalf("got %d images, want only the two no container uses", len(images))
	}
	for _, image := range images {
		if app.imageInUse(image) {
			t.Errorf("%s is in use and should have been filtered out", image.Tag())
		}
	}
}

func TestUnusedFilterCombinesWithTextFilter(t *testing.T) {
	app := withImages(t)
	app.unusedImagesOnly = true
	app.filter = "old"

	images := app.filteredImages()

	if len(images) != 1 || images[0].Tag() != "old:2" {
		t.Errorf("got %v, want both filters applied", images)
	}
}

func TestUnusedTotalsMatchWhatWouldBeFreed(t *testing.T) {
	app := withImages(t)

	count, reclaimable := app.unusedImages()

	if count != 2 {
		t.Errorf("got %d unused, want 2", count)
	}
	// (800-200) + (300-0), excluding layers other images share.
	if reclaimable != 900 {
		t.Errorf("got %d, want 900 net of shared layers", reclaimable)
	}
}

func TestUnusedKeyTogglesTheFilter(t *testing.T) {
	app := withImages(t)

	press(app, runeKey('u'))
	if !app.unusedImagesOnly {
		t.Fatal("u should narrow the list to unused images")
	}
	if !strings.Contains(app.status.text, "reclaimable") {
		t.Errorf("got %q, want the reclaimable total reported", app.status.text)
	}

	press(app, runeKey('u'))
	if app.unusedImagesOnly {
		t.Error("u should toggle back to every image")
	}
}

// Toggling changes the list under the cursor, which must not be left pointing
// past the end.
func TestUnusedToggleResetsSelection(t *testing.T) {
	app := withImages(t)
	app.selection[ViewImages] = 2

	press(app, runeKey('u'))

	if app.selected() != 0 {
		t.Errorf("got selection %d, want it reset onto the narrowed list", app.selected())
	}
}

// A stopped container still pins its image; offering it for removal would fail
// at the daemon.
func TestImagePinnedByStoppedContainerIsNotUnused(t *testing.T) {
	app := withImages(t)
	app.containers = []docker.Container{{ID: "c", ImageID: "sha256:idle", State: "exited"}}
	app.imageUsage = docker.ImageUsage(app.containers)

	if !app.imageInUse(app.images[1]) {
		t.Error("an image held by a stopped container is in use")
	}
}

func TestStatusBarNamesTheReclaimableTotal(t *testing.T) {
	app := withImages(t)

	bar := app.renderStatusBar(160)

	if !strings.Contains(bar, "2 unused") {
		t.Errorf("got %q, want the unused count", bar)
	}
	if !strings.Contains(bar, "reclaimable") {
		t.Errorf("got %q, want the reclaimable total tied to the panel's figure", bar)
	}
}
