package docker

import (
	"context"
	"net/http"
	"testing"
)

// A user's own networks are the ones they can act on, so they come first.
func TestNetworksSortsPredefinedLast(t *testing.T) {
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[
			{"Name":"bridge","Driver":"bridge"},
			{"Name":"shop_default","Driver":"bridge","Labels":{"com.docker.compose.project":"shop"}},
			{"Name":"host","Driver":"host"},
			{"Name":"standalone","Driver":"bridge"}
		]`))
	})

	networks, err := client.Networks(context.Background())
	if err != nil {
		t.Fatalf("Networks: %v", err)
	}

	if networks[0].Name != "shop_default" {
		t.Errorf("got %q first, want a project network ahead of predefined ones", networks[0].Name)
	}
	last := networks[len(networks)-1]
	if !last.Predefined() {
		t.Errorf("got %q last, want a predefined network there", last.Name)
	}
}

// "remove" on one of these fails at the daemon, and nothing else distinguishes
// them from a user's own networks.
func TestPredefinedNetworksAreIdentified(t *testing.T) {
	for _, name := range []string{"bridge", "host", "none"} {
		if !(Network{Name: name}).Predefined() {
			t.Errorf("%q should be recognised as predefined", name)
		}
	}
	for _, name := range []string{"shop_default", "my-bridge", "hostile"} {
		if (Network{Name: name}).Predefined() {
			t.Errorf("%q is a user network, not predefined", name)
		}
	}
}

func TestNetworkSubnetJoinsConfiguredRanges(t *testing.T) {
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[{"Name":"n","IPAM":{"Config":[
			{"Subnet":"172.20.0.0/16","Gateway":"172.20.0.1"},
			{"Subnet":"fd00::/64"}
		]}}]`))
	})

	networks, _ := client.Networks(context.Background())

	if got := networks[0].Subnet(); got != "172.20.0.0/16, fd00::/64" {
		t.Errorf("got %q, want both subnets", got)
	}
}

func TestNetworkSubnetEmptyWhenUnconfigured(t *testing.T) {
	if got := (Network{}).Subnet(); got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func TestNetworkProjectComesFromComposeLabel(t *testing.T) {
	network := Network{Labels: map[string]string{LabelProject: "shop"}}

	if got := network.Project(); got != "shop" {
		t.Errorf("got %q, want the compose project", got)
	}
	if got := (Network{}).Project(); got != "" {
		t.Errorf("got %q, want empty for a network created outside compose", got)
	}
}

func TestNetworkShortID(t *testing.T) {
	network := Network{ID: "48addf058d334b9cdeacba2ceb1285007e988eb8"}

	if got := network.ShortID(); got != "48addf058d33" {
		t.Errorf("got %q, want 12 characters", got)
	}
}

func TestRemoveNetworkIssuesDelete(t *testing.T) {
	var method string
	client, requested := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		method = r.Method
		w.WriteHeader(http.StatusNoContent)
	})

	if err := client.RemoveNetwork(context.Background(), "abc"); err != nil {
		t.Fatalf("RemoveNetwork: %v", err)
	}
	if method != http.MethodDelete {
		t.Errorf("got %s, want DELETE", method)
	}
	if got := requested.First(); got != "/networks/abc" {
		t.Errorf("got %q, want the network path", got)
	}
}

// Networks occupy no meaningful disk, so the daemon reports no reclaimed
// space and only the count is meaningful.
func TestPruneNetworksReportsCountOnly(t *testing.T) {
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"NetworksDeleted":["a","b","c"]}`))
	})

	result, err := client.PruneNetworks(context.Background())
	if err != nil {
		t.Fatalf("PruneNetworks: %v", err)
	}
	if result.Deleted != 3 {
		t.Errorf("got %d, want 3 removed", result.Deleted)
	}
	if result.SpaceReclaimed != 0 {
		t.Errorf("got %d bytes, want none claimed for networks", result.SpaceReclaimed)
	}
}
