package ui

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/RchrdHndrcks/muelle/internal/docker"
	"github.com/RchrdHndrcks/muelle/internal/registry"
)

// fakeChecker serves canned verdicts, so key routing and result handling can
// be tested without a registry on the network.
type fakeChecker struct {
	results []registry.Result
	calls   atomic.Int32
}

func (f *fakeChecker) CheckAll(_ context.Context, _ []docker.Image) []registry.Result {
	f.calls.Add(1)
	return f.results
}

// awaitEvent reads the next event the background sweep posts and applies it,
// standing in for the loop the real app runs.
func awaitEvent(t *testing.T, app *App) {
	t.Helper()
	select {
	case ev := <-app.events:
		ev.apply(app)
	case <-time.After(2 * time.Second):
		t.Fatal("no event arrived from the update check")
	}
}

func TestCheckKeyRunsTheSweepAndSummarises(t *testing.T) {
	app := withImages(t)
	fake := &fakeChecker{results: []registry.Result{
		{Reference: "app:1", Verdict: registry.VerdictCurrent},
		{Reference: "old:2", Verdict: registry.VerdictOutdated},
		{Reference: "", Verdict: registry.VerdictUnknown},
	}}
	app.updateChecker = fake

	press(app, runeKey('c'))

	if !app.checkingUpdates {
		t.Fatal("c should start a check")
	}
	awaitEvent(t, app)

	if app.checkingUpdates {
		t.Error("a delivered result should end the in-flight state")
	}
	if app.updates["old:2"] != registry.VerdictOutdated {
		t.Errorf("updates[old:2] = %v, want VerdictOutdated", app.updates["old:2"])
	}
	if !strings.Contains(app.status.text, "updates: 1 of 3, 1 unknown") {
		t.Errorf("status = %q, want the summary", app.status.text)
	}
}

// A second press mid-sweep must not fan out a second round of registry
// requests behind the first.
func TestCheckKeyDoesNotStackSweeps(t *testing.T) {
	app := withImages(t)
	fake := &fakeChecker{}
	app.updateChecker = fake
	app.checkingUpdates = true

	press(app, runeKey('c'))

	if !strings.Contains(app.status.text, "already running") {
		t.Errorf("status = %q, want a note that a check is in flight", app.status.text)
	}
	if fake.calls.Load() != 0 {
		t.Error("no new sweep should have started")
	}
}

func TestCheckKeyWithNothingToCheck(t *testing.T) {
	app := newTestApp(t)
	app.SetView(ViewImages)
	app.updateChecker = &fakeChecker{}

	press(app, runeKey('c'))

	if app.checkingUpdates {
		t.Error("an empty list should not start a check")
	}
	if !strings.Contains(app.status.text, "no images") {
		t.Errorf("status = %q, want an explanation", app.status.text)
	}
}

func TestOutdatedImagesCarryTheMarker(t *testing.T) {
	app := withImages(t)
	app.updates["old:2"] = registry.VerdictOutdated
	app.updates["app:1"] = registry.VerdictCurrent

	rendered := strings.Join(app.renderImages(140, 20), "\n")

	if strings.Count(rendered, "↑") != 1 {
		t.Errorf("exactly the outdated row should carry the marker:\n%s", rendered)
	}
	for _, line := range strings.Split(rendered, "\n") {
		if strings.Contains(line, "↑") && !strings.Contains(line, "old:2") {
			t.Errorf("the marker landed on the wrong row: %s", line)
		}
	}
}

// The verdicts cost a registry round trip each; the routine list refresh,
// which replaces the image structs wholesale, must not throw them away.
func TestRefreshDoesNotWipeVerdicts(t *testing.T) {
	app := withImages(t)
	app.updates["old:2"] = registry.VerdictOutdated

	imagesLoaded{images: []docker.Image{
		{ID: "sha256:idle", RepoTags: []string{"old:2"}},
	}}.apply(app)

	if app.updates["old:2"] != registry.VerdictOutdated {
		t.Error("a list refresh wiped the cached verdicts")
	}
	rendered := strings.Join(app.renderImages(140, 20), "\n")
	if !strings.Contains(rendered, "↑") {
		t.Errorf("the marker should survive the refresh:\n%s", rendered)
	}
}

func TestStatusBarCountsUpdates(t *testing.T) {
	app := withImages(t)
	app.updates["old:2"] = registry.VerdictOutdated

	bar := app.renderStatusBar(160)

	if !strings.Contains(bar, "1 update") {
		t.Errorf("got %q, want the update count on the right", bar)
	}
}
