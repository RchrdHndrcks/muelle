package ui

import (
	"strings"
	"testing"

	"github.com/RchrdHndrcks/muelle/internal/docker"
	"github.com/RchrdHndrcks/muelle/internal/tui"
)

func withNetworks(t *testing.T) *App {
	t.Helper()
	app := newTestApp(t)
	app.networks = []docker.Network{
		{
			Name: "shop_default", ID: "aaaa1111bbbb2222", Driver: "bridge", Scope: "local",
			Labels: map[string]string{docker.LabelProject: "shop"},
			IPAM: struct {
				Config []struct {
					Subnet  string `json:"Subnet"`
					Gateway string `json:"Gateway"`
				} `json:"Config"`
			}{Config: []struct {
				Subnet  string `json:"Subnet"`
				Gateway string `json:"Gateway"`
			}{{Subnet: "172.20.0.0/16"}}},
		},
		{Name: "bridge", ID: "cccc3333", Driver: "bridge", Scope: "local"},
	}
	app.SetView(ViewNetworks)
	return app
}

func TestNetworksViewRendersDetails(t *testing.T) {
	app := withNetworks(t)

	rendered := strings.Join(app.renderNetworks(120, 20), "\n")

	for _, want := range []string{"shop_default", "bridge", "172.20.0.0/16", "shop"} {
		if !strings.Contains(rendered, want) {
			t.Errorf("networks view is missing %q:\n%s", want, rendered)
		}
	}
}

func TestNetworksViewEmptyState(t *testing.T) {
	app := newTestApp(t)
	app.SetView(ViewNetworks)

	rendered := strings.Join(app.renderNetworks(80, 10), "\n")

	if !strings.Contains(rendered, "No networks") {
		t.Errorf("got %q, want an empty state", rendered)
	}
}

func TestNetworksViewIsReachableByKey(t *testing.T) {
	app := loadedApp(t)

	press(app, runeKey('5'))

	if app.view != ViewNetworks {
		t.Errorf("got %v, want the networks view", app.view.Title())
	}
}

func TestNetworksViewIsInTheTabCycle(t *testing.T) {
	app := loadedApp(t)
	app.SetView(ViewVolumes)

	press(app, typeKey(tui.KeyTab))

	if app.view != ViewNetworks {
		t.Errorf("got %v, want networks after volumes", app.view.Title())
	}
}

// Removing one fails at the daemon; saying so first beats surfacing its error
// after a confirmation the user need not have answered.
func TestRemovingPredefinedNetworkIsRefusedBeforeConfirming(t *testing.T) {
	app := withNetworks(t)
	app.selection[ViewNetworks] = 1 // "bridge"

	press(app, runeKey('D'))

	if app.overlay != nil {
		t.Error("no confirmation should be raised for a network that cannot be removed")
	}
	if !app.status.isError {
		t.Error("the refusal should be explained")
	}
}

func TestRemovingUserNetworkAsksFirst(t *testing.T) {
	app := withNetworks(t)
	app.selection[ViewNetworks] = 0 // "shop_default"

	press(app, runeKey('D'))

	if app.overlay == nil || app.overlay.Kind != OverlayConfirm {
		t.Fatal("removing a user network should ask first")
	}
	if !app.overlay.Danger {
		t.Error("the confirmation should be marked destructive")
	}
}

func TestNetworkFilterMatchesNameAndProject(t *testing.T) {
	app := withNetworks(t)

	app.filter = "shop"
	if got := len(app.filteredNetworks()); got != 1 {
		t.Errorf("got %d matches, want 1", got)
	}

	app.filter = "nothing-matches"
	if got := len(app.filteredNetworks()); got != 0 {
		t.Errorf("got %d matches, want none", got)
	}
}

func TestNetworkRowsFitTheWidth(t *testing.T) {
	app := withNetworks(t)

	for _, width := range []int{40, 80, 200} {
		for _, line := range app.renderNetworks(width, 20) {
			if tui.VisibleWidth(line) > width {
				t.Errorf("at width %d a row overflowed: %q", width, line)
			}
		}
	}
}

// A fifth view must not disturb the per-view selection arrays.
func TestNetworkSelectionIsIndependent(t *testing.T) {
	app := withNetworks(t)
	app.selection[ViewNetworks] = 1

	app.SetView(ViewContainers)
	if app.selected() != 0 {
		t.Errorf("got %d, want the container view's own selection", app.selected())
	}

	app.SetView(ViewNetworks)
	if app.selected() != 1 {
		t.Errorf("got %d, want the network selection restored", app.selected())
	}
}
