package ui

import (
	"sort"

	"github.com/RchrdHndrcks/muelle/internal/config"
	"github.com/RchrdHndrcks/muelle/internal/docker"
	"github.com/RchrdHndrcks/muelle/internal/group"
)

// Header is a group's summary row.
type Header struct {
	Name   string
	Source group.Source
	// Running and Total count the containers listed under it, not every
	// container that exists: with stopped ones hidden, a header claiming
	// more than the rows beneath it would be describing something the user
	// cannot see.
	Running int
	Total   int
	// CPUPercent and MemUsage are the group's totals, which is the reading
	// grouping exists to make possible: what a whole application costs.
	CPUPercent float64
	MemUsage   uint64
	Collapsed  bool
}

// Row is one line of the container list: a container, or the heading of the
// application the following containers belong to.
type Row struct {
	Container docker.Container
	// Header is nil on a container row.
	Header *Header
}

// rows builds the container list as it is drawn.
//
// With grouping off this is the flat list the view has always shown, wrapped
// one field deep. With it on, each application contributes a header followed
// by its containers, unless it is folded away.
func (a *App) rows() []Row {
	containers := a.filteredContainers()
	if !a.grouped {
		rows := make([]Row, 0, len(containers))
		for _, container := range containers {
			rows = append(rows, Row{Container: container})
		}
		return rows
	}

	// Built from every container on the host, not from the filtered list.
	// Which application a container belongs to is a fact about the host; if
	// the groups were derived from the filter, searching for one container
	// would dissolve the group it is in — it would fall below the minimum
	// members on its own — and the row would move somewhere else as you
	// typed.
	groups := group.Build(a.sortedContainers())
	a.sortGroups(groups)

	rows := make([]Row, 0, len(containers)+len(groups))
	for _, found := range groups {
		matched := a.matching(found.Containers)
		if len(matched) == 0 {
			continue
		}
		header := a.summarise(found)
		// A filter that matched inside a folded group has to open it, or
		// searching for something looks like finding nothing.
		header.Collapsed = a.collapsed[found.Name] && a.filter == ""
		rows = append(rows, Row{Header: &header})
		if header.Collapsed {
			continue
		}
		for _, container := range matched {
			rows = append(rows, Row{Container: container})
		}
	}
	return rows
}

// summarise totals a group for its header.
func (a *App) summarise(found group.Group) Header {
	header := Header{Name: found.Name, Source: found.Source, Total: len(found.Containers)}
	for _, container := range found.Containers {
		if container.Running() {
			header.Running++
		}
		if stat, known := a.stats[container.ID]; known {
			header.CPUPercent += stat.CPUPercent
			header.MemUsage += stat.MemUsage
		}
	}
	return header
}

// sortGroups orders the applications themselves.
//
// By what they cost when that is what the user asked to sort by — the question
// "which application is eating the host" is the one grouping makes answerable —
// and by name otherwise. The ungrouped bucket stays last wherever it lands,
// the same way standalone containers already sort last in the flat list.
func (a *App) sortGroups(groups []group.Group) {
	weight := make(map[string]float64, len(groups))
	for _, found := range groups {
		header := a.summarise(found)
		switch a.sortKey {
		case SortCPU:
			weight[found.Name] = header.CPUPercent
		case SortMemory:
			weight[found.Name] = float64(header.MemUsage)
		}
	}

	sort.SliceStable(groups, func(i, j int) bool {
		left, right := groups[i], groups[j]
		if (left.Source == group.Ungrouped) != (right.Source == group.Ungrouped) {
			return right.Source == group.Ungrouped
		}
		if a.sortKey == SortCPU || a.sortKey == SortMemory {
			return weight[left.Name] > weight[right.Name]
		}
		return left.Name < right.Name
	})
}

// selectedRow returns the highlighted row.
func (a *App) selectedRow() (Row, bool) {
	rows := a.rows()
	if len(rows) == 0 {
		return Row{}, false
	}
	return rows[clamp(a.selection[ViewContainers], len(rows))], true
}

// toggleGrouping switches the headings on and off, keeping the cursor on
// whatever container it was on.
func (a *App) toggleGrouping() {
	a.keepingSelection(func() { a.grouped = !a.grouped })
	a.remember(func(cfg *config.Config) { cfg.GroupContainers = a.grouped })
	a.setStatus("grouping %s", map[bool]string{true: "on", false: "off"}[a.grouped])
}

// toggleCollapse folds the selected group away, or opens it again.
//
// Pressing it on a container folds the group that container is in, which is
// what someone reading a long list of one application's containers wants —
// having to move the cursor up to the header first would be a step for
// nothing.
func (a *App) toggleCollapse() {
	if !a.grouped {
		return
	}
	row, ok := a.selectedRow()
	if !ok {
		return
	}
	name := a.groupNameOf(row)
	if name == "" {
		return
	}

	if a.collapsed[name] {
		delete(a.collapsed, name)
	} else {
		a.collapsed[name] = true
		// The rows the cursor was on are about to disappear, so put it on
		// the header that swallowed them.
		a.selectHeader(name)
	}
	a.remember(func(cfg *config.Config) { cfg.CollapsedGroups = a.collapsedNames() })
}

// groupNameOf returns the application a row belongs to.
func (a *App) groupNameOf(row Row) string {
	if row.Header != nil {
		return row.Header.Name
	}
	for _, found := range group.Build(a.filteredContainers()) {
		for _, container := range found.Containers {
			if container.ID == row.Container.ID {
				return found.Name
			}
		}
	}
	return ""
}

// selectHeader moves the cursor onto a group's heading.
func (a *App) selectHeader(name string) {
	for index, row := range a.rows() {
		if row.Header != nil && row.Header.Name == name {
			a.selection[ViewContainers] = index
			return
		}
	}
}

// keepingSelection runs a change that reshuffles the rows, leaving the cursor
// on the container it was on.
//
// The selection is an index into a list whose every entry moves when headings
// appear or disappear, so keeping the number would keep the cursor pointing at
// something else.
func (a *App) keepingSelection(change func()) {
	var was string
	if row, ok := a.selectedRow(); ok && row.Header == nil {
		was = row.Container.ID
	}

	change()

	if was == "" {
		return
	}
	for index, row := range a.rows() {
		if row.Header == nil && row.Container.ID == was {
			a.selection[ViewContainers] = index
			return
		}
	}
}

// collapsedNames lists the folded groups, in a stable order so the stored
// preference does not churn between writes.
func (a *App) collapsedNames() []string {
	names := make([]string, 0, len(a.collapsed))
	for name := range a.collapsed {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// collapsedSet turns the stored list of folded groups into the form the view
// asks questions of.
func collapsedSet(names []string) map[string]bool {
	set := make(map[string]bool, len(names))
	for _, name := range names {
		set[name] = true
	}
	return set
}

// placeSelection puts the cursor on the first container the first time there
// is a list to put it on.
//
// The selection is an index, and with grouping on index zero is a heading —
// so a freshly started muelle would highlight a row where none of the
// container keys do anything. Done once: after that the cursor is the user's.
func (a *App) placeSelection() {
	if a.selectionPlaced {
		return
	}
	a.selectionPlaced = true
	if a.grouped {
		a.selectFirstContainer()
	}
}

// applyFilter narrows the current list and puts the cursor somewhere useful.
//
// Filtering is how someone finds the container they mean to act on, so leaving
// the cursor on the heading above it — where every container key does nothing
// — would defeat the search they just made.
func (a *App) applyFilter(filter string) {
	a.filter = filter
	a.selection[a.view] = 0
	a.offset[a.view] = 0
	if a.view == ViewContainers {
		a.selectFirstContainer()
	}
}

// selectFirstContainer moves the cursor past any leading heading.
func (a *App) selectFirstContainer() {
	for index, row := range a.rows() {
		if row.Header == nil {
			a.selection[ViewContainers] = index
			return
		}
	}
}
