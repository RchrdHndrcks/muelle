package docker

import (
	"context"
	"net/http"
	"net/url"
	"sort"
	"strings"
)

// Network is an entry from /networks.
type Network struct {
	Name     string            `json:"Name"`
	ID       string            `json:"Id"`
	Driver   string            `json:"Driver"`
	Scope    string            `json:"Scope"`
	Internal bool              `json:"Internal"`
	Labels   map[string]string `json:"Labels"`
	IPAM     struct {
		Config []struct {
			Subnet  string `json:"Subnet"`
			Gateway string `json:"Gateway"`
		} `json:"Config"`
	} `json:"IPAM"`
	// Containers is only populated when a single network is inspected, not
	// in the list response.
	Containers map[string]struct {
		Name        string `json:"Name"`
		IPv4Address string `json:"IPv4Address"`
	} `json:"Containers"`
}

// ShortID returns the conventional 12-character network ID.
func (n Network) ShortID() string {
	if len(n.ID) > 12 {
		return n.ID[:12]
	}
	return n.ID
}

// Project returns the Compose project that created this network, if any.
func (n Network) Project() string { return n.Labels[LabelProject] }

// Subnet returns the network's configured subnets, joined.
func (n Network) Subnet() string {
	subnets := make([]string, 0, len(n.IPAM.Config))
	for _, config := range n.IPAM.Config {
		if config.Subnet != "" {
			subnets = append(subnets, config.Subnet)
		}
	}
	return strings.Join(subnets, ", ")
}

// Predefined reports whether this is one of the three networks the daemon
// creates and will not let you remove.
//
// Surfacing this matters because "remove" on one of them fails with a daemon
// error rather than doing nothing, and the user has no other way to tell them
// apart from their own networks.
func (n Network) Predefined() bool {
	switch n.Name {
	case "bridge", "host", "none":
		return true
	default:
		return false
	}
}

// Networks lists networks, predefined ones last so a user's own networks come
// first.
func (c *Client) Networks(ctx context.Context) ([]Network, error) {
	var networks []Network
	if err := c.getJSON(ctx, "/networks", nil, &networks); err != nil {
		return nil, err
	}
	sort.SliceStable(networks, func(i, j int) bool {
		a, b := networks[i], networks[j]
		if a.Predefined() != b.Predefined() {
			return !a.Predefined()
		}
		if a.Project() != b.Project() {
			if a.Project() == "" {
				return false
			}
			if b.Project() == "" {
				return true
			}
			return a.Project() < b.Project()
		}
		return a.Name < b.Name
	})
	return networks, nil
}

// RemoveNetwork deletes a network.
func (c *Client) RemoveNetwork(ctx context.Context, id string) error {
	body, err := c.do(ctx, http.MethodDelete, "/networks/"+id, nil, nil)
	if err != nil {
		return err
	}
	return body.Close()
}

// PruneNetworks removes networks no container is attached to.
//
// The daemon reports no reclaimed space for networks — they occupy no
// meaningful disk — so only the count is filled in.
func (c *Client) PruneNetworks(ctx context.Context) (PruneResult, error) {
	var payload struct {
		NetworksDeleted []string `json:"NetworksDeleted"`
	}
	body, err := c.do(ctx, http.MethodPost, "/networks/prune", url.Values{}, nil)
	if err != nil {
		return PruneResult{}, err
	}
	defer body.Close()
	if err := decodeJSON(body, &payload); err != nil {
		return PruneResult{}, err
	}
	return PruneResult{Deleted: len(payload.NetworksDeleted)}, nil
}
