package docker

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"testing"
)

// pruneServer records which prune endpoints were called.
func pruneServer(t *testing.T, fail map[string]bool) (*Client, func() []string) {
	t.Helper()
	var (
		mu     sync.Mutex
		called []string
	)
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		called = append(called, r.URL.Path)
		mu.Unlock()

		if fail[r.URL.Path] {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte(`{"message":"prune failed"}`))
			return
		}
		switch r.URL.Path {
		case "/containers/prune":
			w.Write([]byte(`{"ContainersDeleted":["a","b"],"SpaceReclaimed":100}`))
		case "/images/prune":
			w.Write([]byte(`{"ImagesDeleted":[{"Deleted":"x"}],"SpaceReclaimed":2000}`))
		case "/networks/prune":
			w.Write([]byte(`{"NetworksDeleted":["n1"]}`))
		case "/build/prune":
			w.Write([]byte(`{"SpaceReclaimed":500}`))
		case "/volumes/prune":
			w.Write([]byte(`{"VolumesDeleted":["v1"],"SpaceReclaimed":9000}`))
		}
	})
	return client, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), called...)
	}
}

// The API has no single prune endpoint; each object type has its own.
func TestSystemPruneCallsEachEndpoint(t *testing.T) {
	client, called := pruneServer(t, nil)

	result := client.SystemPrune(context.Background(), PruneScope{})

	endpoints := strings.Join(called(), " ")
	for _, want := range []string{"/containers/prune", "/images/prune", "/networks/prune", "/build/prune"} {
		if !strings.Contains(endpoints, want) {
			t.Errorf("%s was never called; got %v", want, called())
		}
	}
	if result.Containers != 2 || result.Images != 1 || result.Networks != 1 {
		t.Errorf("got %+v, want each count decoded", result)
	}
	if result.SpaceReclaimed != 2600 {
		t.Errorf("got %d reclaimed, want 100+2000+500", result.SpaceReclaimed)
	}
}

// Volumes hold data that cannot be rebuilt, so they must never go unless
// explicitly asked for.
func TestSystemPruneSkipsVolumesUnlessRequested(t *testing.T) {
	client, called := pruneServer(t, nil)

	client.SystemPrune(context.Background(), PruneScope{})

	if strings.Contains(strings.Join(called(), " "), "/volumes/prune") {
		t.Error("volumes were pruned without being asked for")
	}
}

func TestSystemPruneIncludesVolumesWhenRequested(t *testing.T) {
	client, called := pruneServer(t, nil)

	result := client.SystemPrune(context.Background(), PruneScope{Volumes: true})

	if !strings.Contains(strings.Join(called(), " "), "/volumes/prune") {
		t.Fatal("volumes should have been pruned")
	}
	if result.Volumes != 1 {
		t.Errorf("got %d volumes, want 1", result.Volumes)
	}
	if result.SpaceReclaimed != 11600 {
		t.Errorf("got %d, want the volume space included", result.SpaceReclaimed)
	}
}

// Containers must go first so the images and networks they were holding become
// unreferenced and can be removed in the same pass.
func TestSystemPrunePrunesContainersBeforeImages(t *testing.T) {
	client, called := pruneServer(t, nil)

	client.SystemPrune(context.Background(), PruneScope{})

	order := called()
	containers, images := -1, -1
	for i, path := range order {
		switch path {
		case "/containers/prune":
			containers = i
		case "/images/prune":
			images = i
		}
	}
	if containers < 0 || images < 0 || containers > images {
		t.Errorf("got order %v, want containers pruned before images", order)
	}
}

// A prune that stops halfway leaves the user unsure what state the host is in.
func TestSystemPruneContinuesAfterAStepFails(t *testing.T) {
	client, called := pruneServer(t, map[string]bool{"/images/prune": true})

	result := client.SystemPrune(context.Background(), PruneScope{})

	if len(result.Errors) != 1 {
		t.Fatalf("got %d errors, want the failure recorded", len(result.Errors))
	}
	if !strings.Contains(strings.Join(called(), " "), "/networks/prune") {
		t.Error("later steps should still run after one fails")
	}
	if result.Containers != 2 {
		t.Errorf("got %d containers, want the successful step still counted", result.Containers)
	}
}

func TestSystemPruneAllImagesUsesTheInvertedFilter(t *testing.T) {
	var query string
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/images/prune" {
			query = r.URL.RawQuery
		}
		w.Write([]byte(`{}`))
	})

	client.SystemPrune(context.Background(), PruneScope{AllImages: true})

	if !strings.Contains(query, "dangling") {
		t.Errorf("got query %q, want the dangling filter for -a behaviour", query)
	}
}

func TestSummaryDescribesWhatWasRemoved(t *testing.T) {
	result := SystemPruneResult{Containers: 3, Images: 1, Networks: 2, SpaceReclaimed: 2 << 30}

	summary := result.Summary()

	for _, want := range []string{"3 containers", "1 image", "2 networks", "2.0GiB"} {
		if !strings.Contains(summary, want) {
			t.Errorf("summary %q is missing %q", summary, want)
		}
	}
}

func TestSummaryHandlesNothingToPrune(t *testing.T) {
	if got := (SystemPruneResult{}).Summary(); got != "nothing to prune" {
		t.Errorf("got %q, want it to say nothing was removed", got)
	}
}
