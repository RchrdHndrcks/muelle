package compose

import (
	"slices"
	"strings"
	"testing"
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

func stubProbes(plugin, standalone bool) (restore func()) {
	oldPlugin, oldStandalone := pluginInstalled, standaloneInstalled
	pluginInstalled = func() bool { return plugin }
	standaloneInstalled = func() bool { return standalone }
	return func() { pluginInstalled, standaloneInstalled = oldPlugin, oldStandalone }
}

// pluginBinary is the form the older command tests were written against.
var pluginBinary = Binary{"docker", "compose"}
