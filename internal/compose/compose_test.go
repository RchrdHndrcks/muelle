package compose

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RchrdHndrcks/muelle/internal/docker"
)

// container builds a container carrying the Compose labels the daemon would
// report for it.
func container(name, project, service, state string) docker.Container {
	labels := map[string]string{}
	if project != "" {
		labels[docker.LabelProject] = project
		labels[docker.LabelService] = service
		labels[docker.LabelWorkingDir] = "/srv/" + project
		labels[docker.LabelConfigFiles] = "/srv/" + project + "/compose.yaml"
	}
	return docker.Container{
		ID:     name + "-id",
		Names:  []string{"/" + name},
		State:  state,
		Labels: labels,
	}
}

func TestFromContainersGroupsByProject(t *testing.T) {
	containers := []docker.Container{
		container("shop-api", "shop", "api", "running"),
		container("blog-db", "blog", "db", "running"),
		container("shop-db", "shop", "db", "running"),
	}

	projects := FromContainers(containers)

	if len(projects) != 2 {
		t.Fatalf("got %d projects, want 2", len(projects))
	}
	if projects[0].Name != "blog" || projects[1].Name != "shop" {
		t.Errorf("got %q and %q, want them sorted by name", projects[0].Name, projects[1].Name)
	}
	if len(projects[1].Containers) != 2 {
		t.Errorf("got %d containers in shop, want 2", len(projects[1].Containers))
	}
}

// Standalone containers belong in the container view, not the project view.
func TestFromContainersIgnoresUnlabelledContainers(t *testing.T) {
	containers := []docker.Container{
		container("standalone", "", "", "running"),
		container("shop-api", "shop", "api", "running"),
	}

	projects := FromContainers(containers)

	if len(projects) != 1 || projects[0].Name != "shop" {
		t.Errorf("got %+v, want only the labelled project", projects)
	}
}

func TestFromContainersReadsPathsFromLabels(t *testing.T) {
	projects := FromContainers([]docker.Container{container("shop-api", "shop", "api", "running")})

	if got := projects[0].WorkingDir; got != "/srv/shop" {
		t.Errorf("got working dir %q, want it from the label", got)
	}
	if got := projects[0].ConfigFiles; len(got) != 1 || got[0] != "/srv/shop/compose.yaml" {
		t.Errorf("got config files %v, want the labelled path", got)
	}
}

// Compose writes multiple config files as one comma-separated label value.
func TestFromContainersSplitsMultipleConfigFiles(t *testing.T) {
	c := container("shop-api", "shop", "api", "running")
	c.Labels[docker.LabelConfigFiles] = "/srv/shop/compose.yaml,/srv/shop/compose.prod.yaml"

	projects := FromContainers([]docker.Container{c})

	if got := projects[0].ConfigFiles; len(got) != 2 {
		t.Fatalf("got %v, want both files", got)
	}
}

func TestProjectStatusReflectsContainerStates(t *testing.T) {
	cases := []struct {
		name       string
		containers []docker.Container
		want       string
	}{
		{"all up", []docker.Container{
			container("a", "p", "a", "running"),
			container("b", "p", "b", "running"),
		}, "running"},
		{"all down", []docker.Container{
			container("a", "p", "a", "exited"),
		}, "exited"},
		{"some up", []docker.Container{
			container("a", "p", "a", "running"),
			container("b", "p", "b", "exited"),
		}, "partial"},
		{"no containers", nil, "stopped"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			project := Project{Containers: tc.containers}
			if got := project.Status(); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestProjectServicesDeduplicatesReplicas(t *testing.T) {
	project := Project{Containers: []docker.Container{
		container("web-1", "p", "web", "running"),
		container("web-2", "p", "web", "running"),
		container("db-1", "p", "db", "running"),
	}}

	services := project.Services()

	if len(services) != 2 || services[0] != "db" || services[1] != "web" {
		t.Errorf("got %v, want [db web]", services)
	}
}

// writeProject creates a directory holding a compose file.
func writeProject(t *testing.T, dir, filename string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, filename), []byte("services: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A stopped stack has no containers, so only a disk scan can find it.
func TestDiscoverFindsProjectsOneLevelDeep(t *testing.T) {
	root := t.TempDir()
	writeProject(t, filepath.Join(root, "shop"), "compose.yaml")
	writeProject(t, filepath.Join(root, "blog"), "docker-compose.yml")

	projects := Discover([]string{root})

	if len(projects) != 2 {
		t.Fatalf("got %d projects, want 2: %+v", len(projects), projects)
	}
	if projects[0].Name != "blog" || !projects[0].OnDisk {
		t.Errorf("got %+v, want blog marked as found on disk", projects[0])
	}
}

func TestDiscoverFindsProjectInRootItself(t *testing.T) {
	root := t.TempDir()
	writeProject(t, root, "compose.yaml")

	projects := Discover([]string{root})

	if len(projects) != 1 {
		t.Fatalf("got %d projects, want the root itself to count", len(projects))
	}
}

func TestDiscoverIgnoresDirectoriesWithoutComposeFiles(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "not-a-project"), 0o755); err != nil {
		t.Fatal(err)
	}

	if projects := Discover([]string{root}); len(projects) != 0 {
		t.Errorf("got %+v, want none", projects)
	}
}

func TestDiscoverSkipsMissingRoots(t *testing.T) {
	if projects := Discover([]string{"/nonexistent-path-for-test"}); len(projects) != 0 {
		t.Errorf("got %+v, want none rather than an error", projects)
	}
}

// A project both running and on disk must appear once, described by its
// labels.
func TestMergePrefersLabelDerivedProjects(t *testing.T) {
	fromContainers := []Project{{
		Name:        "shop",
		WorkingDir:  "/srv/shop",
		ConfigFiles: []string{"/srv/shop/compose.yaml"},
		Containers:  []docker.Container{container("shop-api", "shop", "api", "running")},
	}}
	fromDisk := []Project{
		{Name: "shop", WorkingDir: "/elsewhere/shop", ConfigFiles: []string{"/elsewhere/shop/compose.yaml"}, OnDisk: true},
		{Name: "blog", WorkingDir: "/srv/blog", ConfigFiles: []string{"/srv/blog/compose.yaml"}, OnDisk: true},
	}

	merged := Merge(fromContainers, fromDisk)

	if len(merged) != 2 {
		t.Fatalf("got %d projects, want the duplicate collapsed: %+v", len(merged), merged)
	}
	shop := merged[1]
	if shop.Name != "shop" {
		t.Fatalf("got %q at index 1, want shop", shop.Name)
	}
	if shop.WorkingDir != "/srv/shop" {
		t.Errorf("got %q, want the label's path to win over the disk scan", shop.WorkingDir)
	}
	if len(shop.Containers) != 1 {
		t.Error("merging should not drop the project's containers")
	}
}

// Containers created by an older Compose can lack the path labels; the disk
// scan is then the only source for them.
func TestMergeFillsMissingPathsFromDisk(t *testing.T) {
	fromContainers := []Project{{Name: "shop"}}
	fromDisk := []Project{{Name: "shop", WorkingDir: "/srv/shop", ConfigFiles: []string{"/srv/shop/compose.yaml"}, OnDisk: true}}

	merged := Merge(fromContainers, fromDisk)

	if merged[0].WorkingDir != "/srv/shop" {
		t.Errorf("got %q, want the disk path used when the label is absent", merged[0].WorkingDir)
	}
	if len(merged[0].ConfigFiles) != 1 {
		t.Error("config files should be filled in from disk too")
	}
}

func TestExpandPathResolvesTilde(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	got, err := ExpandPath("~/stacks")
	if err != nil {
		t.Fatalf("ExpandPath: %v", err)
	}
	if got != filepath.Join(home, "stacks") {
		t.Errorf("got %q, want it under the home directory", got)
	}
}

func TestCommandIdentifiesProjectExplicitly(t *testing.T) {
	project := Project{
		Name:        "shop",
		WorkingDir:  "/srv/shop",
		ConfigFiles: []string{"/srv/shop/compose.yaml"},
	}

	argv := Command(project, ActionUp)

	want := "docker compose -f /srv/shop/compose.yaml --project-directory /srv/shop -p shop up -d"
	if got := strings.Join(argv, " "); got != want {
		t.Errorf("got %q\nwant %q", got, want)
	}
}

func TestCommandPassesEveryConfigFile(t *testing.T) {
	project := Project{
		Name:        "shop",
		ConfigFiles: []string{"/srv/shop/compose.yaml", "/srv/shop/compose.prod.yaml"},
	}

	argv := Command(project, ActionRestart)

	if got := strings.Count(strings.Join(argv, " "), "-f "); got != 2 {
		t.Errorf("got %d -f flags in %v, want one per config file", got, argv)
	}
}

// "up" must detach, or the TUI would sit attached to the log stream.
func TestCommandUpIsDetached(t *testing.T) {
	argv := Command(Project{Name: "shop"}, ActionUp)

	if argv[len(argv)-1] != "-d" {
		t.Errorf("got %v, want it to end with -d", argv)
	}
}

func TestCommandLogsFollowsWithBoundedTail(t *testing.T) {
	argv := Command(Project{Name: "shop"}, ActionLogs)

	joined := strings.Join(argv, " ")
	if !strings.Contains(joined, "logs -f --tail 200") {
		t.Errorf("got %q, want a followed, bounded log stream", joined)
	}
}

func TestCommandOmitsAbsentPaths(t *testing.T) {
	argv := Command(Project{Name: "shop"}, ActionPS)

	if got := strings.Join(argv, " "); got != "docker compose -p shop ps" {
		t.Errorf("got %q, want no empty flags", got)
	}
}

func TestActionsOfferOnlyApplicableOperations(t *testing.T) {
	stopped := Actions(Project{Name: "shop"})
	for _, action := range stopped {
		if action == ActionStop || action == ActionRestart {
			t.Errorf("got %q offered for a stopped project", action)
		}
	}
	if stopped[0] != ActionUp {
		t.Errorf("got %q first, want up to lead for a stopped project", stopped[0])
	}

	running := Actions(Project{
		Name:       "shop",
		Containers: []docker.Container{container("shop-api", "shop", "api", "running")},
	})
	if running[0] != ActionRestart {
		t.Errorf("got %q first, want restart to lead for a running project", running[0])
	}
}

func TestDownIsTheOnlyDestructiveAction(t *testing.T) {
	if !ActionDown.Destructive() {
		t.Error("down removes containers and must be confirmed")
	}
	for _, action := range []Action{ActionUp, ActionRestart, ActionStop, ActionPull, ActionPS} {
		if action.Destructive() {
			t.Errorf("%q should not require confirmation", action)
		}
	}
}
