package docker

import (
	"testing"
	"time"
)

func TestUptimeParsedFromStatusText(t *testing.T) {
	cases := map[string]struct {
		uptime  time.Duration
		running bool
	}{
		"Up Less than a second": {0, true},
		"Up 1 second":           {time.Second, true},
		"Up 45 seconds":         {45 * time.Second, true},
		"Up About a minute":     {time.Minute, true},
		"Up 3 minutes":          {3 * time.Minute, true},
		"Up About an hour":      {time.Hour, true},
		"Up 5 hours":            {5 * time.Hour, true},
		"Up 3 days":             {72 * time.Hour, true},
		"Up 8 weeks":            {8 * 7 * 24 * time.Hour, true},
		"Up 5 months":           {5 * 30 * 24 * time.Hour, true},
		"Up 2 years":            {2 * 365 * 24 * time.Hour, true},

		// Nothing that is not currently running has an uptime.
		"Exited (0) 2 days ago":        {0, false},
		"Exited (137) 5 minutes ago":   {0, false},
		"Restarting (1) 3 seconds ago": {0, false},
		"Created":                      {0, false},
		"Removal In Progress":          {0, false},
		"Dead":                         {0, false},
		"":                             {0, false},
	}

	for status, want := range cases {
		uptime, running := Container{Status: status}.Uptime()
		if uptime != want.uptime || running != want.running {
			t.Errorf("Status %q gave (%v, %v), want (%v, %v)",
				status, uptime, running, want.uptime, want.running)
		}
	}
}

// The health marker the daemon appends must not defeat the parse; it is the
// common case for any image that defines a healthcheck.
func TestUptimeIgnoresTrailingMarkers(t *testing.T) {
	cases := map[string]time.Duration{
		"Up 2 hours (healthy)":          2 * time.Hour,
		"Up 2 hours (unhealthy)":        2 * time.Hour,
		"Up 30 seconds (health: start)": 30 * time.Second,
		"Up 4 days (Paused)":            96 * time.Hour,
	}

	for status, want := range cases {
		uptime, running := Container{Status: status}.Uptime()
		if !running || uptime != want {
			t.Errorf("Status %q gave (%v, %v), want (%v, true)",
				status, uptime, running, want)
		}
	}
}

// A status shaped like "Up ..." but carrying something unparseable should
// report no uptime rather than a wrong one.
func TestUptimeRejectsUnparseableDurations(t *testing.T) {
	for _, status := range []string{
		"Up",
		"Up ",
		"Up forever",
		"Up 3 fortnights",
		"Up many hours",
	} {
		if uptime, running := (Container{Status: status}).Uptime(); running {
			t.Errorf("Status %q gave (%v, true), want no uptime", status, uptime)
		}
	}
}
