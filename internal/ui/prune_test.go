package ui

import (
	"strings"
	"testing"

	"github.com/RchrdHndrcks/muelle/internal/docker"
	"github.com/RchrdHndrcks/muelle/internal/tui"
)

// P offers a choice rather than acting, because the scopes differ by whether
// data is lost.
func TestPruneKeyOpensScopeMenu(t *testing.T) {
	app := loadedApp(t)

	press(app, runeKey('P'))

	if app.overlay == nil || app.overlay.Kind != OverlayMenu {
		t.Fatal("P should offer a choice of prune scopes")
	}
	if len(app.overlay.Items) != 3 {
		t.Errorf("got %d scopes, want three", len(app.overlay.Items))
	}
}

// Volumes are the only prune target that cannot be rebuilt or re-pulled.
func TestDefaultPruneScopeSparesVolumes(t *testing.T) {
	app := loadedApp(t)
	press(app, runeKey('P'))

	scope, ok := app.overlay.Items[0].Value.(docker.PruneScope)

	if !ok {
		t.Fatal("menu items should carry a prune scope")
	}
	if scope.Volumes {
		t.Error("the first, least destructive scope must not touch volumes")
	}
	if scope.AllImages {
		t.Error("the first scope should remove only dangling images")
	}
}

func TestVolumeScopeIsLastAndWarns(t *testing.T) {
	app := loadedApp(t)
	press(app, runeKey('P'))

	last := app.overlay.Items[len(app.overlay.Items)-1]
	scope := last.Value.(docker.PruneScope)

	if !scope.Volumes {
		t.Error("the most destructive scope should be last")
	}
	if !strings.Contains(strings.ToUpper(last.Detail), "DATA") {
		t.Errorf("got %q, want the data loss spelled out", last.Detail)
	}
}

// Choosing a scope must still confirm; the menu is a choice, not consent.
func TestChoosingAScopeStillConfirms(t *testing.T) {
	app := loadedApp(t)
	press(app, runeKey('P'))

	press(app, typeKey(tui.KeyEnter))

	if app.overlay == nil {
		t.Fatal("selecting a scope should raise a confirmation")
	}
	if app.overlay.Kind != OverlayConfirm || !app.overlay.Danger {
		t.Errorf("got %v (danger=%v), want a destructive confirmation",
			app.overlay.Kind, app.overlay.Danger)
	}
}

func TestVolumeConfirmationSpellsOutTheConsequence(t *testing.T) {
	app := loadedApp(t)
	app.confirmSystemPrune(t.Context(), docker.PruneScope{Volumes: true, AllImages: true})

	if app.overlay == nil {
		t.Fatal("expected a confirmation")
	}
	if !strings.Contains(app.overlay.Prompt, "cannot be recovered") {
		t.Errorf("got %q, want the irreversibility stated", app.overlay.Prompt)
	}
}

// The panel already shows a reclaimable figure; the menu should quote it so
// the user knows what they are getting.
func TestPruneMenuQuotesReclaimableSpace(t *testing.T) {
	app := withMetrics(t)

	app.openSystemPruneMenu(t.Context())

	if !strings.Contains(app.overlay.Items[0].Label, "4.0GiB") {
		t.Errorf("got %q, want the reclaimable estimate", app.overlay.Items[0].Label)
	}
}

func TestPruneMenuOmitsEstimateWhenUnmeasured(t *testing.T) {
	app := loadedApp(t)
	app.metrics.DiskKnown = false

	app.openSystemPruneMenu(t.Context())

	if strings.Contains(app.overlay.Items[0].Label, "about") {
		t.Errorf("got %q, want no estimate before disk has been measured",
			app.overlay.Items[0].Label)
	}
}

// A prune is exactly when the disk figures change; the panel must not sit on a
// stale reading for a minute afterwards.
func TestPruneMarksDiskMeasurementStale(t *testing.T) {
	app := withMetrics(t)

	systemPruned{result: docker.SystemPruneResult{Containers: 1, SpaceReclaimed: 1024}}.apply(app)

	if !app.diskMeasuredAt.IsZero() {
		t.Error("the disk measurement should be invalidated so the panel re-measures")
	}
}

// A partial failure still removed things, and hiding that leaves the user
// unsure what state the host is in.
func TestPartialPruneReportsBothOutcomes(t *testing.T) {
	app := withMetrics(t)

	systemPruned{result: docker.SystemPruneResult{
		Containers:     4,
		SpaceReclaimed: 2048,
		Errors:         []error{errFake},
	}}.apply(app)

	if !app.status.isError {
		t.Error("a partial failure should be reported as an error")
	}
	if !strings.Contains(app.status.text, "4 containers") {
		t.Errorf("got %q, want what was reclaimed reported too", app.status.text)
	}
}

func TestSuccessfulPruneReportsSummary(t *testing.T) {
	app := withMetrics(t)

	systemPruned{result: docker.SystemPruneResult{Images: 3, SpaceReclaimed: 3 << 30}}.apply(app)

	if app.status.isError {
		t.Errorf("got an error status for a successful prune: %q", app.status.text)
	}
	if !strings.Contains(app.status.text, "3 images") {
		t.Errorf("got %q, want the summary", app.status.text)
	}
}

// A menu choice that raises a confirmation must not have that confirmation
// wiped by the dismissal of the menu itself. This also covers the Compose
// action menu, where choosing "down" silently did nothing.
func TestMenuChoiceCanRaiseAFollowUpOverlay(t *testing.T) {
	app := loadedApp(t)
	ran := false

	app.overlay = NewMenu("Pick", []MenuItem{{Label: "one", Value: 1}}, func(any) {
		app.confirm("Confirm", "Really?", func() { ran = true })
	})

	press(app, typeKey(tui.KeyEnter))

	if app.overlay == nil {
		t.Fatal("the follow-up confirmation was discarded with the menu")
	}
	press(app, runeKey('y'))
	if !ran {
		t.Error("confirming should have run the action")
	}
}
