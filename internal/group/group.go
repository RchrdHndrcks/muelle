// Package group works out which containers belong to the same application.
//
// Docker has no notion of an application. Compose comes closest, stamping a
// project label on everything it creates, but a great many containers are
// started with "docker run" and carry nothing at all — and those are exactly
// the ones a person still thinks of as belonging together, because they named
// them that way: shop-db and shop-db-replica are one application to everyone
// except the daemon.
//
// So the grouping is read from two places, in that order: the Compose project
// where there is one, and the container's own name where there is not.
package group

import (
	"sort"
	"strings"

	"github.com/RchrdHndrcks/muelle/internal/docker"
)

// Source is where a group's identity came from.
type Source int

const (
	// FromCompose is a Compose project: the user has already said these
	// containers belong together.
	FromCompose Source = iota
	// FromPrefix is a name shared by several containers.
	FromPrefix
	// Ungrouped is the bucket for everything that belongs to nothing.
	Ungrouped
)

// ungroupedName labels the bucket. It is not a group anyone made, so it is
// named for what it is rather than given a plausible-looking title.
const ungroupedName = "ungrouped"

// Group is one application's containers.
type Group struct {
	Name       string
	Source     Source
	Containers []docker.Container
}

// minimumMembers is how many containers a shared prefix needs before it counts
// as an application.
//
// One container with a hyphen in its name is not an application; it is a
// container with a hyphen in its name. Without this, a host carrying a few
// dozen one-off containers turns into a few dozen one-member groups, which is
// the flat list again with twice as many rows.
const minimumMembers = 2

// Build sorts containers into applications.
//
// Compose projects are formed first and win outright. Remaining containers are
// bucketed by the part of their name before the first hyphen; a bucket joins a
// Compose project of the same name if there is one, becomes a group of its own
// if enough containers share it, and otherwise falls through to ungrouped.
//
// Groups come back in name order with the ungrouped bucket last, which is
// where the eye should reach it: on a working host it is mostly debris.
func Build(containers []docker.Container) []Group {
	byName := make(map[string]*Group)
	var order []string
	var loose []docker.Container

	for _, container := range containers {
		project := container.Project()
		if project == "" {
			loose = append(loose, container)
			continue
		}
		if _, exists := byName[project]; !exists {
			byName[project] = &Group{Name: project, Source: FromCompose}
			order = append(order, project)
		}
		byName[project].Containers = append(byName[project].Containers, container)
	}

	buckets, bucketOrder := bucketByPrefix(loose)

	var ungrouped []docker.Container
	for _, prefix := range bucketOrder {
		members := buckets[prefix]
		if existing, joins := byName[prefix]; joins {
			// A container someone started by hand and named after the
			// stack it serves belongs to that stack, not beside it.
			existing.Containers = append(existing.Containers, members...)
			continue
		}
		if len(members) < minimumMembers {
			ungrouped = append(ungrouped, members...)
			continue
		}
		byName[prefix] = &Group{Name: prefix, Source: FromPrefix, Containers: members}
		order = append(order, prefix)
	}

	sort.Strings(order)
	groups := make([]Group, 0, len(order)+1)
	for _, name := range order {
		groups = append(groups, *byName[name])
	}
	if len(ungrouped) > 0 {
		groups = append(groups, Group{
			Name:       ungroupedName,
			Source:     Ungrouped,
			Containers: ungrouped,
		})
	}
	return groups
}

// bucketByPrefix groups containers by the part of their name before the first
// hyphen, keeping the order they were seen in.
//
// The hyphen is the whole rule, and it is enough: it is what Compose uses to
// join a project to a service, and what people reach for when naming by hand.
// Docker's own generated names use underscores, so a host full of
// agitated_gagarin and crazy_bhabha produces no prefixes worth the name and
// needs no special case.
func bucketByPrefix(containers []docker.Container) (map[string][]docker.Container, []string) {
	buckets := make(map[string][]docker.Container)
	var order []string

	for _, container := range containers {
		name := container.Name()
		prefix, _, _ := strings.Cut(name, "-")
		if prefix == "" {
			prefix = name
		}
		if _, seen := buckets[prefix]; !seen {
			order = append(order, prefix)
		}
		buckets[prefix] = append(buckets[prefix], container)
	}
	return buckets, order
}

// Shorten drops the application's name from the front of a container's,
// leaving the part that says which container it is.
//
// Under a heading that already reads "shop", every row repeating it spends the
// widest column in the list on the word the eye has just read. What remains —
// db, db-replica, api — is what actually distinguishes one row from the next.
//
// Only the group's own name followed by a hyphen is removed. A container whose
// name does not begin with it is left alone: a Compose project can be called
// anything, and its containers need not be named after it, so trimming
// anything else would be inventing a name rather than shortening one.
func Shorten(name, group string) string {
	if group == "" || name == group {
		return name
	}
	short, found := strings.CutPrefix(name, group+"-")
	if !found || short == "" {
		return name
	}
	return short
}
