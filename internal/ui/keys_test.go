package ui

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/RchrdHndrcks/muelle/internal/compose"
	"github.com/RchrdHndrcks/muelle/internal/config"
	"github.com/RchrdHndrcks/muelle/internal/docker"
	"github.com/RchrdHndrcks/muelle/internal/tui"
)

// loadedApp returns an app populated from the fake daemon, showing everything.
func loadedApp(t *testing.T) *App {
	t.Helper()
	app := newTestApp(t)
	app.SetShowAll(true)
	if err := app.LoadOnce(context.Background()); err != nil {
		t.Fatalf("LoadOnce: %v", err)
	}
	return app
}

func press(app *App, key tui.Key) {
	app.handleKey(context.Background(), key)
}

func TestNavigationMovesSelection(t *testing.T) {
	app := loadedApp(t)

	start := app.selected()

	press(app, runeKey('j'))
	if got := app.selected(); got != start+1 {
		t.Errorf("got %d after j, want one below %d", got, start)
	}

	press(app, runeKey('k'))
	if got := app.selected(); got != start {
		t.Errorf("got %d after k, want back to %d", got, start)
	}
}

func TestNavigationStopsAtListEnds(t *testing.T) {
	app := loadedApp(t)

	press(app, runeKey('k'))
	if got := app.selected(); got != 0 {
		t.Errorf("got %d, want to stay at the top", got)
	}

	press(app, runeKey('G'))
	last := app.currentLength() - 1
	press(app, runeKey('j'))
	if got := app.selected(); got != last {
		t.Errorf("got %d, want to stay at the last row (%d)", got, last)
	}
}

func TestGAndShiftGJumpToEnds(t *testing.T) {
	app := loadedApp(t)

	press(app, runeKey('G'))
	if got := app.selected(); got != app.currentLength()-1 {
		t.Errorf("got %d, want the last row", got)
	}

	press(app, runeKey('g'))
	if got := app.selected(); got != 0 {
		t.Errorf("got %d, want the first row", got)
	}
}

func TestNumberKeysSwitchViews(t *testing.T) {
	app := loadedApp(t)

	press(app, runeKey('3'))
	if app.view != ViewImages {
		t.Errorf("got %v, want the images view", app.view.Title())
	}

	press(app, runeKey('1'))
	if app.view != ViewContainers {
		t.Errorf("got %v, want the containers view", app.view.Title())
	}
}

func TestTabCyclesThroughViews(t *testing.T) {
	app := loadedApp(t)

	press(app, typeKey(tui.KeyTab))
	if app.view != ViewCompose {
		t.Errorf("got %v, want the next view", app.view.Title())
	}

	press(app, typeKey(tui.KeyShiftTab))
	if app.view != ViewContainers {
		t.Errorf("got %v, want the previous view", app.view.Title())
	}
}

// Left and right are bound alongside Tab so the arrow keys alone are enough
// to move around.
func TestArrowKeysCycleViewsLikeTab(t *testing.T) {
	app := loadedApp(t)

	press(app, typeKey(tui.KeyRight))
	if app.view != ViewCompose {
		t.Errorf("got %v after right, want the next view", app.view.Title())
	}

	press(app, typeKey(tui.KeyLeft))
	if app.view != ViewContainers {
		t.Errorf("got %v after left, want the previous view", app.view.Title())
	}
}

func TestArrowKeysWrapAroundTheViewList(t *testing.T) {
	app := loadedApp(t)

	// Named against the count rather than a specific view, so adding
	// another view does not break the assertion.
	last := View(viewCount - 1)
	press(app, typeKey(tui.KeyLeft))
	if app.view != last {
		t.Errorf("got %v, want left from the first view to wrap to %v",
			app.view.Title(), last.Title())
	}

	press(app, typeKey(tui.KeyRight))
	if app.view != ViewContainers {
		t.Errorf("got %v, want right from the last view to wrap to the first", app.view.Title())
	}
}

// Up and down must keep moving the selection, not the view.
func TestVerticalArrowsStillMoveSelection(t *testing.T) {
	app := loadedApp(t)

	start := app.selected()

	press(app, typeKey(tui.KeyDown))

	if app.view != ViewContainers {
		t.Errorf("down changed the view to %v", app.view.Title())
	}
	if app.selected() != start+1 {
		t.Errorf("got selection %d, want down to move it below %d", app.selected(), start)
	}
}

func TestCapitalSTogglesSystemPanel(t *testing.T) {
	app := loadedApp(t)
	before := app.showSystem

	press(app, runeKey('S'))

	if app.showSystem == before {
		t.Error("S should toggle the host metrics panel")
	}
}

// Lower-case s starts a container; the toggle must not steal it.
func TestLowercaseSStillStartsContainer(t *testing.T) {
	app := loadedApp(t)
	panelBefore := app.showSystem

	press(app, runeKey('s'))

	if app.showSystem != panelBefore {
		t.Error("s must not toggle the panel; that is S")
	}
}

func TestQuestionMarkTogglesHelp(t *testing.T) {
	app := loadedApp(t)

	press(app, runeKey('?'))
	if app.mode != ModeHelp {
		t.Error("? should open help")
	}

	press(app, typeKey(tui.KeyEscape))
	if app.mode != ModeList {
		t.Error("Escape should leave help")
	}
}

func TestSlashOpensFilterPrompt(t *testing.T) {
	app := loadedApp(t)

	press(app, runeKey('/'))

	if app.overlay == nil || app.overlay.Kind != OverlayInput {
		t.Fatal("/ should open a text input")
	}

	for _, r := range "cache" {
		press(app, runeKey(r))
	}
	press(app, typeKey(tui.KeyEnter))

	if app.filter != "cache" {
		t.Errorf("got filter %q, want %q", app.filter, "cache")
	}
	if app.overlay != nil {
		t.Error("the overlay should close after Enter")
	}
}

func TestEscapeClearsFilter(t *testing.T) {
	app := loadedApp(t)
	app.filter = "cache"

	press(app, typeKey(tui.KeyEscape))

	if app.filter != "" {
		t.Errorf("got %q, want the filter cleared", app.filter)
	}
}

// Every destructive key must raise a confirmation rather than acting at once.
func TestDestructiveKeysAlwaysConfirmFirst(t *testing.T) {
	cases := []struct {
		view View
		key  rune
		name string
	}{
		{ViewContainers, 'D', "remove container"},
		{ViewContainers, 'K', "kill container"},
		{ViewImages, 'D', "remove image"},
		{ViewImages, 'P', "prune images"},
		{ViewVolumes, 'D', "remove volume"},
		{ViewVolumes, 'P', "prune volumes"},
	}

	for _, tc := range cases {
		app := loadedApp(t)
		app.SetView(tc.view)

		press(app, runeKey(tc.key))

		if app.overlay == nil {
			t.Errorf("%s: pressing %q acted without confirmation", tc.name, tc.key)
			continue
		}
		if app.overlay.Kind != OverlayConfirm {
			t.Errorf("%s: got overlay kind %v, want a confirmation", tc.name, app.overlay.Kind)
		}
		if !app.overlay.Danger {
			t.Errorf("%s: the confirmation should be marked destructive", tc.name)
		}
	}
}

// While an overlay is open, keys belong to it — a stray "q" must not quit.
func TestOverlayCapturesKeysFromTheList(t *testing.T) {
	app := loadedApp(t)
	app.overlay = NewInput("Filter", "match:", "", func(any) {})

	press(app, runeKey('q'))

	select {
	case <-app.quit:
		t.Fatal("q reached the global handler and quit the app")
	default:
	}
	if app.overlay == nil || app.overlay.Input != "q" {
		t.Error("the key should have been typed into the overlay")
	}
}

func TestQuitStopsTheApp(t *testing.T) {
	app := loadedApp(t)

	press(app, runeKey('q'))

	select {
	case <-app.quit:
	default:
		t.Error("q should stop the app")
	}
}

func TestCtrlCStopsTheApp(t *testing.T) {
	app := loadedApp(t)

	press(app, typeKey(tui.KeyCtrlC))

	select {
	case <-app.quit:
	default:
		t.Error("Ctrl-C should stop the app")
	}
}

// Stopping is idempotent: a second quit must not panic on a closed channel.
func TestQuittingTwiceIsSafe(t *testing.T) {
	app := loadedApp(t)

	press(app, runeKey('q'))
	press(app, typeKey(tui.KeyCtrlC))
	app.Stop()
}

func TestLogViewerKeysToggleOptions(t *testing.T) {
	app := loadedApp(t)
	app.mode = ModeLogs

	wrapBefore := app.logWrap
	press(app, runeKey('w'))
	if app.logWrap == wrapBefore {
		t.Error("w should toggle wrapping")
	}

	stampsBefore := app.logStamps
	press(app, runeKey('t'))
	if app.logStamps == stampsBefore {
		t.Error("t should toggle timestamps")
	}

	press(app, runeKey('f'))
	if app.logPager.Following() {
		t.Error("f should pause following when it was on")
	}
}

func TestEscapeLeavesTheLogViewer(t *testing.T) {
	app := loadedApp(t)
	app.mode = ModeLogs

	press(app, typeKey(tui.KeyEscape))

	if app.mode != ModeList {
		t.Error("Escape should return to the list")
	}
}

// The exec menu needs a running container; offering it for a stopped one would
// fail confusingly at the daemon.
func TestExecIsRefusedForStoppedContainer(t *testing.T) {
	app := loadedApp(t)
	app.dockerCLI = true
	app.filter = "shop-db" // the exited container

	press(app, runeKey('x'))

	if app.overlay != nil {
		t.Error("no exec menu should open for a stopped container")
	}
	if !app.status.isError {
		t.Error("the refusal should be explained in the status bar")
	}
}

// Without the docker CLI, exec cannot work; say so rather than failing later.
func TestExecReportsMissingDockerCLI(t *testing.T) {
	app := loadedApp(t)
	app.dockerCLI = false
	app.filter = "shop-api" // running

	press(app, runeKey('x'))

	if !app.status.isError {
		t.Error("expected an error explaining the CLI is required")
	}
	if app.overlay != nil {
		t.Error("no menu should open without the CLI")
	}
}

// The viewer's toggles should be found the way they were left, the same as the
// container ordering is.
func TestLogViewTogglesAreRecorded(t *testing.T) {
	app := loadedApp(t)
	app.mode = ModeLogs
	var saved config.Config
	app.SetPreferenceWriter(func(change func(*config.Config)) error {
		change(&saved)
		return nil
	})

	press(app, runeKey('t'))
	press(app, runeKey('w'))
	press(app, runeKey('F'))

	if saved.LogTimestamps != app.logStamps {
		t.Errorf("recorded timestamps %v while showing %v", saved.LogTimestamps, app.logStamps)
	}
	if saved.LogWrap != app.logWrap {
		t.Errorf("recorded wrap %v while showing %v", saved.LogWrap, app.logWrap)
	}
	if saved.LogFormat != app.logFormat {
		t.Errorf("recorded format %v while showing %v", saved.LogFormat, app.logFormat)
	}
}

func TestStoredLogViewSettingsApplyAtStartup(t *testing.T) {
	cfg := config.Default()
	cfg.ComposeDirs = nil
	cfg.LogTimestamps = true
	cfg.LogWrap = false
	cfg.LogFormat = false
	app := New(cfg, fakeDaemon(t), tui.NewScreen(&bytes.Buffer{}, 120, 30, false), nil)

	if !app.logStamps || app.logWrap || app.logFormat {
		t.Errorf("got stamps %v wrap %v format %v, want the stored settings applied",
			app.logStamps, app.logWrap, app.logFormat)
	}
}

// U recreates containers and removes images, so it must not start from a
// slipped keystroke: the first thing it does is ask.
func TestUpdateKeyAsksForConfirmation(t *testing.T) {
	app := loadedApp(t)
	press(app, runeKey('2'))

	press(app, runeKey('U'))

	if app.overlay == nil || app.overlay.Kind != OverlayConfirm {
		t.Fatal("U should open a confirmation overlay")
	}
	want := "Pull images, apply changes, and prune dangling images for shop?"
	if app.overlay.Prompt != want {
		t.Errorf("got prompt %q, want %q", app.overlay.Prompt, want)
	}
	if app.overlay.Danger {
		t.Error("updating loses no data, so Enter should be allowed to confirm")
	}
}

// Declining must leave everything alone.
func TestUpdateDeclinedDoesNothing(t *testing.T) {
	app := loadedApp(t)
	press(app, runeKey('2'))
	press(app, runeKey('U'))

	press(app, runeKey('n'))

	if app.overlay != nil {
		t.Error("declining should close the overlay")
	}
	if app.status.isError {
		t.Errorf("got error %q, want none", app.status.text)
	}
}

// Update goes through the same guard as every other Compose action, so a
// machine without Compose hears about the missing install rather than an
// exec failure.
func TestUpdateRefusedWhenComposeIsMissing(t *testing.T) {
	app := loadedApp(t)
	app.runner = &Runner{}
	app.dockerCLI = true
	app.composeBinary = nil
	press(app, runeKey('2'))
	press(app, runeKey('U'))

	press(app, runeKey('y'))

	if !app.status.isError {
		t.Fatalf("got status %q, want an error", app.status.text)
	}
	if !strings.Contains(app.status.text, "docker-compose") {
		t.Errorf("got %q, want it to name both forms of Compose", app.status.text)
	}
}

// A failed pull stops the sequence: recreating containers onto whatever
// happened to be cached is not the update that was asked for, and the error
// has to say which step refused.
func TestUpdateStopsWhenPullFails(t *testing.T) {
	app := loadedApp(t)
	app.runner = &Runner{}
	// "false" ignores its arguments and exits 1, standing in for a Compose
	// whose pull fails.
	app.composeBinary = compose.Binary{"false"}

	app.updateStack(compose.Project{Name: "shop", WorkingDir: "/srv/shop"})

	if !app.status.isError {
		t.Fatalf("got status %q, want an error", app.status.text)
	}
	if !strings.Contains(app.status.text, "compose pull") {
		t.Errorf("got %q, want the failing step named", app.status.text)
	}
}

// After a successful pull and up, the dangling images the pull replaced are
// pruned through the API, and the status line reports what that reclaimed.
func TestUpdatePrunesAndReportsReclaimedSpace(t *testing.T) {
	app := loadedApp(t)
	app.runner = &Runner{}
	// "true" ignores its arguments and exits 0, standing in for a Compose
	// whose pull and up both succeed.
	app.composeBinary = compose.Binary{"true"}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/images/prune" {
			json.NewEncoder(w).Encode(map[string]any{
				"ImagesDeleted":  []map[string]any{{"Deleted": "sha256:aaa"}, {"Deleted": "sha256:bbb"}},
				"SpaceReclaimed": 268435456,
			})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(server.Close)
	client, err := docker.New(server.URL)
	if err != nil {
		t.Fatalf("docker.New: %v", err)
	}
	app.docker = client

	app.updateStack(compose.Project{Name: "shop", WorkingDir: "/srv/shop"})

	done := awaitActionDone(t, app)
	if done.err != nil {
		t.Fatalf("got error %v, want a prune report", done.err)
	}
	want := "updated shop: pruned 2 dangling images, 256MiB reclaimed"
	if done.message != want {
		t.Errorf("got %q, want %q", done.message, want)
	}
}

// A prune failing after a successful update must not make the update look
// failed: the containers are running the new images either way.
func TestUpdateReportsPruneFailureAsItsOwn(t *testing.T) {
	app := loadedApp(t)
	app.runner = &Runner{}
	app.composeBinary = compose.Binary{"true"}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(server.Close)
	client, err := docker.New(server.URL)
	if err != nil {
		t.Fatalf("docker.New: %v", err)
	}
	app.docker = client

	app.updateStack(compose.Project{Name: "shop", WorkingDir: "/srv/shop"})

	done := awaitActionDone(t, app)
	if done.err == nil {
		t.Fatal("a failed prune must be reported")
	}
	if !strings.Contains(done.err.Error(), "updated shop, but pruning") {
		t.Errorf("got %q, want the update's success stated alongside the prune failure", done.err)
	}
}

// awaitActionDone reads events until the update's outcome arrives. The
// refreshes an update triggers post their own events, so anything else on the
// channel is skipped rather than mistaken for the result.
func awaitActionDone(t *testing.T, app *App) actionDone {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case ev := <-app.events:
			if done, ok := ev.(actionDone); ok {
				return done
			}
		case <-deadline:
			t.Fatal("no actionDone event arrived")
		}
	}
}

// The Enter menu offers update beside up, whose single-step version it is.
func TestComposeMenuOffersUpdate(t *testing.T) {
	app := newTestApp(t)
	app.composeBinary = compose.Binary{"docker", "compose"}

	app.openComposeMenu(compose.Project{Name: "shop", WorkingDir: "/srv/shop"})

	upIndex, updateIndex := -1, -1
	for i, item := range app.overlay.Items {
		switch item.Value {
		case compose.ActionUp:
			upIndex = i
		case updateChoice{}:
			updateIndex = i
		}
	}
	if updateIndex == -1 {
		t.Fatal("the menu should offer update")
	}
	if updateIndex != upIndex+1 {
		t.Errorf("update sits at %d, want it directly after up at %d", updateIndex, upIndex)
	}
}

// Choosing update from the menu raises the same confirmation the U key does,
// so the two routes cannot drift apart.
func TestComposeMenuUpdateRaisesConfirmation(t *testing.T) {
	app := loadedApp(t)
	press(app, runeKey('2'))
	press(app, tui.Key{Type: tui.KeyEnter})
	if app.overlay == nil || app.overlay.Kind != OverlayMenu {
		t.Fatal("Enter should open the compose menu")
	}

	for i, item := range app.overlay.Items {
		if item.Value == (updateChoice{}) {
			app.overlay.Selected = i
			break
		}
	}
	press(app, tui.Key{Type: tui.KeyEnter})

	if app.overlay == nil || app.overlay.Kind != OverlayConfirm {
		t.Fatal("choosing update should raise a confirmation")
	}
	if !strings.Contains(app.overlay.Prompt, "prune dangling images for shop") {
		t.Errorf("got prompt %q, want the update confirmation", app.overlay.Prompt)
	}
}
