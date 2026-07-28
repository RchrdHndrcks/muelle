package docker

import (
	"strconv"
	"strings"
	"time"
)

// humanUnits maps the units the daemon writes into its status text to their
// durations. Months and years are the daemon's own approximations — it counts
// a month as thirty days and a year as 365 — and are reproduced here so the
// value round-trips to what "docker ps" would have shown.
var humanUnits = map[string]time.Duration{
	"second": time.Second,
	"minute": time.Minute,
	"hour":   time.Hour,
	"day":    24 * time.Hour,
	"week":   7 * 24 * time.Hour,
	"month":  30 * 24 * time.Hour,
	"year":   365 * 24 * time.Hour,
}

// Uptime returns how long the container has been running, and whether it is
// running at all.
//
// This is the figure that answers "did my restart actually take effect?",
// which Created cannot: restarting a container reuses it, so its creation time
// never moves. Only the current run's start time does.
//
// The list endpoint does not report that start time as a field — it is in the
// inspect payload, which would mean one request per container on every
// refresh. But the daemon has already rendered it into the status text as
// "Up 8 weeks", so reading it back from there costs nothing. The price is the
// daemon's own granularity: a container up for eight weeks reports eight
// weeks, not the exact hour. That is precise where it matters, since anything
// recently restarted is measured in seconds and minutes.
func (c Container) Uptime() (time.Duration, bool) {
	text, ok := strings.CutPrefix(c.Status, "Up ")
	if !ok {
		// Everything else — "Exited (0) 2 days ago", "Restarting (1) 3
		// seconds ago", "Created" — describes a container that is not
		// running, and so has no uptime to report.
		return 0, false
	}

	// Health and pause markers are appended in parentheses: "Up 2 hours
	// (healthy)".
	if open := strings.Index(text, " ("); open >= 0 {
		text = text[:open]
	}

	duration, ok := parseHumanDuration(text)
	if !ok {
		return 0, false
	}
	return duration, true
}

// parseHumanDuration reverses the daemon's human-readable duration format.
//
// Three of its cases are irregular prose rather than a number and a unit, and
// are matched literally; the rest are "<n> <unit>".
func parseHumanDuration(text string) (time.Duration, bool) {
	switch text {
	case "Less than a second":
		return 0, true
	case "About a minute":
		return time.Minute, true
	case "About an hour":
		return time.Hour, true
	}

	count, unit, found := strings.Cut(text, " ")
	if !found {
		return 0, false
	}
	n, err := strconv.Atoi(count)
	if err != nil {
		return 0, false
	}
	// Both "1 second" and "45 seconds" occur, so the plural is optional
	// rather than a different unit.
	size, known := humanUnits[strings.TrimSuffix(unit, "s")]
	if !known {
		return 0, false
	}
	return time.Duration(n) * size, true
}
