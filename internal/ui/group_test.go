package ui

import (
	"strings"
	"testing"

	"github.com/RchrdHndrcks/muelle/internal/config"
	"github.com/RchrdHndrcks/muelle/internal/docker"
)

// Grouping turns the list into headers with their containers under them.
func TestRowsInterleaveHeaders(t *testing.T) {
	app := groupedApp(t)

	rows := app.rows()

	if len(rows) != 5 {
		t.Fatalf("got %d rows, want two headers and three containers: %v", len(rows), rowNames(rows))
	}
	if rows[0].Header == nil || rows[0].Header.Name != "shop" {
		t.Errorf("got %v, want the shop header first", rowNames(rows))
	}
	if rows[1].Header != nil || rows[1].Container.Name() != "shop-db" {
		t.Errorf("got %v, want shop's containers under it", rowNames(rows))
	}
}

// With grouping off the view is exactly what it was before this feature.
func TestRowsAreFlatWhenGroupingIsOff(t *testing.T) {
	app := groupedApp(t)
	app.grouped = false

	rows := app.rows()

	for _, row := range rows {
		if row.Header != nil {
			t.Fatalf("got %v, want no headers", rowNames(rows))
		}
	}
	if len(rows) != 3 {
		t.Errorf("got %d rows, want one per container", len(rows))
	}
}

// Collapsing is what makes a bucket of thirty stray containers bearable.
func TestCollapsingHidesTheMembersButNotTheHeader(t *testing.T) {
	app := groupedApp(t)
	app.collapsed["shop"] = true

	rows := app.rows()

	names := rowNames(rows)
	if len(rows) != 3 {
		t.Fatalf("got %v, want shop's containers hidden", names)
	}
	if names[0] != "shop" {
		t.Errorf("got %v, want the header still there to expand", names)
	}
}

// The header carries what the application costs, which is the reading the
// grouping exists to make possible.
func TestTheHeaderSumsItsContainers(t *testing.T) {
	app := groupedApp(t)
	app.stats = map[string]docker.Stat{
		"shop-db":         {CPUPercent: 0.5, MemUsage: 300},
		"shop-db-replica": {CPUPercent: 0.7, MemUsage: 700},
	}

	header := app.rows()[0].Header

	if header.CPUPercent != 1.2 {
		t.Errorf("got %v, want the group's CPU", header.CPUPercent)
	}
	if header.MemUsage != 1000 {
		t.Errorf("got %v, want the group's memory", header.MemUsage)
	}
	if header.Running != 2 || header.Total != 2 {
		t.Errorf("got %d/%d, want both counted", header.Running, header.Total)
	}
}

// Every container key already copes with there being no selected container,
// so a header selecting as "no container" makes them all no-ops for free —
// and stops a restart landing on six containers because the cursor was one
// line high.
func TestAHeaderIsNotASelectedContainer(t *testing.T) {
	app := groupedApp(t)
	app.selection[ViewContainers] = 0

	if _, ok := app.selectedContainer(); ok {
		t.Error("a header must not answer as a container")
	}

	app.selection[ViewContainers] = 1
	container, ok := app.selectedContainer()
	if !ok || container.Name() != "shop-db" {
		t.Errorf("got %v (ok=%v), want the container under the header", container.Name(), ok)
	}
}

// Filtering to something inside a collapsed group must show it. Otherwise the
// filter looks like it found nothing.
func TestFilteringExpandsACollapsedGroup(t *testing.T) {
	app := groupedApp(t)
	app.collapsed["shop"] = true
	app.filter = "db-replica"

	names := rowNames(app.rows())

	if len(names) != 2 || names[0] != "shop" || names[1] != "shop-db-replica" {
		t.Errorf("got %v, want the match shown under its header", names)
	}
}

// A group with nothing matching should not leave an empty header behind.
func TestAGroupWithNoMatchesDisappears(t *testing.T) {
	app := groupedApp(t)
	app.filter = "shop"

	for _, name := range rowNames(app.rows()) {
		if name == "web" {
			t.Errorf("got %v, want the unmatched group gone", rowNames(app.rows()))
		}
	}
}

// Toggling moves every row, so an index kept across it points at something
// else. The cursor should stay on the container the user was looking at.
func TestTheSelectionFollowsTheContainerAcrossAToggle(t *testing.T) {
	app := groupedApp(t)
	// The header, then shop-db, then shop-db-replica.
	app.selection[ViewContainers] = 2

	app.toggleGrouping()

	container, ok := app.selectedContainer()
	if !ok || container.Name() != "shop-db-replica" {
		t.Errorf("got %q (ok=%v), want the cursor to have followed the container", container.Name(), ok)
	}
}

// Collapsing the group the cursor is inside has to put it somewhere sensible.
func TestCollapsingTheSelectedGroupSelectsItsHeader(t *testing.T) {
	app := groupedApp(t)
	app.selection[ViewContainers] = 1

	app.toggleCollapse()

	row := app.rows()[app.selection[ViewContainers]]
	if row.Header == nil || row.Header.Name != "shop" {
		t.Errorf("got %v, want the cursor on the header it collapsed", rowNames(app.rows()))
	}
}

// groupedApp builds an app with grouping on and three containers in two
// applications.
func groupedApp(t *testing.T) *App {
	t.Helper()
	app := newTestApp(t)
	app.grouped = true
	app.collapsed = map[string]bool{}
	app.containers = []docker.Container{
		{ID: "shop-db", Names: []string{"/shop-db"}, State: "running", Status: "Up 2 hours"},
		{ID: "shop-db-replica", Names: []string{"/shop-db-replica"}, State: "running", Status: "Up 2 hours"},
		{ID: "web-db", Names: []string{"/web-db"}, State: "running", Status: "Up 2 hours",
			Labels: map[string]string{docker.LabelProject: "web", docker.LabelService: "db"}},
	}
	return app
}

func rowNames(rows []Row) []string {
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		if row.Header != nil {
			out = append(out, row.Header.Name)
			continue
		}
		out = append(out, row.Container.Name())
	}
	return out
}

// The grouping toggle is a preference like the ordering, and having to set it
// again on every launch is the same annoyance.
func TestGroupingIsRemembered(t *testing.T) {
	app := groupedApp(t)
	var stored config.Config
	app.SetPreferenceWriter(func(change func(*config.Config)) error {
		change(&stored)
		return nil
	})

	app.toggleGrouping()

	if stored.GroupContainers {
		t.Error("turning grouping off should have been recorded")
	}
}

// Which groups are folded away is worth remembering too: the bucket of stray
// containers is collapsed once and should stay that way.
func TestCollapsedGroupsAreRemembered(t *testing.T) {
	app := groupedApp(t)
	var stored config.Config
	app.SetPreferenceWriter(func(change func(*config.Config)) error {
		change(&stored)
		return nil
	})
	app.selection[ViewContainers] = 0

	app.toggleCollapse()

	if len(stored.CollapsedGroups) != 1 || stored.CollapsedGroups[0] != "shop" {
		t.Errorf("got %v, want shop recorded as collapsed", stored.CollapsedGroups)
	}
}

// Without an indent the headings and their containers sit in the same column,
// which reads as a flat list with odd extra rows in it rather than as a
// hierarchy.
func TestContainersAreIndentedUnderTheirHeading(t *testing.T) {
	app := groupedApp(t)

	name := app.containerCells(app.rows()[1])[0]

	if !strings.HasPrefix(name, "  ") {
		t.Errorf("got %q, want it indented under its heading", name)
	}
}

// Ungrouped, there is nothing to be indented under.
func TestContainersAreNotIndentedWithoutGrouping(t *testing.T) {
	app := groupedApp(t)
	app.grouped = false

	name := app.containerCells(app.rows()[1])[0]

	if strings.HasPrefix(name, " ") {
		t.Errorf("got %q, want the name flush left", name)
	}
}

// Filtering is how you find the container you want to act on. Landing the
// cursor on the heading above it means the keys you reached for do nothing.
func TestFilteringLeavesAContainerSelected(t *testing.T) {
	app := groupedApp(t)
	app.applyFilter("db-replica")

	container, ok := app.selectedContainer()

	if !ok {
		t.Fatalf("got no container selected, rows are %v", rowNames(app.rows()))
	}
	if container.Name() != "shop-db-replica" {
		t.Errorf("got %q, want the container that was searched for", container.Name())
	}
}

// G is already "go to the end" in every list, and the list keys are answered
// first, so binding grouping to it would have made the toggle dead code.
func TestTheGroupingKeyDoesNotCollideWithEnd(t *testing.T) {
	app := groupedApp(t)
	app.grouped = false

	press(app, runeKey('A'))
	if !app.grouped {
		t.Error("A should turn grouping on")
	}

	press(app, runeKey('G'))
	if app.selected() != len(app.rows())-1 {
		t.Errorf("got %d, want G to still jump to the last row", app.selected())
	}
	if !app.grouped {
		t.Error("G must not have toggled grouping back off")
	}
}

// The selection indexes rows, so its bounds have to be the rows: clamping
// against the container count lands the cursor on a heading when the list
// shrinks, which is where no container key works.
func TestSelectionIsBoundedByRowsNotContainers(t *testing.T) {
	app := groupedApp(t)

	if got, want := app.currentLength(), len(app.rows()); got != want {
		t.Errorf("got %d, want %d rows", got, want)
	}
}

// Grouped, the heading carries the application's name and the rows carry what
// distinguishes them.
func TestGroupedRowsDropTheApplicationName(t *testing.T) {
	app := groupedApp(t)

	names := []string{}
	for _, row := range app.rows() {
		if row.Header == nil {
			names = append(names, strings.TrimSpace(app.containerCells(row)[0]))
		}
	}

	want := []string{"db", "db-replica", "db"}
	if len(names) != len(want) {
		t.Fatalf("got %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("got %v, want %v", names, want)
		}
	}
}

// Ungrouped, there is no heading saying the rest, so the full name is all the
// row has.
func TestFlatRowsKeepTheWholeName(t *testing.T) {
	app := groupedApp(t)
	app.grouped = false

	name := app.containerCells(app.rows()[0])[0]

	if name != "shop-db" {
		t.Errorf("got %q, want the whole name", name)
	}
}

// A container that is not named after its application keeps its name: there is
// nothing of the heading in it to remove.
func TestARowNotNamedAfterItsApplicationIsUntouched(t *testing.T) {
	app := groupedApp(t)
	app.containers = append(app.containers, docker.Container{
		ID: "odd", Names: []string{"/legacy-cart"}, State: "running", Status: "Up 2 hours",
		Labels: map[string]string{docker.LabelProject: "shop", docker.LabelService: "cart"},
	})

	var found bool
	for _, row := range app.rows() {
		if row.Header == nil && row.Container.ID == "odd" {
			found = strings.TrimSpace(app.containerCells(row)[0]) == "legacy-cart"
		}
	}
	if !found {
		t.Error("a container not named after its application should keep its name")
	}
}
