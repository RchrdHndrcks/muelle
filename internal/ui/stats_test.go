package ui

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/RchrdHndrcks/muelle/internal/config"
	"github.com/RchrdHndrcks/muelle/internal/docker"
	"github.com/RchrdHndrcks/muelle/internal/tui"
)

// newStatsApp builds an app with the stats columns enabled, unlike
// newTestApp, whose fake daemon serves none.
func newStatsApp(t *testing.T) *App {
	t.Helper()
	cfg := config.Default()
	cfg.ComposeDirs = nil
	screen := tui.NewScreen(&bytes.Buffer{}, 120, 30, false)
	return New(cfg, fakeDaemon(t), screen, nil)
}

// The stats off-switch is the streamer never being created, so no code path
// can open a connection by accident.
func TestStatsOffSwitchCreatesNoStreamer(t *testing.T) {
	if app := newTestApp(t); app.statStreams != nil {
		t.Error("stats are off, no streamer should exist")
	}
	if app := newStatsApp(t); app.statStreams == nil {
		t.Error("stats are on, the streamer should exist")
	}
}

// A streamed sample lands in the model and the host panel's aggregates move
// with it.
func TestStatSampleUpdatesModelAndTotals(t *testing.T) {
	app := newStatsApp(t)
	app.containers = []docker.Container{
		{ID: "aaa", State: "running", Status: "Up 1 hour"},
		{ID: "bbb", State: "running", Status: "Up 1 hour"},
	}

	statSampled{id: "aaa", stat: docker.Stat{CPUPercent: 10, MemUsage: 1 << 20}}.apply(app)
	statSampled{id: "bbb", stat: docker.Stat{CPUPercent: 5, MemUsage: 1 << 20}}.apply(app)

	if got := app.stats["aaa"].CPUPercent; got != 10 {
		t.Errorf("got %v%% for aaa, want the sample applied", got)
	}
	if app.metrics.CPUPercent != 15 || app.metrics.MemBytes != 2<<20 {
		t.Errorf("got %v%% / %d bytes, want the samples summed", app.metrics.CPUPercent, app.metrics.MemBytes)
	}

	// A newer sample replaces the entry rather than accumulating.
	statSampled{id: "aaa", stat: docker.Stat{CPUPercent: 2, MemUsage: 1 << 20}}.apply(app)
	if app.metrics.CPUPercent != 7 {
		t.Errorf("got %v%%, want totals recomputed from the latest samples", app.metrics.CPUPercent)
	}
}

// A sample may still be in flight when its container stops; applying it would
// resurrect a row's figures after the stream was torn down.
func TestLateSampleForAStoppedContainerIsDropped(t *testing.T) {
	app := newStatsApp(t)
	app.containers = []docker.Container{
		{ID: "aaa", State: "exited", Status: "Exited (0) 1 second ago"},
	}

	statSampled{id: "aaa", stat: docker.Stat{CPUPercent: 10}}.apply(app)
	statSampled{id: "gone", stat: docker.Stat{CPUPercent: 10}}.apply(app)

	if len(app.stats) != 0 {
		t.Errorf("got %d samples, want none for stopped or vanished containers", len(app.stats))
	}
}

// A refreshed list without a container must also drop its last sample: the
// aggregate figures would otherwise keep counting a container that is gone.
func TestRefreshedListPrunesStaleSamples(t *testing.T) {
	app := newStatsApp(t)
	app.stats = map[string]docker.Stat{
		"kept":    {CPUPercent: 10, MemUsage: 1 << 20},
		"stopped": {CPUPercent: 5, MemUsage: 1 << 20},
	}

	containersLoaded{containers: []docker.Container{
		{ID: "kept", State: "running", Status: "Up 1 hour"},
		{ID: "stopped", State: "exited", Status: "Exited (0) 1 second ago"},
	}}.apply(app)

	if _, present := app.stats["stopped"]; present {
		t.Error("a stopped container's sample should be pruned")
	}
	if _, present := app.stats["kept"]; !present {
		t.Error("a running container's sample should survive the refresh")
	}
	if app.metrics.CPUPercent != 10 || app.metrics.MemBytes != 1<<20 {
		t.Errorf("got %v%% / %d bytes, want totals recomputed after pruning", app.metrics.CPUPercent, app.metrics.MemBytes)
	}
}

// Dump mode renders one frame and exits, so it samples stats with the
// one-shot request rather than waiting on a stream.
func TestLoadOnceSamplesStatsOneShot(t *testing.T) {
	var (
		mu           sync.Mutex
		statsQueries []string
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/containers/") && strings.HasSuffix(r.URL.Path, "/stats"):
			mu.Lock()
			statsQueries = append(statsQueries, r.URL.Query().Get("stream"))
			mu.Unlock()
			fmt.Fprintln(w, `{
				"cpu_stats":    {"cpu_usage": {"total_usage": 12}, "system_cpu_usage": 110, "online_cpus": 4},
				"precpu_stats": {"cpu_usage": {"total_usage": 10}, "system_cpu_usage": 100},
				"memory_stats": {"usage": 2048, "limit": 4096}
			}`)
		case strings.HasPrefix(r.URL.Path, "/containers/json"):
			fmt.Fprintln(w, `[{"Id": "aaa", "Names": ["/web"], "State": "running", "Status": "Up 1 hour"}]`)
		default:
			fmt.Fprintln(w, `{}`)
		}
	}))
	defer server.Close()

	client, err := docker.New(server.URL)
	if err != nil {
		t.Fatalf("docker.New: %v", err)
	}
	cfg := config.Default()
	cfg.ComposeDirs = nil
	app := New(cfg, client, tui.NewScreen(&bytes.Buffer{}, 120, 30, false), nil)

	if err := app.LoadOnce(context.Background()); err != nil {
		t.Fatalf("LoadOnce: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(statsQueries) != 1 || statsQueries[0] != "false" {
		t.Errorf("got stats queries %v, want exactly one with stream=false", statsQueries)
	}
	// Delta 2 over a system delta of 10, times 4 CPUs.
	if got := app.stats["aaa"].CPUPercent; got != 80.0 {
		t.Errorf("got %v%%, want 80%% from the one-shot sample", got)
	}
}
