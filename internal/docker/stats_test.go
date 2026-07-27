package docker

import (
	"encoding/json"
	"math"
	"testing"
)

// sample builds a StatSample from the JSON shape the daemon actually sends,
// so the test exercises decoding as well as the arithmetic.
func sample(t *testing.T, raw string) StatSample {
	t.Helper()
	var s StatSample
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return s
}

func TestCalculateCPUPercent(t *testing.T) {
	// The container consumed 2 of the 10 nanoseconds the system had
	// available, across 4 CPUs: 20% * 4 = 80%.
	s := sample(t, `{
		"cpu_stats":    {"cpu_usage": {"total_usage": 12}, "system_cpu_usage": 110, "online_cpus": 4},
		"precpu_stats": {"cpu_usage": {"total_usage": 10}, "system_cpu_usage": 100, "online_cpus": 4}
	}`)

	got := s.Calculate().CPUPercent
	if math.Abs(got-80.0) > 0.001 {
		t.Errorf("got %.3f%%, want 80%%", got)
	}
}

// Older daemons omit online_cpus, leaving the per-CPU array as the only
// indication of how many cores are in play.
func TestCalculateCPUFallsBackToPerCPUArray(t *testing.T) {
	s := sample(t, `{
		"cpu_stats":    {"cpu_usage": {"total_usage": 12, "percpu_usage": [1,2]}, "system_cpu_usage": 110},
		"precpu_stats": {"cpu_usage": {"total_usage": 10}, "system_cpu_usage": 100}
	}`)

	got := s.Calculate().CPUPercent
	if math.Abs(got-40.0) > 0.001 {
		t.Errorf("got %.3f%%, want 40%% (2 CPUs inferred from percpu_usage)", got)
	}
}

// The first sample after a container starts has no baseline, which would
// otherwise divide by zero.
func TestCalculateHandlesMissingBaseline(t *testing.T) {
	s := sample(t, `{
		"cpu_stats":    {"cpu_usage": {"total_usage": 12}, "system_cpu_usage": 110, "online_cpus": 4},
		"precpu_stats": {"cpu_usage": {"total_usage": 0}, "system_cpu_usage": 0}
	}`)

	if got := s.Calculate().CPUPercent; got != 0 {
		t.Errorf("got %v, want 0 with no baseline to compare against", got)
	}
}

// "docker stats" reports memory net of page cache; reporting raw usage makes
// every database look like it is about to OOM.
func TestCalculateSubtractsPageCacheFromMemory(t *testing.T) {
	s := sample(t, `{
		"memory_stats": {"usage": 1000, "limit": 4000, "stats": {"inactive_file": 400}}
	}`)

	stat := s.Calculate()
	if stat.MemUsage != 600 {
		t.Errorf("got %d bytes, want 600 (1000 usage - 400 cache)", stat.MemUsage)
	}
	if math.Abs(stat.MemPercent-15.0) > 0.001 {
		t.Errorf("got %.2f%%, want 15%%", stat.MemPercent)
	}
}

func TestCalculateUsesCgroupV1MemoryFieldNames(t *testing.T) {
	s := sample(t, `{
		"memory_stats": {"usage": 1000, "limit": 4000, "stats": {"total_inactive_file": 250}}
	}`)

	if got := s.Calculate().MemUsage; got != 750 {
		t.Errorf("got %d, want 750 (cgroup v1 field honoured)", got)
	}
}

// A cache value exceeding usage would underflow the unsigned subtraction into
// an absurd number.
func TestCalculateClampsMemoryUnderflow(t *testing.T) {
	s := sample(t, `{
		"memory_stats": {"usage": 100, "limit": 4000, "stats": {"inactive_file": 500}}
	}`)

	if got := s.Calculate().MemUsage; got != 0 {
		t.Errorf("got %d, want 0 rather than an underflowed value", got)
	}
}

func TestCalculateSumsNetworkInterfaces(t *testing.T) {
	s := sample(t, `{
		"networks": {
			"eth0": {"rx_bytes": 100, "tx_bytes": 200},
			"eth1": {"rx_bytes": 50,  "tx_bytes": 25}
		}
	}`)

	stat := s.Calculate()
	if stat.NetRx != 150 || stat.NetTx != 225 {
		t.Errorf("got rx=%d tx=%d, want rx=150 tx=225", stat.NetRx, stat.NetTx)
	}
}

func TestCalculateHandlesMissingMemoryLimit(t *testing.T) {
	s := sample(t, `{"memory_stats": {"usage": 1000, "limit": 0}}`)

	if got := s.Calculate().MemPercent; got != 0 {
		t.Errorf("got %v, want 0 rather than a division by zero", got)
	}
}
