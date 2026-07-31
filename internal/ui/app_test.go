package ui

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/RchrdHndrcks/muelle/internal/config"
	"github.com/RchrdHndrcks/muelle/internal/docker"
	"github.com/RchrdHndrcks/muelle/internal/tui"
)

// fakeDaemon serves the endpoints the app reads at startup.
func fakeDaemon(t *testing.T) *docker.Client {
	t.Helper()

	containers := []map[string]any{
		{
			"Id": "aaaaaaaaaaaa1111", "Names": []string{"/shop-api"}, "Image": "shop/api:1.4",
			"State": "running", "Status": "Up 2 hours",
			"Ports":  []map[string]any{{"PrivatePort": 8080, "PublicPort": 8080, "Type": "tcp"}},
			"Labels": map[string]string{docker.LabelProject: "shop", docker.LabelService: "api"},
		},
		{
			"Id": "bbbbbbbbbbbb2222", "Names": []string{"/shop-db"}, "Image": "mysql:8.0",
			"State": "exited", "Status": "Exited (0) 5 minutes ago",
			"Labels": map[string]string{docker.LabelProject: "shop", docker.LabelService: "db"},
		},
		{
			"Id": "cccccccccccc3333", "Names": []string{"/standalone-cache"}, "Image": "redis:7-alpine",
			"State": "running", "Status": "Up 1 day",
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/containers/json"):
			json.NewEncoder(w).Encode(containers)
		case strings.HasPrefix(r.URL.Path, "/images/json"):
			json.NewEncoder(w).Encode([]map[string]any{
				{"Id": "sha256:1111111111112222", "RepoTags": []string{"mysql:8.0"}, "Size": 1073741824},
			})
		case strings.HasPrefix(r.URL.Path, "/volumes"):
			json.NewEncoder(w).Encode(map[string]any{
				"Volumes": []map[string]any{{"Name": "shop-db-data", "Driver": "local"}},
			})
		case r.URL.Path == "/version":
			json.NewEncoder(w).Encode(map[string]any{"Version": "27.4.0", "ApiVersion": "1.47"})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)

	client, err := docker.New(server.URL)
	if err != nil {
		t.Fatalf("docker.New: %v", err)
	}
	return client
}

// newTestApp builds an app with colour off, so assertions compare plain text.
func newTestApp(t *testing.T) *App {
	t.Helper()
	cfg := config.Default()
	cfg.ComposeDirs = nil // no filesystem scanning in tests
	cfg.Stats = false     // the fake daemon serves no stats
	screen := tui.NewScreen(&bytes.Buffer{}, 120, 30, false)
	return New(cfg, fakeDaemon(t), screen, nil)
}

// frameText renders a frame and strips trailing padding for comparison.
func frameText(app *App) string {
	lines := app.Frame(120, 30)
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, " ")
	}
	return strings.Join(lines, "\n")
}

func TestFrameRendersContainerList(t *testing.T) {
	app := newTestApp(t)
	app.SetShowAll(true)
	if err := app.LoadOnce(context.Background()); err != nil {
		t.Fatalf("LoadOnce: %v", err)
	}

	frame := frameText(app)

	// Grouped, which is the default: the heading carries the application's
	// name and the rows carry what tells them apart, so "shop-api" appears
	// as "shop" above "api" rather than as one string.
	for _, want := range []string{"shop", "api", "db", "standalone-cache", "mysql:8.0", "8080->8080/tcp"} {
		if !strings.Contains(frame, want) {
			t.Errorf("frame is missing %q:\n%s", want, frame)
		}
	}
	if !strings.Contains(frame, "NAME") {
		t.Error("frame should include column headers")
	}
}

// Ungrouped there is no heading to carry the application's name, so each row
// has to carry the whole thing.
func TestFrameRendersWholeNamesUngrouped(t *testing.T) {
	app := newTestApp(t)
	app.SetShowAll(true)
	app.grouped = false
	if err := app.LoadOnce(context.Background()); err != nil {
		t.Fatalf("LoadOnce: %v", err)
	}

	frame := frameText(app)

	for _, want := range []string{"shop-api", "shop-db", "standalone-cache"} {
		if !strings.Contains(frame, want) {
			t.Errorf("frame is missing %q:\n%s", want, frame)
		}
	}
}

// Without -all, stopped containers are hidden.
func TestFrameHidesStoppedContainersByDefault(t *testing.T) {
	app := newTestApp(t)
	// Ungrouped, so the assertion is about which containers are listed
	// rather than about how a heading splits their names.
	app.grouped = false
	if err := app.LoadOnce(context.Background()); err != nil {
		t.Fatalf("LoadOnce: %v", err)
	}

	frame := frameText(app)

	if strings.Contains(frame, "shop-db") {
		t.Errorf("stopped container shown without -all:\n%s", frame)
	}
	if !strings.Contains(frame, "shop-api") {
		t.Error("running containers should still be listed")
	}
}

// A project's status must account for stopped members, so it cannot be derived
// from the running-only list.
func TestComposeViewReportsPartialProject(t *testing.T) {
	app := newTestApp(t)
	if err := app.LoadOnce(context.Background()); err != nil {
		t.Fatalf("LoadOnce: %v", err)
	}
	app.SetView(ViewCompose)

	frame := frameText(app)

	if !strings.Contains(frame, "shop") {
		t.Errorf("compose view is missing the project:\n%s", frame)
	}
	if !strings.Contains(frame, "partial") {
		t.Errorf("one of two services is up, so the project is partial:\n%s", frame)
	}
	if !strings.Contains(frame, "1/2") {
		t.Errorf("expected a 1/2 running count:\n%s", frame)
	}
}

func TestEveryFrameFitsTheScreen(t *testing.T) {
	app := newTestApp(t)
	app.SetShowAll(true)
	if err := app.LoadOnce(context.Background()); err != nil {
		t.Fatalf("LoadOnce: %v", err)
	}

	for _, view := range []View{ViewContainers, ViewCompose, ViewImages, ViewVolumes} {
		app.SetView(view)
		lines := app.Frame(80, 24)

		if len(lines) != 24 {
			t.Errorf("%v produced %d lines, want exactly 24", view.Title(), len(lines))
		}
		for i, line := range lines {
			if width := tui.VisibleWidth(line); width > 80 {
				t.Errorf("%v line %d is %d cells wide, want at most 80", view.Title(), i, width)
			}
		}
	}
}

// The layout must survive a terminal far narrower than it was designed for.
func TestFrameSurvivesTinyTerminal(t *testing.T) {
	app := newTestApp(t)
	app.SetShowAll(true)
	if err := app.LoadOnce(context.Background()); err != nil {
		t.Fatalf("LoadOnce: %v", err)
	}

	for _, size := range [][2]int{{20, 6}, {40, 10}, {200, 60}} {
		lines := app.Frame(size[0], size[1])

		if len(lines) != size[1] {
			t.Errorf("at %dx%d got %d lines, want %d", size[0], size[1], len(lines), size[1])
		}
		for _, line := range lines {
			if tui.VisibleWidth(line) > size[0] {
				t.Errorf("at %dx%d a line overflowed: %q", size[0], size[1], line)
			}
		}
	}
}

func TestHelpFrameRenders(t *testing.T) {
	app := newTestApp(t)
	app.mode = ModeHelp

	frame := frameText(app)

	for _, want := range []string{"Navigation", "Containers", "exec menu", "quit"} {
		if !strings.Contains(frame, want) {
			t.Errorf("help is missing %q:\n%s", want, frame)
		}
	}
}

func TestFilterNarrowsTheList(t *testing.T) {
	app := newTestApp(t)
	app.SetShowAll(true)
	if err := app.LoadOnce(context.Background()); err != nil {
		t.Fatalf("LoadOnce: %v", err)
	}

	app.filter = "cache"
	frame := frameText(app)

	if !strings.Contains(frame, "standalone-cache") {
		t.Errorf("the matching container should remain:\n%s", frame)
	}
	if strings.Contains(frame, "shop-api") {
		t.Errorf("non-matching containers should be hidden:\n%s", frame)
	}
}

func TestFilterMatchesImageAndProject(t *testing.T) {
	app := newTestApp(t)
	app.SetShowAll(true)
	if err := app.LoadOnce(context.Background()); err != nil {
		t.Fatalf("LoadOnce: %v", err)
	}

	app.filter = "redis"
	if got := len(app.filteredContainers()); got != 1 {
		t.Errorf("got %d matches for an image substring, want 1", got)
	}

	app.filter = "shop"
	if got := len(app.filteredContainers()); got != 2 {
		t.Errorf("got %d matches for a project name, want 2", got)
	}
}

// Selection must survive the list shrinking beneath it.
func TestSelectionClampsWhenListShrinks(t *testing.T) {
	app := newTestApp(t)
	app.SetShowAll(true)
	if err := app.LoadOnce(context.Background()); err != nil {
		t.Fatalf("LoadOnce: %v", err)
	}

	app.selection[ViewContainers] = 2
	app.filter = "cache" // narrows the list to one entry

	if got, last := app.selected(), len(app.rows())-1; got != last {
		t.Errorf("got selection %d, want it clamped to the last row (%d)", got, last)
	}
	if _, ok := app.selectedContainer(); !ok {
		t.Error("a non-empty list should still have a selected container")
	}
}

func TestEmptyListRendersGuidance(t *testing.T) {
	app := newTestApp(t)
	app.SetShowAll(true)
	if err := app.LoadOnce(context.Background()); err != nil {
		t.Fatalf("LoadOnce: %v", err)
	}

	app.filter = "no-such-container"
	frame := frameText(app)

	if !strings.Contains(frame, "No containers") {
		t.Errorf("expected an empty-state message:\n%s", frame)
	}
	if _, ok := app.selectedContainer(); ok {
		t.Error("an empty list should report no selection")
	}
}

func TestOverlayDrawsOverTheList(t *testing.T) {
	app := newTestApp(t)
	if err := app.LoadOnce(context.Background()); err != nil {
		t.Fatalf("LoadOnce: %v", err)
	}

	app.overlay = NewConfirm("Remove container", "Remove shop-api?", true, func(any) {})
	frame := frameText(app)

	if !strings.Contains(frame, "Remove container") {
		t.Errorf("the overlay title should appear:\n%s", frame)
	}
	if !strings.Contains(frame, "y = yes") {
		t.Errorf("the overlay should show its key hints:\n%s", frame)
	}
}

// Switching tabs must not carry one view's cursor into another.
func TestSelectionIsPerView(t *testing.T) {
	app := newTestApp(t)
	app.SetShowAll(true)
	if err := app.LoadOnce(context.Background()); err != nil {
		t.Fatalf("LoadOnce: %v", err)
	}

	app.selection[ViewContainers] = 2
	app.SetView(ViewVolumes)

	if got := app.selected(); got != 0 {
		t.Errorf("got %d in the volumes view, want its own selection", got)
	}
	app.SetView(ViewContainers)
	if got := app.selected(); got != 2 {
		t.Errorf("got %d, want the container selection restored", got)
	}
}

// A field declared but never initialised is invisible to every test that
// supplies its own value, which is how log formatting came to ship switched
// off. This asserts the state the application actually starts in.
func TestNewAppliesItsDefaults(t *testing.T) {
	cfg := config.Default()
	cfg.ComposeDirs = nil
	app := New(cfg, fakeDaemon(t), tui.NewScreen(&bytes.Buffer{}, 120, 30, false), nil)

	if !app.logFormat {
		t.Error("log formatting should be on by default; it is the point of parsing the lines")
	}
	if !app.logWrap {
		t.Error("wrapping should be on by default")
	}
	if app.showSystem != cfg.SystemPanel {
		t.Errorf("got showSystem %v, want it to follow the configuration", app.showSystem)
	}
	if app.imageUsage == nil {
		t.Error("the usage map should be ready to read before the first refresh")
	}
	if app.mode != ModeList || app.view != ViewContainers {
		t.Errorf("got mode %v view %v, want the container list", app.mode, app.view.Title())
	}
}

// The formatter is what makes a structured log readable; defaulting it off
// leaves every user to discover a key that turns the feature on.
func TestLogViewFormatsByDefault(t *testing.T) {
	cfg := config.Default()
	cfg.ComposeDirs = nil
	app := New(cfg, fakeDaemon(t), tui.NewScreen(&bytes.Buffer{}, 120, 30, false), nil)

	if !app.logOptions(120).Format {
		t.Error("the log view should format without being asked")
	}
}
