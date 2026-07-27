package docker

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// PruneScope selects how much a system prune removes.
type PruneScope struct {
	// Volumes includes unreferenced volumes. Off by default and kept
	// separate because volumes hold data: every other prune target can be
	// rebuilt or re-pulled, a volume cannot.
	Volumes bool
	// AllImages removes every unused image rather than only dangling ones,
	// matching "docker system prune -a".
	AllImages bool
}

// SystemPruneResult reports what a system prune removed, per object type.
type SystemPruneResult struct {
	Containers     int
	Images         int
	Networks       int
	Volumes        int
	BuildCache     int64
	SpaceReclaimed int64
	// Errors holds failures from individual steps. A prune is several
	// independent calls, and one failing is no reason to hide what the
	// others reclaimed.
	Errors []error
}

// SystemPrune removes unused data, the equivalent of "docker system prune".
//
// The Engine API has no single prune endpoint; the CLI calls each object
// type's endpoint in turn, and so does this. The order matters: containers
// first, so images and networks they were holding become unreferenced and can
// go in the same pass.
//
// Steps are independent. A failure in one is recorded and the rest still run,
// because a prune that stops halfway leaves the user with no idea what was
// removed.
func (c *Client) SystemPrune(ctx context.Context, scope PruneScope) SystemPruneResult {
	var result SystemPruneResult

	if containers, err := c.pruneContainers(ctx); err != nil {
		result.Errors = append(result.Errors, err)
	} else {
		result.Containers = containers.Deleted
		result.SpaceReclaimed += containers.SpaceReclaimed
	}

	images, err := c.pruneImagesScoped(ctx, scope.AllImages)
	if err != nil {
		result.Errors = append(result.Errors, err)
	} else {
		result.Images = images.Deleted
		result.SpaceReclaimed += images.SpaceReclaimed
	}

	if networks, err := c.PruneNetworks(ctx); err != nil {
		result.Errors = append(result.Errors, err)
	} else {
		result.Networks = networks.Deleted
	}

	if cache, err := c.pruneBuildCache(ctx); err != nil {
		result.Errors = append(result.Errors, err)
	} else {
		result.BuildCache = cache
		result.SpaceReclaimed += cache
	}

	if scope.Volumes {
		if volumes, err := c.PruneVolumes(ctx); err != nil {
			result.Errors = append(result.Errors, err)
		} else {
			result.Volumes = volumes.Deleted
			result.SpaceReclaimed += volumes.SpaceReclaimed
		}
	}
	return result
}

// pruneContainers removes stopped containers.
func (c *Client) pruneContainers(ctx context.Context) (PruneResult, error) {
	var payload struct {
		ContainersDeleted []string `json:"ContainersDeleted"`
		SpaceReclaimed    int64    `json:"SpaceReclaimed"`
	}
	body, err := c.do(ctx, http.MethodPost, "/containers/prune", nil, nil)
	if err != nil {
		return PruneResult{}, err
	}
	defer body.Close()
	if err := decodeJSON(body, &payload); err != nil {
		return PruneResult{}, err
	}
	return PruneResult{
		Deleted:        len(payload.ContainersDeleted),
		SpaceReclaimed: payload.SpaceReclaimed,
	}, nil
}

// pruneImagesScoped removes dangling images, or every unused image when all is
// set.
func (c *Client) pruneImagesScoped(ctx context.Context, all bool) (PruneResult, error) {
	query := url.Values{}
	if all {
		// The filter is inverted: dangling=false means "not only
		// dangling", which is how the CLI implements -a.
		query.Set("filters", `{"dangling":{"false":true}}`)
	}
	var payload struct {
		ImagesDeleted []struct {
			Deleted string `json:"Deleted"`
		} `json:"ImagesDeleted"`
		SpaceReclaimed int64 `json:"SpaceReclaimed"`
	}
	body, err := c.do(ctx, http.MethodPost, "/images/prune", query, nil)
	if err != nil {
		return PruneResult{}, err
	}
	defer body.Close()
	if err := decodeJSON(body, &payload); err != nil {
		return PruneResult{}, err
	}
	return PruneResult{
		Deleted:        len(payload.ImagesDeleted),
		SpaceReclaimed: payload.SpaceReclaimed,
	}, nil
}

// pruneBuildCache clears the builder cache and returns the bytes freed.
func (c *Client) pruneBuildCache(ctx context.Context) (int64, error) {
	var payload struct {
		SpaceReclaimed int64 `json:"SpaceReclaimed"`
	}
	body, err := c.do(ctx, http.MethodPost, "/build/prune", nil, nil)
	if err != nil {
		return 0, err
	}
	defer body.Close()
	if err := decodeJSON(body, &payload); err != nil {
		return 0, err
	}
	return payload.SpaceReclaimed, nil
}

// Summary describes what a prune removed, in a form fit for a status bar.
func (r SystemPruneResult) Summary() string {
	parts := make([]string, 0, 5)
	if r.Containers > 0 {
		parts = append(parts, plural(r.Containers, "container", "containers"))
	}
	if r.Images > 0 {
		parts = append(parts, plural(r.Images, "image", "images"))
	}
	if r.Networks > 0 {
		parts = append(parts, plural(r.Networks, "network", "networks"))
	}
	if r.Volumes > 0 {
		parts = append(parts, plural(r.Volumes, "volume", "volumes"))
	}
	if len(parts) == 0 && r.SpaceReclaimed == 0 {
		return "nothing to prune"
	}
	summary := "pruned"
	if len(parts) > 0 {
		summary += " " + strings.Join(parts, ", ")
	}
	return summary + ", " + formatBytes(r.SpaceReclaimed) + " reclaimed"
}

// plural renders a count with the matching noun.
func plural(count int, singular, many string) string {
	if count == 1 {
		return strconv.Itoa(count) + " " + singular
	}
	return strconv.Itoa(count) + " " + many
}

// formatBytes renders a byte count in binary units. The docker package cannot
// import the ui package, so this small duplicate keeps the dependency pointing
// one way.
func formatBytes(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return strconv.FormatInt(bytes, 10) + "B"
	}
	value := float64(bytes)
	units := []string{"KiB", "MiB", "GiB", "TiB"}
	index := -1
	for value >= unit && index < len(units)-1 {
		value /= unit
		index++
	}
	return strconv.FormatFloat(value, 'f', 1, 64) + units[index]
}
