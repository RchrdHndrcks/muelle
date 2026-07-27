package docker

import (
	"context"
	"net/http"
	"testing"
)

func TestSystemInfoDecodesHostFacts(t *testing.T) {
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{
			"Name": "vm-host", "ServerVersion": "27.4.0", "NCPU": 4,
			"MemTotal": 8589934592, "Containers": 12, "ContainersRunning": 9,
			"ContainersStopped": 3, "Images": 40,
			"OperatingSystem": "Ubuntu 24.04.1 LTS", "OSType": "linux",
			"Architecture": "aarch64"
		}`))
	})

	info, err := client.SystemInfo(context.Background())
	if err != nil {
		t.Fatalf("SystemInfo: %v", err)
	}
	if info.NCPU != 4 || info.MemTotal != 8589934592 {
		t.Errorf("got %d CPUs and %d bytes, want 4 and 8GiB", info.NCPU, info.MemTotal)
	}
	if info.ContainersRunning != 9 || info.Name != "vm-host" {
		t.Errorf("got %+v, want the host counts decoded", info)
	}
}

// The sum of image sizes double-counts every shared base layer; LayersSize is
// the deduplicated figure and the only honest total.
func TestDiskUsageUsesDeduplicatedLayerSize(t *testing.T) {
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{
			"LayersSize": 1000,
			"Images": [
				{"Size": 800, "SharedSize": 300, "Containers": 1},
				{"Size": 700, "SharedSize": 300, "Containers": 0}
			],
			"Containers": [{"SizeRw": 50}, {"SizeRw": 25}],
			"Volumes": [{"UsageData": {"Size": 200, "RefCount": 1}}],
			"BuildCache": [{"Size": 400}]
		}`))
	})

	usage, err := client.DiskUsage(context.Background())
	if err != nil {
		t.Fatalf("DiskUsage: %v", err)
	}

	if usage.ImagesSize != 1000 {
		t.Errorf("got %d, want LayersSize rather than the sum of image sizes", usage.ImagesSize)
	}
	if usage.ContainersSize != 75 {
		t.Errorf("got %d, want the writable layers summed", usage.ContainersSize)
	}
	if usage.VolumesSize != 200 {
		t.Errorf("got %d, want 200", usage.VolumesSize)
	}
	if usage.BuildCacheSize != 400 {
		t.Errorf("got %d, want 400", usage.BuildCacheSize)
	}
}

// Only images no container references can be reclaimed, and only their
// unshared layers.
func TestDiskUsageCountsOnlyUnusedImagesAsReclaimable(t *testing.T) {
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{
			"LayersSize": 1000,
			"Images": [
				{"Size": 800, "SharedSize": 300, "Containers": 2},
				{"Size": 700, "SharedSize": 200, "Containers": 0}
			],
			"BuildCache": [{"Size": 100}]
		}`))
	})

	usage, err := client.DiskUsage(context.Background())
	if err != nil {
		t.Fatalf("DiskUsage: %v", err)
	}

	if usage.ImagesUnused != 1 {
		t.Errorf("got %d unused images, want 1", usage.ImagesUnused)
	}
	// 700 - 200 shared, from the unused image only.
	if usage.ImagesReclaimable != 500 {
		t.Errorf("got %d reclaimable, want 500 (unshared layers of the unused image)",
			usage.ImagesReclaimable)
	}
	if usage.Reclaimable() != 600 {
		t.Errorf("got %d, want 600 (unused images plus the build cache)", usage.Reclaimable())
	}
}

// Some daemons report no usage data at all, and a bare 0B would read as
// "nothing stored" rather than "not measured".
func TestDiskUsageFlagsUnmeasuredVolumes(t *testing.T) {
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{
			"LayersSize": 100,
			"Volumes": [{"Name": "data"}, {"Name": "cache"}]
		}`))
	})

	usage, err := client.DiskUsage(context.Background())
	if err != nil {
		t.Fatalf("DiskUsage: %v", err)
	}
	if usage.VolumeSizesKnown {
		t.Error("volumes without usage data should not report a known size")
	}
	if usage.VolumesCount != 2 {
		t.Errorf("got %d volumes, want them still counted", usage.VolumesCount)
	}
}

func TestDiskUsageCountsUnreferencedVolumes(t *testing.T) {
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{
			"LayersSize": 0,
			"Volumes": [
				{"UsageData": {"Size": 100, "RefCount": 1}},
				{"UsageData": {"Size": 50, "RefCount": 0}}
			]
		}`))
	})

	usage, _ := client.DiskUsage(context.Background())

	if usage.VolumesUnused != 1 {
		t.Errorf("got %d unused volumes, want 1", usage.VolumesUnused)
	}
	if usage.VolumesSize != 150 {
		t.Errorf("got %d, want both volumes counted toward the total", usage.VolumesSize)
	}
}

// A negative size means "unknown" in some daemon versions and must not be
// subtracted from the total.
func TestDiskUsageIgnoresNegativeVolumeSizes(t *testing.T) {
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{
			"LayersSize": 0,
			"Volumes": [{"UsageData": {"Size": -1, "RefCount": 1}}]
		}`))
	})

	usage, _ := client.DiskUsage(context.Background())

	if usage.VolumesSize != 0 {
		t.Errorf("got %d, want a negative reading ignored", usage.VolumesSize)
	}
}

func TestDiskUsageTotalSumsEverything(t *testing.T) {
	usage := DiskUsage{
		ImagesSize:     1000,
		ContainersSize: 100,
		VolumesSize:    200,
		BuildCacheSize: 50,
	}

	if got := usage.Total(); got != 1350 {
		t.Errorf("got %d, want 1350", got)
	}
}

func TestDiskUsageHandlesEmptyDaemon(t *testing.T) {
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"LayersSize": 0}`))
	})

	usage, err := client.DiskUsage(context.Background())
	if err != nil {
		t.Fatalf("DiskUsage: %v", err)
	}
	if usage.Total() != 0 || usage.Reclaimable() != 0 {
		t.Errorf("got %+v, want zeroes on an empty daemon", usage)
	}
}
