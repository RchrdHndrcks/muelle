package docker

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

// The list endpoint reports Containers as -1 for every image, so usage has to
// come from somewhere else.
func TestImageListReportsNoUsageOfItsOwn(t *testing.T) {
	client, requested := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[{"Id":"sha256:aaa","RepoTags":["app:1"],"Size":100,"Containers":-1}]`))
	})

	images, err := client.Images(context.Background())
	if err != nil {
		t.Fatalf("Images: %v", err)
	}
	if images[0].Containers != -1 {
		t.Errorf("got %d; the daemon reports -1 here and callers must not trust it",
			images[0].Containers)
	}
	if !strings.Contains(requested.First(), "shared-size=true") {
		t.Errorf("got %q, want shared sizes requested so per-image reclaim is meaningful",
			requested.First())
	}
}

// An image referenced by a stopped container still cannot be removed, so
// stopped containers must count.
func TestImageUsageCountsStoppedContainersToo(t *testing.T) {
	containers := []Container{
		{ID: "1", ImageID: "sha256:app", State: "running"},
		{ID: "2", ImageID: "sha256:app", State: "exited"},
		{ID: "3", ImageID: "sha256:db", State: "exited"},
	}

	usage := ImageUsage(containers)

	if usage["sha256:app"] != 2 {
		t.Errorf("got %d for the app image, want both containers counted", usage["sha256:app"])
	}
	if usage["sha256:db"] != 1 {
		t.Errorf("got %d, want a stopped container to still pin its image", usage["sha256:db"])
	}
}

func TestImageUsageOmitsImagesWithNoContainers(t *testing.T) {
	usage := ImageUsage([]Container{{ID: "1", ImageID: "sha256:app"}})

	if _, present := usage["sha256:unused"]; present {
		t.Error("an image no container references should be absent from the map")
	}
}

func TestImageUsageHandlesNoContainers(t *testing.T) {
	if got := len(ImageUsage(nil)); got != 0 {
		t.Errorf("got %d entries, want an empty map", got)
	}
}

// Removing an image frees only the layers no other image needs.
func TestReclaimableExcludesSharedLayers(t *testing.T) {
	image := Image{Size: 1000, SharedSize: 400}

	if got := image.Reclaimable(); got != 600 {
		t.Errorf("got %d, want 600 (1000 less the 400 other images share)", got)
	}
}

// When the daemon did not compute the shared portion, overstating is the
// honest direction for a figure shown before a deletion.
func TestReclaimableFallsBackToFullSize(t *testing.T) {
	image := Image{Size: 1000, SharedSize: -1}

	if got := image.Reclaimable(); got != 1000 {
		t.Errorf("got %d, want the full size when sharing is unknown", got)
	}
}
