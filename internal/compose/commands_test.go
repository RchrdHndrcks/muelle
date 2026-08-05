package compose

import (
	"slices"
	"strings"
	"testing"

	"github.com/RchrdHndrcks/muelle/internal/docker"
)

func TestCommandBuildsArgvForEachBinary(t *testing.T) {
	project := Project{
		Name:        "engi",
		WorkingDir:  "/srv/engi",
		ConfigFiles: []string{"/srv/engi/docker-compose.yml"},
	}

	cases := map[string]struct {
		binary Binary
		want   []string
	}{
		"plugin": {
			binary: Binary{"docker", "compose"},
			want: []string{
				"docker", "compose",
				"-f", "/srv/engi/docker-compose.yml",
				"--project-directory", "/srv/engi",
				"-p", "engi",
				"up", "-d",
			},
		},
		"standalone": {
			binary: Binary{"docker-compose"},
			want: []string{
				"docker-compose",
				"-f", "/srv/engi/docker-compose.yml",
				"--project-directory", "/srv/engi",
				"-p", "engi",
				"up", "-d",
			},
		},
	}

	for name, c := range cases {
		got := c.binary.Command(project, ActionUp)
		if !slices.Equal(got, c.want) {
			t.Errorf("%s: got %v, want %v", name, got, c.want)
		}
	}
}

// Command must not write through to the Binary's backing array, or the second
// project acted on would inherit the first one's flags.
func TestCommandDoesNotMutateTheBinary(t *testing.T) {
	binary := Binary{"docker", "compose"}

	binary.Command(Project{Name: "one"}, ActionUp)
	got := binary.Command(Project{Name: "two"}, ActionDown)

	if !slices.Equal(binary, Binary{"docker", "compose"}) {
		t.Errorf("binary became %v, want it unchanged", binary)
	}
	if !slices.Equal(got, []string{"docker", "compose", "-p", "two", "down"}) {
		t.Errorf("got %v, want the second project's own argv", got)
	}
}

// The plugin wins when both are installed: it is the form Docker ships and
// keeps current, and a stale standalone binary alongside it is common.
func TestDetectPrefersThePluginOverTheStandaloneBinary(t *testing.T) {
	cases := map[string]struct {
		plugin, standalone bool
		want               Binary
	}{
		"both installed":    {true, true, Binary{"docker", "compose"}},
		"plugin only":       {true, false, Binary{"docker", "compose"}},
		"standalone only":   {false, true, Binary{"docker-compose"}},
		"neither installed": {false, false, nil},
	}

	for name, c := range cases {
		restore := stubProbes(c.plugin, c.standalone)
		got := Detect()
		restore()

		if !slices.Equal(got, c.want) {
			t.Errorf("%s: got %v, want %v", name, got, c.want)
		}
	}
}

// A machine with neither form of Compose must be reported as such rather than
// producing an argv that fails only once the user has picked an action.
func TestAvailableReportsWhetherComposeCanRun(t *testing.T) {
	if (Binary(nil)).Available() {
		t.Error("a nil binary must not report itself available")
	}
	if !(Binary{"docker-compose"}).Available() {
		t.Error("a detected binary must report itself available")
	}
}

// The menu shows the exact command each entry would run, so it has to name the
// binary actually in use — telling someone without the plugin that muelle will
// run "docker compose" is a lie they cannot act on.
func TestStringNamesTheDetectedBinary(t *testing.T) {
	detail := strings.Join(Binary{"docker-compose"}.Command(Project{Name: "x"}, ActionPS), " ")
	if !strings.HasPrefix(detail, "docker-compose ") {
		t.Errorf("got %q, want it to start with the standalone binary", detail)
	}
}

// Recreating is an "up" that refuses to reuse the existing container. A
// restart cannot apply a configuration change — the container's environment
// and image are fixed when it is created — so this is the only action that
// makes an edited compose file take effect.
func TestCommandBuildsRecreateAsAForcedUp(t *testing.T) {
	got := pluginBinary.Command(Project{Name: "engi"}, ActionRecreate)

	want := []string{"docker", "compose", "-p", "engi", "up", "-d", "--force-recreate"}
	if !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// Recreating one service must leave the rest of the stack alone. Without
// --no-deps Compose recreates everything the service depends_on, which turns
// "restart the API" into an outage of the database behind it.
func TestCommandRecreatesOneServiceWithoutItsDependencies(t *testing.T) {
	got := pluginBinary.Command(Project{Name: "engi"}, ActionRecreate, "api")

	want := []string{
		"docker", "compose", "-p", "engi",
		"up", "-d", "--force-recreate", "--no-deps", "api",
	}
	if !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// --no-deps belongs to recreating a single service, not to naming a service.
// An "up" that names a service is asking for that service to exist, which
// means its dependencies have to exist too.
func TestCommandKeepsDependenciesForAScopedUp(t *testing.T) {
	got := pluginBinary.Command(Project{Name: "engi"}, ActionUp, "api")

	want := []string{"docker", "compose", "-p", "engi", "up", "-d", "api"}
	if !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// Validation renders the configuration and reports what is wrong with it
// without touching a single container, which is what makes it worth running
// before an edit is applied.
func TestCommandValidatesQuietly(t *testing.T) {
	got := pluginBinary.Command(Project{Name: "engi"}, ActionConfig)

	want := []string{"docker", "compose", "-p", "engi", "config", "-q"}
	if !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// Recreate is offered only where it can do something: a project with nothing
// running has no container to replace, and "up" already covers it.
func TestActionsOfferRecreateOnlyForARunningProject(t *testing.T) {
	running := Project{Containers: []docker.Container{{State: "running"}}}
	if !slices.Contains(Actions(running), ActionRecreate) {
		t.Error("a running project must be offered recreate")
	}
	if slices.Contains(Actions(Project{}), ActionRecreate) {
		t.Error("a project with nothing running must not be offered recreate")
	}
}

// RunCaptured must return both streams: Compose spreads its diagnostics over
// stdout and stderr, and only their interleaving tells the story.
func TestRunCapturedCombinesBothStreams(t *testing.T) {
	output, err := RunCaptured(t.Context(), []string{"sh", "-c", "echo out; echo err >&2"})
	if err != nil {
		t.Fatalf("RunCaptured: %v", err)
	}
	for _, want := range []string{"out", "err"} {
		if !strings.Contains(output, want) {
			t.Errorf("got %q, want %q captured", output, want)
		}
	}
}

// A failing command must still surrender its output, because the output is
// the reason it failed.
func TestRunCapturedReturnsOutputWithTheError(t *testing.T) {
	output, err := RunCaptured(t.Context(), []string{"sh", "-c", "echo doomed; exit 3"})
	if err == nil {
		t.Fatal("expected the exit status as an error")
	}
	if !strings.Contains(output, "doomed") {
		t.Errorf("got %q, want the output kept alongside the error", output)
	}
}

func stubProbes(plugin, standalone bool) (restore func()) {
	oldPlugin, oldStandalone := pluginInstalled, standaloneInstalled
	pluginInstalled = func() bool { return plugin }
	standaloneInstalled = func() bool { return standalone }
	return func() { pluginInstalled, standaloneInstalled = oldPlugin, oldStandalone }
}

// pluginBinary is the form the older command tests were written against.
var pluginBinary = Binary{"docker", "compose"}
