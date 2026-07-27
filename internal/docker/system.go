package docker

import (
	"context"
	"net/http"
	"net/url"
	"sort"
)

// Info is the subset of /info describing the host the daemon runs on.
//
// With Docker Desktop, Colima or any other VM-backed setup this describes the
// virtual machine, not the physical machine — which is the boundary that
// actually constrains the containers, and so the one worth reporting.
type Info struct {
	Name              string `json:"Name"`
	ServerVersion     string `json:"ServerVersion"`
	OperatingSystem   string `json:"OperatingSystem"`
	OSType            string `json:"OSType"`
	Architecture      string `json:"Architecture"`
	KernelVersion     string `json:"KernelVersion"`
	Driver            string `json:"Driver"`
	DockerRootDir     string `json:"DockerRootDir"`
	NCPU              int    `json:"NCPU"`
	MemTotal          int64  `json:"MemTotal"`
	Containers        int    `json:"Containers"`
	ContainersRunning int    `json:"ContainersRunning"`
	ContainersPaused  int    `json:"ContainersPaused"`
	ContainersStopped int    `json:"ContainersStopped"`
	Images            int    `json:"Images"`
}

// SystemInfo returns the daemon's view of its host.
func (c *Client) SystemInfo(ctx context.Context) (Info, error) {
	var info Info
	err := c.getJSON(ctx, "/info", nil, &info)
	return info, err
}

// DiskUsage summarises what Docker is storing on disk.
type DiskUsage struct {
	// ImagesSize is the deduplicated size of all image layers. It is not
	// the sum of the images' individual sizes, which double-counts every
	// shared base layer.
	ImagesSize int64
	// ImagesReclaimable is the size of images no container references.
	ImagesReclaimable int64
	ImagesCount       int
	ImagesUnused      int

	// ContainersSize counts only the containers' writable layers; their
	// read-only layers are already counted in ImagesSize.
	ContainersSize  int64
	ContainersCount int

	VolumesSize   int64
	VolumesCount  int
	VolumesUnused int

	BuildCacheSize int64

	// VolumeSizesKnown reports whether the daemon supplied volume sizes.
	// Some configurations return no usage data at all, and a bare "0B"
	// would read as "nothing stored" rather than "not measured".
	VolumeSizesKnown bool
}

// Total returns the combined size of everything Docker is storing.
func (d DiskUsage) Total() int64 {
	return d.ImagesSize + d.ContainersSize + d.VolumesSize + d.BuildCacheSize
}

// Reclaimable returns how much the default prune would free: images no
// container references, plus the whole build cache.
//
// Unreferenced volumes are deliberately excluded. They hold data that cannot
// be rebuilt, prune leaves them alone unless explicitly asked, and counting
// them here would promise space that the obvious action does not deliver.
func (d DiskUsage) Reclaimable() int64 {
	return d.ImagesReclaimable + d.BuildCacheSize
}

// ReclaimableSource is one contributor to the reclaimable total.
type ReclaimableSource struct {
	Label string
	Size  int64
}

// ReclaimableSources breaks the reclaimable total down by where the space
// actually sits, largest first.
//
// The total on its own is not actionable: build cache is frequently the bulk
// of it and lives nowhere in the interface, so a figure attributed to images
// sends the reader to delete images and wonder why the number does not move.
// Largest first so that a truncated line still names the thing worth doing
// something about.
func (d DiskUsage) ReclaimableSources() []ReclaimableSource {
	var sources []ReclaimableSource
	if d.BuildCacheSize > 0 {
		sources = append(sources, ReclaimableSource{"build cache", d.BuildCacheSize})
	}
	if d.ImagesReclaimable > 0 {
		sources = append(sources, ReclaimableSource{"unused images", d.ImagesReclaimable})
	}
	sort.Slice(sources, func(i, j int) bool { return sources[i].Size > sources[j].Size })
	return sources
}

// diskUsagePayload mirrors the /system/df response.
type diskUsagePayload struct {
	LayersSize int64 `json:"LayersSize"`
	Images     []struct {
		Size       int64 `json:"Size"`
		SharedSize int64 `json:"SharedSize"`
		Containers int64 `json:"Containers"`
	} `json:"Images"`
	Containers []struct {
		SizeRw int64 `json:"SizeRw"`
	} `json:"Containers"`
	Volumes []struct {
		UsageData *struct {
			Size     int64 `json:"Size"`
			RefCount int64 `json:"RefCount"`
		} `json:"UsageData"`
	} `json:"Volumes"`
	BuildCache []struct {
		Size int64 `json:"Size"`
	} `json:"BuildCache"`
}

// DiskUsage reports what Docker is storing, the equivalent of
// "docker system df".
//
// This endpoint walks the storage driver, so it is markedly slower than the
// rest of the API — seconds on a host with many layers. Callers should refresh
// it on a slower cadence than the container list.
func (c *Client) DiskUsage(ctx context.Context) (DiskUsage, error) {
	var payload diskUsagePayload
	// Without the type filter the daemon computes everything anyway; the
	// explicit query keeps the request shape stable across API versions.
	query := url.Values{}
	body, err := c.do(ctx, http.MethodGet, "/system/df", query, nil)
	if err != nil {
		return DiskUsage{}, err
	}
	defer body.Close()
	if err := decodeJSON(body, &payload); err != nil {
		return DiskUsage{}, err
	}

	usage := DiskUsage{
		// LayersSize is already deduplicated, unlike the per-image sizes.
		ImagesSize:  payload.LayersSize,
		ImagesCount: len(payload.Images),
	}

	for _, image := range payload.Images {
		if image.Containers == 0 {
			usage.ImagesUnused++
			// Matches how the CLI attributes reclaimable space: an
			// unused image can free its own layers, though shared
			// ones survive until the last referent goes.
			usage.ImagesReclaimable += image.Size - image.SharedSize
		}
	}

	usage.ContainersCount = len(payload.Containers)
	for _, container := range payload.Containers {
		usage.ContainersSize += container.SizeRw
	}

	usage.VolumesCount = len(payload.Volumes)
	for _, volume := range payload.Volumes {
		if volume.UsageData == nil {
			continue
		}
		usage.VolumeSizesKnown = true
		if volume.UsageData.Size > 0 {
			usage.VolumesSize += volume.UsageData.Size
		}
		if volume.UsageData.RefCount == 0 {
			usage.VolumesUnused++
		}
	}

	for _, cache := range payload.BuildCache {
		usage.BuildCacheSize += cache.Size
	}
	return usage, nil
}
