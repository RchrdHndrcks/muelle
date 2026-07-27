package docker

import (
	"context"
	"net/http"
	"net/url"
	"sync"
)

// StatSample is the raw /containers/{id}/stats payload, in the shape the
// daemon reports it.
type StatSample struct {
	Read     string `json:"read"`
	CPUStats struct {
		CPUUsage struct {
			TotalUsage  uint64   `json:"total_usage"`
			PerCPUUsage []uint64 `json:"percpu_usage"`
		} `json:"cpu_usage"`
		SystemUsage uint64 `json:"system_cpu_usage"`
		OnlineCPUs  uint32 `json:"online_cpus"`
	} `json:"cpu_stats"`
	PreCPUStats struct {
		CPUUsage struct {
			TotalUsage  uint64   `json:"total_usage"`
			PerCPUUsage []uint64 `json:"percpu_usage"`
		} `json:"cpu_usage"`
		SystemUsage uint64 `json:"system_cpu_usage"`
		OnlineCPUs  uint32 `json:"online_cpus"`
	} `json:"precpu_stats"`
	MemoryStats struct {
		Usage uint64            `json:"usage"`
		Limit uint64            `json:"limit"`
		Stats map[string]uint64 `json:"stats"`
	} `json:"memory_stats"`
	Networks map[string]struct {
		RxBytes uint64 `json:"rx_bytes"`
		TxBytes uint64 `json:"tx_bytes"`
	} `json:"networks"`
}

// Stat is the resource usage of one container, derived from a StatSample.
type Stat struct {
	CPUPercent float64
	MemUsage   uint64
	MemLimit   uint64
	MemPercent float64
	NetRx      uint64
	NetTx      uint64
}

// Calculate derives displayable figures from a raw sample.
//
// CPU percentage is a delta between two points in time: the container's own
// consumed nanoseconds over the system's total available nanoseconds, scaled
// by the CPU count so a container saturating 2 of 8 cores reads as 200%,
// matching "docker stats".
func (s StatSample) Calculate() Stat {
	stat := Stat{
		MemLimit: s.MemoryStats.Limit,
		MemUsage: s.memoryUsage(),
	}

	// A zero previous system reading means there is no earlier sample to
	// diff against: the counter is host CPU-nanoseconds since boot and is
	// never legitimately zero on a live host. Without this guard the
	// formula silently degrades into "share of all CPU time since boot",
	// which is not what the column claims to show.
	if s.PreCPUStats.SystemUsage > 0 {
		cpuDelta := float64(s.CPUStats.CPUUsage.TotalUsage) - float64(s.PreCPUStats.CPUUsage.TotalUsage)
		systemDelta := float64(s.CPUStats.SystemUsage) - float64(s.PreCPUStats.SystemUsage)
		if cpuDelta > 0 && systemDelta > 0 {
			stat.CPUPercent = (cpuDelta / systemDelta) * float64(s.cpuCount()) * 100.0
		}
	}

	if stat.MemLimit > 0 {
		stat.MemPercent = float64(stat.MemUsage) / float64(stat.MemLimit) * 100.0
	}

	for _, net := range s.Networks {
		stat.NetRx += net.RxBytes
		stat.NetTx += net.TxBytes
	}
	return stat
}

// memoryUsage subtracts page cache from the reported total, which is what
// "docker stats" shows and what people mean by a container's memory use. The
// field naming differs between cgroup versions: v2 reports "inactive_file",
// v1 reports "total_inactive_file" or "cache".
func (s StatSample) memoryUsage() uint64 {
	usage := s.MemoryStats.Usage
	for _, key := range []string{"inactive_file", "total_inactive_file", "cache"} {
		if cached, ok := s.MemoryStats.Stats[key]; ok {
			if cached < usage {
				return usage - cached
			}
			return 0
		}
	}
	return usage
}

// cpuCount reports how many CPUs the container may use. online_cpus is absent
// on older daemons, where the length of the per-CPU array is the fallback.
func (s StatSample) cpuCount() uint32 {
	if s.CPUStats.OnlineCPUs > 0 {
		return s.CPUStats.OnlineCPUs
	}
	if n := len(s.CPUStats.CPUUsage.PerCPUUsage); n > 0 {
		return uint32(n)
	}
	return 1
}

// Stats fetches a single resource-usage sample for one container.
//
// This uses stream=false rather than one-shot=true deliberately: one-shot
// omits the "precpu" block, leaving no baseline to compute a CPU delta
// against, so every reading would be 0%. The trade-off is that the daemon
// holds the request open for about a second while it gathers the second
// sample, which is why callers run this off the UI goroutine.
func (c *Client) Stats(ctx context.Context, id string) (Stat, error) {
	query := url.Values{"stream": {"false"}}
	body, err := c.do(ctx, http.MethodGet, "/containers/"+id+"/stats", query, nil)
	if err != nil {
		return Stat{}, err
	}
	defer body.Close()

	var sample StatSample
	if err := decodeJSON(body, &sample); err != nil {
		return Stat{}, err
	}
	return sample.Calculate(), nil
}

// statsConcurrency bounds how many stats requests are in flight at once. Each
// one occupies a daemon goroutine for roughly a second, so an unbounded fan-out
// over a large container list would hammer the daemon.
const statsConcurrency = 8

// StatsFor samples several containers concurrently, returning what succeeded.
// Containers whose stats could not be read are simply absent from the result:
// a container that exits mid-sample is normal, not an error worth surfacing.
func (c *Client) StatsFor(ctx context.Context, ids []string) map[string]Stat {
	results := make(map[string]Stat, len(ids))
	var (
		mu sync.Mutex
		wg sync.WaitGroup
	)
	sem := make(chan struct{}, statsConcurrency)

	for _, id := range ids {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				return
			}
			stat, err := c.Stats(ctx, id)
			if err != nil {
				return
			}
			mu.Lock()
			results[id] = stat
			mu.Unlock()
		}(id)
	}
	wg.Wait()
	return results
}
