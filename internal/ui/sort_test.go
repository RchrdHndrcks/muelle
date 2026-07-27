package ui

import (
	"bytes"
	"strings"
	"testing"

	"github.com/RchrdHndrcks/muelle/internal/config"
	"github.com/RchrdHndrcks/muelle/internal/docker"
	"github.com/RchrdHndrcks/muelle/internal/tui"
)

// sortable builds a container with the fields the orderings read.
func sortable(name, project, state string, created int64) docker.Container {
	labels := map[string]string{}
	if project != "" {
		labels[docker.LabelProject] = project
	}
	return docker.Container{
		ID:      name + "-id",
		Names:   []string{"/" + name},
		State:   state,
		Created: created,
		Labels:  labels,
	}
}

func names(containers []docker.Container) string {
	var out []string
	for _, c := range containers {
		out = append(out, c.Name())
	}
	return strings.Join(out, ",")
}

func TestSortByCPUPutsHeaviestFirst(t *testing.T) {
	containers := []docker.Container{
		sortable("idle", "", "running", 1),
		sortable("busy", "", "running", 2),
		sortable("medium", "", "running", 3),
	}
	stats := map[string]docker.Stat{
		"idle-id":   {CPUPercent: 0.1},
		"busy-id":   {CPUPercent: 92.5},
		"medium-id": {CPUPercent: 12.0},
	}

	SortContainers(containers, SortCPU, stats)

	if got := names(containers); got != "busy,medium,idle" {
		t.Errorf("got %q, want the heaviest consumer first", got)
	}
}

func TestSortByMemoryPutsLargestFirst(t *testing.T) {
	containers := []docker.Container{
		sortable("small", "", "running", 1),
		sortable("large", "", "running", 2),
	}
	stats := map[string]docker.Stat{
		"small-id": {MemUsage: 1 << 20},
		"large-id": {MemUsage: 1 << 30},
	}

	SortContainers(containers, SortMemory, stats)

	if got := names(containers); got != "large,small" {
		t.Errorf("got %q, want the largest first", got)
	}
}

// Stats arrive asynchronously. A container without a sample yet must not be
// treated as idle and float to the bottom as though it were measured.
func TestSortByResourceRanksUnmeasuredContainersLast(t *testing.T) {
	containers := []docker.Container{
		sortable("unmeasured", "", "running", 1),
		sortable("idle", "", "running", 2),
	}
	stats := map[string]docker.Stat{"idle-id": {CPUPercent: 0}}

	SortContainers(containers, SortCPU, stats)

	if got := names(containers); got != "idle,unmeasured" {
		t.Errorf("got %q, want the measured container ranked above the unmeasured one", got)
	}
}

func TestSortByAgePutsNewestFirst(t *testing.T) {
	containers := []docker.Container{
		sortable("old", "", "running", 1000),
		sortable("new", "", "running", 9000),
	}

	SortContainers(containers, SortAge, nil)

	if got := names(containers); got != "new,old" {
		t.Errorf("got %q, want the newest first", got)
	}
}

// Broken things first: the point of sorting by state is to surface what needs
// attention.
func TestSortByStateSurfacesProblemsFirst(t *testing.T) {
	containers := []docker.Container{
		sortable("fine", "", "running", 1),
		sortable("gone", "", "exited", 2),
		sortable("flapping", "", "restarting", 3),
		sortable("dead", "", "dead", 4),
	}

	SortContainers(containers, SortState, nil)

	if got := names(containers); got != "dead,flapping,gone,fine" {
		t.Errorf("got %q, want the most alarming states first", got)
	}
}

func TestSortDefaultGroupsByProject(t *testing.T) {
	containers := []docker.Container{
		sortable("loose", "", "running", 1),
		sortable("shop-web", "shop", "running", 2),
		sortable("blog-db", "blog", "running", 3),
		sortable("shop-api", "shop", "running", 4),
	}

	SortContainers(containers, SortDefault, nil)

	if got := names(containers); got != "blog-db,shop-api,shop-web,loose" {
		t.Errorf("got %q, want projects grouped and standalone containers last", got)
	}
}

// Equal rows must land somewhere deterministic, or the list shuffles on every
// refresh.
func TestSortBreaksTiesByName(t *testing.T) {
	containers := []docker.Container{
		sortable("zulu", "", "running", 5),
		sortable("alpha", "", "running", 5),
		sortable("mike", "", "running", 5),
	}

	SortContainers(containers, SortAge, nil)

	if got := names(containers); got != "alpha,mike,zulu" {
		t.Errorf("got %q, want equal rows ordered by name", got)
	}
}

func TestSortIsStableAcrossRepeatedCalls(t *testing.T) {
	build := func() []docker.Container {
		return []docker.Container{
			sortable("a", "", "running", 1),
			sortable("b", "", "running", 1),
			sortable("c", "", "running", 1),
		}
	}
	stats := map[string]docker.Stat{
		"a-id": {CPUPercent: 5},
		"b-id": {CPUPercent: 5},
		"c-id": {CPUPercent: 5},
	}

	first, second := build(), build()
	SortContainers(first, SortCPU, stats)
	SortContainers(second, SortCPU, stats)

	if names(first) != names(second) {
		t.Errorf("got %q then %q, want a deterministic order", names(first), names(second))
	}
}

func TestSortKeyCycleVisitsEveryOrderingAndWraps(t *testing.T) {
	seen := map[SortKey]bool{}
	key := SortDefault

	for range int(sortKeyCount) {
		if seen[key] {
			t.Fatalf("ordering %v repeated before the cycle completed", key.Label())
		}
		seen[key] = true
		key = key.Next()
	}

	if key != SortDefault {
		t.Errorf("got %v after a full cycle, want it back at the default", key.Label())
	}
}

func TestSortKeysHaveLabels(t *testing.T) {
	for key := SortDefault; key < sortKeyCount; key++ {
		if label := key.Label(); label == "" || label == "?" {
			t.Errorf("ordering %d has no label", key)
		}
	}
}

func TestResourceOrderingsAreDescending(t *testing.T) {
	if !SortCPU.Descending() || !SortMemory.Descending() {
		t.Error("resource orderings should put the largest consumer first")
	}
	if SortDefault.Descending() {
		t.Error("the name ordering should be ascending")
	}
}

// Sorting must not reorder the model behind the view.
func TestFilteredContainersDoesNotMutateTheModel(t *testing.T) {
	app := newTestApp(t)
	app.containers = []docker.Container{
		sortable("aaa", "", "running", 1),
		sortable("zzz", "", "running", 9),
	}
	app.sortKey = SortAge

	_ = app.filteredContainers()

	if app.containers[0].Name() != "aaa" {
		t.Errorf("got %q first, want the underlying slice untouched", app.containers[0].Name())
	}
}

func TestFilteredContainersAppliesSortAfterFilter(t *testing.T) {
	app := newTestApp(t)
	app.containers = []docker.Container{
		sortable("web-old", "", "running", 1),
		sortable("db-new", "", "running", 9),
		sortable("web-new", "", "running", 5),
	}
	app.filter = "web"
	app.sortKey = SortAge

	got := names(app.filteredContainers())

	if got != "web-new,web-old" {
		t.Errorf("got %q, want only web containers, newest first", got)
	}
}

// Reordering must move the cursor with the container it was on. Leaving it at
// the same index would silently re-aim the next keystroke at a different
// container — and some of those keystrokes remove things.
func TestCursorFollowsContainerAcrossReorder(t *testing.T) {
	app := newTestApp(t)
	app.containers = []docker.Container{
		sortable("first", "", "running", 100),
		sortable("second", "", "running", 200),
		sortable("third", "", "running", 300),
	}
	app.stats = map[string]docker.Stat{
		"first-id":  {CPUPercent: 1},
		"second-id": {CPUPercent: 99},
		"third-id":  {CPUPercent: 50},
	}
	app.selection[ViewContainers] = 0 // "first"

	before, _ := app.selectedContainer()
	press(app, runeKey('o')) // default -> cpu
	after, ok := app.selectedContainer()

	if !ok {
		t.Fatal("expected a selection after reordering")
	}
	if after.ID != before.ID {
		t.Errorf("cursor moved from %q to %q; it should follow the container",
			before.Name(), after.Name())
	}
	if app.sortKey != SortCPU {
		t.Errorf("got sort %v, want it advanced to cpu", app.sortKey.Label())
	}
}

func TestSortKeyOnlyCyclesOnTheContainerView(t *testing.T) {
	app := loadedApp(t)
	app.SetView(ViewImages)

	press(app, runeKey('o'))

	if app.sortKey != SortDefault {
		t.Errorf("got %v, want the images view to leave sorting alone", app.sortKey.Label())
	}
}

// The names in the configuration file are the ones the interface uses, so what
// is written there is what the application calls it.
func TestParseSortKeyRoundTripsEveryOrdering(t *testing.T) {
	for key := SortDefault; key < sortKeyCount; key++ {
		parsed, ok := ParseSortKey(key.Label())
		if !ok {
			t.Errorf("%q is shown in the interface but not recognised back", key.Label())
			continue
		}
		if parsed != key {
			t.Errorf("%q parsed as %q", key.Label(), parsed.Label())
		}
	}
}

// A mistyped setting should not stop the application starting.
func TestParseSortKeyRejectsUnknownNames(t *testing.T) {
	for _, name := range []string{"", "cpus", "PROJECT", "nonsense"} {
		if key, ok := ParseSortKey(name); ok {
			t.Errorf("ParseSortKey(%q) accepted it as %v", name, key.Label())
		}
	}
	if got := parseConfiguredSort("nonsense"); got != SortDefault {
		t.Errorf("got %v, want a fallback to the default", got.Label())
	}
}

func TestConfiguredSortIsAppliedAtStartup(t *testing.T) {
	cfg := config.Default()
	cfg.Sort = "memory"
	cfg.ComposeDirs = nil
	app := New(cfg, fakeDaemon(t), tui.NewScreen(&bytes.Buffer{}, 120, 30, false), nil)

	if app.sortKey != SortMemory {
		t.Errorf("got %v, want the stored ordering applied", app.sortKey.Label())
	}
}

// The ordering someone chose should be what they find next time, not a setting
// to reapply on every launch.
func TestChangingTheOrderRecordsIt(t *testing.T) {
	app := loadedApp(t)
	var saved config.Config
	app.SetPreferenceWriter(func(change func(*config.Config)) { change(&saved) })

	press(app, runeKey('o')) // default -> cpu

	if saved.Sort != "cpu" {
		t.Errorf("got %q, want the new ordering recorded", saved.Sort)
	}
	if saved.Sort != app.sortKey.Label() {
		t.Errorf("recorded %q while showing %q", saved.Sort, app.sortKey.Label())
	}
}

// Nothing should be written where there is nowhere to write, as in dump mode.
func TestChangingTheOrderWithoutAWriterIsHarmless(t *testing.T) {
	app := loadedApp(t)
	app.SetPreferenceWriter(nil)

	press(app, runeKey('o'))

	if app.sortKey == SortDefault {
		t.Error("the ordering should still change in the session")
	}
}
