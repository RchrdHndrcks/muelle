package ui

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/RchrdHndrcks/muelle/internal/config"
	"github.com/RchrdHndrcks/muelle/internal/docker"
	"github.com/RchrdHndrcks/muelle/internal/probe"
	"github.com/RchrdHndrcks/muelle/internal/tui"
)

// The point of probing is that the answer shows up where the user already
// looks for health, in the same glyphs as everything else.
func TestStateCellShowsWhatTheProbeFound(t *testing.T) {
	cases := map[string]struct {
		result probe.Result
		want   string
	}{
		"answered 200":     {probe.Result{Healthy: true, Status: 200}, "✓"},
		"answered 503":     {probe.Result{Status: 503}, "✗"},
		"refused the call": {probe.Result{Err: errors.New("connection refused")}, "✗"},
	}

	for name, c := range cases {
		app := newTestApp(t)
		container := docker.Container{ID: "abc", State: "running", Status: "Up 2 hours"}
		app.probeResults = map[string]probe.Result{"abc": c.result}

		if cell := app.stateCell(container); !strings.Contains(cell, c.want) {
			t.Errorf("%s: got %q, want it to contain %q", name, cell, c.want)
		}
	}
}

// Setting the variable is a deliberate instruction to muelle, made by someone
// who has a reason to want the endpoint asked rather than the image's own
// healthcheck. It wins.
func TestTheProbeOutranksTheImagesOwnHealthcheck(t *testing.T) {
	app := newTestApp(t)
	container := docker.Container{ID: "abc", State: "running", Status: "Up 2 hours (healthy)"}
	app.probeResults = map[string]probe.Result{"abc": {Status: 503}}

	cell := app.stateCell(container)

	if strings.Contains(cell, "✓") {
		t.Errorf("got %q, want the probe's verdict rather than the image's", cell)
	}
	if !strings.Contains(cell, "✗") {
		t.Errorf("got %q, want the failing probe reported", cell)
	}
}

// A container nobody asked muelle to probe must look exactly as it did before
// the feature existed.
func TestStateCellKeepsDockersHealthWhenThereIsNoProbe(t *testing.T) {
	app := newTestApp(t)
	container := docker.Container{ID: "abc", State: "running", Status: "Up 2 hours (healthy)"}

	if cell := app.stateCell(container); !strings.Contains(cell, "✓") {
		t.Errorf("got %q, want the healthcheck the daemon reported", cell)
	}
}

// Results arrive from a background sweep, one refresh behind the list. A
// container that has gone must not leave its verdict behind for whatever
// reuses its place on screen.
func TestProbeResultsReplaceTheirPredecessors(t *testing.T) {
	app := newTestApp(t)
	app.probeResults = map[string]probe.Result{"gone": {Healthy: true}}

	probesSwept{results: map[string]probe.Result{"abc": {Healthy: true}}}.apply(app)

	if _, stale := app.probeResults["gone"]; stale {
		t.Error("a sweep must replace what the last one found, not add to it")
	}
	if !app.probeResults["abc"].Healthy {
		t.Error("the new results must be kept")
	}
}

// -dump renders through LoadOnce rather than the refresh cycle, and it is
// documented as using the same model as the interactive app. A health column
// that only fills in interactively would make that untrue.
func TestLoadOnceProbesToo(t *testing.T) {
	health := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer health.Close()
	port := health.URL[strings.LastIndex(health.URL, ":")+1:]

	daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/json") && strings.Contains(r.URL.Path, "/containers/abc"):
			json.NewEncoder(w).Encode(map[string]any{
				"Id":     "abc",
				"Config": map[string]any{"Env": []string{"MUELLE_HEALTH=" + port + "/health"}},
				"NetworkSettings": map[string]any{
					"Networks": map[string]any{"bridge": map[string]any{"IPAddress": "127.0.0.1"}},
				},
			})
		case strings.HasPrefix(r.URL.Path, "/containers/json"):
			json.NewEncoder(w).Encode([]map[string]any{
				{"Id": "abc", "Names": []string{"/api"}, "State": "running", "Status": "Up 1 hour"},
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer daemon.Close()

	client, err := docker.New(daemon.URL)
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.ComposeDirs, cfg.Stats = nil, false
	app := New(cfg, client, tui.NewScreen(&bytes.Buffer{}, 120, 30, false), nil)

	if err := app.LoadOnce(context.Background()); err != nil {
		t.Fatalf("LoadOnce: %v", err)
	}

	if !app.probeResults["abc"].Healthy {
		t.Errorf("got %+v, want the endpoint probed during a one-shot load", app.probeResults["abc"])
	}
}
