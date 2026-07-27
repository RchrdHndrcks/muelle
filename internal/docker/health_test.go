package docker

import (
	"context"
	"encoding/json"
	"net/http"
	"sync/atomic"
	"testing"
)

// The list endpoint has no health field; it appends health to the status text.
func TestHealthParsedFromStatusText(t *testing.T) {
	cases := map[string]Health{
		"Up 2 hours (healthy)":            HealthHealthy,
		"Up 5 minutes (unhealthy)":        HealthUnhealthy,
		"Up 3 seconds (health: starting)": HealthStarting,
		"Up 8 weeks":                      HealthNone,
		"Exited (0) 2 days ago":           HealthNone,
		"Created":                         HealthNone,
		"":                                HealthNone,
	}

	for status, want := range cases {
		got := Container{Status: status}.Health()
		if got != want {
			t.Errorf("Status %q gave health %q, want %q", status, got, want)
		}
	}
}

// "Exited (0)" contains no health marker and must not be mistaken for one.
func TestHealthNotInferredFromExitStatus(t *testing.T) {
	if got := (Container{Status: "Exited (137) 5 minutes ago", State: "exited"}).Health(); got != HealthNone {
		t.Errorf("got %q, want no health for a container without a healthcheck", got)
	}
}

func TestUnhealthyIsDistinctFromStopped(t *testing.T) {
	unhealthy := Container{Status: "Up 5 minutes (unhealthy)", State: "running"}
	stopped := Container{Status: "Exited (1) 5 minutes ago", State: "exited"}

	if !unhealthy.Unhealthy() {
		t.Error("a failing healthcheck should report unhealthy")
	}
	if stopped.Unhealthy() {
		t.Error("a stopped container is not unhealthy; it is stopped")
	}
}

// A container crash-looping every thirty seconds otherwise looks identical to
// one that has been up for a month.
func TestRestartingIsDetected(t *testing.T) {
	flapping := Container{State: "restarting", Status: "Restarting (1) 5 seconds ago"}

	if !flapping.Restarting() {
		t.Error("a restarting container should be recognised")
	}
	if (Container{State: "running"}).Restarting() {
		t.Error("a running container is not restarting")
	}
}

func TestNeedsAttentionSelectsOnlyTroubledContainers(t *testing.T) {
	troubled := []Container{
		{State: "restarting"},
		{State: "dead"},
		{State: "running", Status: "Up 2 minutes (unhealthy)"},
	}
	for _, c := range troubled {
		if !c.NeedsAttention() {
			t.Errorf("%+v should be flagged for attention", c)
		}
	}

	fine := []Container{
		{State: "running", Status: "Up 2 hours (healthy)"},
		{State: "running", Status: "Up 2 hours"},
		{State: "exited", Status: "Exited (0) 1 hour ago"},
		{State: "created", Status: "Created"},
	}
	for _, c := range fine {
		if c.NeedsAttention() {
			t.Errorf("%+v should not trigger an extra inspect", c)
		}
	}
}

func TestRestartCountsFetchesPerContainer(t *testing.T) {
	var calls atomic.Int32
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		json.NewEncoder(w).Encode(map[string]any{"Id": "x", "RestartCount": 7})
	})

	counts := client.RestartCounts(context.Background(), []string{"a", "b"})

	if len(counts) != 2 {
		t.Fatalf("got %v, want a count for each container", counts)
	}
	if counts["a"] != 7 {
		t.Errorf("got %d, want the restart count decoded", counts["a"])
	}
	if got := calls.Load(); got != 2 {
		t.Errorf("made %d requests, want one per container", got)
	}
}

// A container that exits mid-sweep is ordinary; it should be absent rather
// than failing the whole refresh.
func TestRestartCountsSkipsContainersThatDisappear(t *testing.T) {
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"message":"no such container"}`))
	})

	counts := client.RestartCounts(context.Background(), []string{"gone"})

	if len(counts) != 0 {
		t.Errorf("got %v, want the missing container omitted", counts)
	}
}

func TestRestartCountsHandlesEmptyInput(t *testing.T) {
	client, requested := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("no request should be made for an empty list")
	})

	counts := client.RestartCounts(context.Background(), nil)

	if len(counts) != 0 || requested.Len() != 0 {
		t.Errorf("got %v after %d requests, want neither", counts, requested.Len())
	}
}
