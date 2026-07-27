package ui

import (
	"sort"
	"strings"

	"github.com/RchrdHndrcks/muelle/internal/docker"
)

// SortKey is how the container list is ordered.
type SortKey int

// Available orderings, in the order the cycle key visits them.
const (
	// SortDefault groups by Compose project, then name. It is what makes
	// the list readable as a set of stacks rather than a flat inventory.
	SortDefault SortKey = iota
	SortCPU
	SortMemory
	SortAge
	SortState
	sortKeyCount
)

// Label names the ordering for the status bar.
func (s SortKey) Label() string {
	switch s {
	case SortDefault:
		return "project"
	case SortCPU:
		return "cpu"
	case SortMemory:
		return "memory"
	case SortAge:
		return "age"
	case SortState:
		return "state"
	default:
		return "?"
	}
}

// Next returns the following ordering, wrapping around.
func (s SortKey) Next() SortKey { return (s + 1) % sortKeyCount }

// ParseSortKey recognises an ordering by the name it goes by in the
// interface, reporting whether it was one.
//
// The names are the labels the status bar shows, so what is written in the
// configuration file is what the application calls it.
func ParseSortKey(name string) (SortKey, bool) {
	for key := SortDefault; key < sortKeyCount; key++ {
		if key.Label() == name {
			return key, true
		}
	}
	return SortDefault, false
}

// Descending reports whether the key sorts largest first.
//
// The interesting end differs per key: for a resource you want the biggest
// consumer at the top, for a name you want alphabetical order. Making this a
// property of the key means the user never has to think about direction.
func (s SortKey) Descending() bool {
	return s == SortCPU || s == SortMemory || s == SortAge
}

// SortContainers orders containers in place by the given key.
//
// stats supplies the resource samples, which arrive asynchronously and may be
// missing for a container that just started. Missing samples sort last under a
// resource ordering rather than being treated as zero, so an unmeasured
// container does not masquerade as an idle one.
func SortContainers(containers []docker.Container, key SortKey, stats map[string]docker.Stat) {
	// Stable, so containers that compare equal keep their previous relative
	// order and the list does not shuffle between refreshes.
	sort.SliceStable(containers, func(i, j int) bool {
		a, b := containers[i], containers[j]

		switch key {
		case SortCPU:
			statA, okA := stats[a.ID]
			statB, okB := stats[b.ID]
			if okA != okB {
				return okA
			}
			if statA.CPUPercent != statB.CPUPercent {
				return statA.CPUPercent > statB.CPUPercent
			}

		case SortMemory:
			statA, okA := stats[a.ID]
			statB, okB := stats[b.ID]
			if okA != okB {
				return okA
			}
			if statA.MemUsage != statB.MemUsage {
				return statA.MemUsage > statB.MemUsage
			}

		case SortAge:
			if a.Created != b.Created {
				// Newest first: a container that just started is what
				// you are most likely looking for.
				return a.Created > b.Created
			}

		case SortState:
			if rank := stateRank(a.State) - stateRank(b.State); rank != 0 {
				return rank < 0
			}

		case SortDefault:
			if a.Project() != b.Project() {
				// Standalone containers sort last, as in the default
				// listing.
				if a.Project() == "" {
					return false
				}
				if b.Project() == "" {
					return true
				}
				return a.Project() < b.Project()
			}
		}

		// Name is the tie-breaker for every ordering, so equal rows have a
		// deterministic position rather than depending on daemon order.
		return strings.ToLower(a.Name()) < strings.ToLower(b.Name())
	})
}

// stateRank orders states by how much they warrant attention: things that are
// broken first, things that are fine last.
func stateRank(state string) int {
	switch state {
	case "dead":
		return 0
	case "restarting":
		return 1
	case "paused":
		return 2
	case "exited":
		return 3
	case "created":
		return 4
	case "removing":
		return 5
	case "running":
		return 6
	default:
		return 7
	}
}
