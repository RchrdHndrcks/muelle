package autodeploy

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/RchrdHndrcks/muelle/internal/compose"
	"github.com/RchrdHndrcks/muelle/internal/config"
	"github.com/RchrdHndrcks/muelle/internal/docker"
)

// writeComposeFile drops a minimal compose file into dir, so Discover finds a
// project there.
func writeComposeFile(dir string) error {
	return os.WriteFile(filepath.Join(dir, "compose.yaml"), []byte("services: {}\n"), 0o644)
}

// composeProjectName is the name Discover derives for a directory project.
func composeProjectName(dir string) string { return filepath.Base(dir) }

// fakeDocker scripts the daemon answers a cycle needs and records whether a
// prune happened.
type fakeDocker struct {
	containers []docker.Container
	// imageIDs maps an image reference to the ID it now resolves to.
	imageIDs map[string]string
	// resolveErr fails every ImageID call, for the path where the daemon
	// cannot answer.
	resolveErr error
	pruned     bool
	pruneErr   error
}

func (f *fakeDocker) Containers(context.Context, bool) ([]docker.Container, error) {
	return f.containers, nil
}

func (f *fakeDocker) ImageID(_ context.Context, reference string) (string, error) {
	if f.resolveErr != nil {
		return "", f.resolveErr
	}
	id, ok := f.imageIDs[reference]
	if !ok {
		return "", fmt.Errorf("no such image: %s", reference)
	}
	return id, nil
}

func (f *fakeDocker) PruneImages(context.Context) (docker.PruneResult, error) {
	f.pruned = true
	return docker.PruneResult{}, f.pruneErr
}

// fakeRunner records every argv it is asked to run and can fail pulls.
type fakeRunner struct {
	commands [][]string
	pullErr  error
	upErr    error
}

func (f *fakeRunner) run(_ context.Context, argv []string) (string, error) {
	f.commands = append(f.commands, argv)
	if slices.Contains(argv, "pull") && f.pullErr != nil {
		return "manifest unknown", f.pullErr
	}
	if slices.Contains(argv, "up") && f.upErr != nil {
		return "service failed to start", f.upErr
	}
	return "", nil
}

// ran reports whether any recorded command contains the subcommand.
func (f *fakeRunner) ran(subcommand string) bool {
	for _, argv := range f.commands {
		if slices.Contains(argv, subcommand) {
			return true
		}
	}
	return false
}

// shopContainer builds a running shop container for the api service.
func shopContainer(service, image, imageID string, running bool) docker.Container {
	state := "running"
	if !running {
		state = "exited"
	}
	return docker.Container{
		ID: service + "-1", Names: []string{"/shop-" + service + "-1"},
		Image: image, ImageID: imageID, State: state,
		Labels: map[string]string{
			docker.LabelProject:     "shop",
			docker.LabelService:     service,
			docker.LabelWorkingDir:  "/srv/shop",
			docker.LabelConfigFiles: "/srv/shop/compose.yaml",
		},
	}
}

// newDeployer wires a deployer around the fakes with one enrolled project.
func newDeployer(daemon *fakeDocker, runner *fakeRunner) *Deployer {
	return &Deployer{
		Settings: config.AutoDeploy{Projects: []string{"shop"}, IntervalMinutes: 15},
		Binary:   compose.Binary{"docker", "compose"},
		Docker:   daemon,
		Run:      runner.run,
	}
}

func cycle(t *testing.T, d *Deployer) Outcome {
	t.Helper()
	outcomes, err := d.Cycle(context.Background())
	if err != nil {
		t.Fatalf("Cycle: %v", err)
	}
	if len(outcomes) != 1 {
		t.Fatalf("got %d outcomes, want 1", len(outcomes))
	}
	return outcomes[0]
}

// A tag resolving to a new ID after the pull is the signal the whole feature
// keys on: the service must be recreated.
func TestCycleDeploysWhenAnImageIDDiverges(t *testing.T) {
	daemon := &fakeDocker{
		containers: []docker.Container{
			shopContainer("api", "shop/api:1.4", "sha256:old", true),
			shopContainer("db", "mysql:8.0", "sha256:db", true),
		},
		imageIDs: map[string]string{"shop/api:1.4": "sha256:new", "mysql:8.0": "sha256:db"},
	}
	runner := &fakeRunner{}

	outcome := cycle(t, newDeployer(daemon, runner))

	if outcome.Action != ActionDeploy {
		t.Fatalf("got action %q, want %q", outcome.Action, ActionDeploy)
	}
	if !slices.Equal(outcome.Changed, []string{"api"}) {
		t.Errorf("got changed %v, want only the diverged service", outcome.Changed)
	}
	if !runner.ran("up") {
		t.Error("a diverged image must trigger compose up")
	}
	if outcome.Error != "" {
		t.Errorf("unexpected error: %s", outcome.Error)
	}
}

// When every running container was created from the ID its tag still
// resolves to, the cycle must touch nothing.
func TestCycleDoesNothingWhenImagesAreCurrent(t *testing.T) {
	daemon := &fakeDocker{
		containers: []docker.Container{
			shopContainer("api", "shop/api:1.4", "sha256:same", true),
		},
		imageIDs: map[string]string{"shop/api:1.4": "sha256:same"},
	}
	runner := &fakeRunner{}

	outcome := cycle(t, newDeployer(daemon, runner))

	if outcome.Action != ActionNone {
		t.Fatalf("got action %q, want %q", outcome.Action, ActionNone)
	}
	if runner.ran("up") {
		t.Error("nothing diverged, so compose up must not run")
	}
	if !runner.ran("pull") {
		t.Error("the pull is what makes the comparison meaningful; it must always run")
	}
}

// A service whose container stopped is not serving whatever the image IDs
// say; enrolment means it should be.
func TestCycleDeploysWhenAServiceHasNoRunningContainer(t *testing.T) {
	daemon := &fakeDocker{
		containers: []docker.Container{
			shopContainer("api", "shop/api:1.4", "sha256:same", true),
			shopContainer("worker", "shop/worker:1.4", "sha256:w", false),
		},
		imageIDs: map[string]string{"shop/api:1.4": "sha256:same"},
	}
	runner := &fakeRunner{}

	outcome := cycle(t, newDeployer(daemon, runner))

	if outcome.Action != ActionDeploy {
		t.Fatalf("got action %q, want %q", outcome.Action, ActionDeploy)
	}
	if !slices.Equal(outcome.Changed, []string{"worker"}) {
		t.Errorf("got changed %v, want the stopped service", outcome.Changed)
	}
}

// A fully stopped project found only on disk has nothing to compare; being
// enrolled means "up -d" is what makes it exist.
func TestCycleDeploysAProjectWithNoContainers(t *testing.T) {
	dir := t.TempDir()
	if err := writeComposeFile(dir); err != nil {
		t.Fatal(err)
	}
	daemon := &fakeDocker{}
	runner := &fakeRunner{}
	deployer := newDeployer(daemon, runner)
	deployer.Settings.Projects = []string{composeProjectName(dir)}
	deployer.ComposeDirs = []string{dir}

	outcomes, err := deployer.Cycle(context.Background())
	if err != nil {
		t.Fatalf("Cycle: %v", err)
	}
	if outcomes[0].Action != ActionDeploy {
		t.Fatalf("got action %q, want %q", outcomes[0].Action, ActionDeploy)
	}
	if !runner.ran("up") {
		t.Error("a fully stopped enrolled project must be brought up")
	}
}

// A failed pull leaves the images in an unknown state, so the cycle must
// record the failure and not deploy on top of it.
func TestCyclePullFailureIsRecordedAndBlocksUp(t *testing.T) {
	daemon := &fakeDocker{
		containers: []docker.Container{
			shopContainer("api", "shop/api:1.4", "sha256:old", true),
		},
		imageIDs: map[string]string{"shop/api:1.4": "sha256:new"},
	}
	runner := &fakeRunner{pullErr: errors.New("exit status 1")}

	outcome := cycle(t, newDeployer(daemon, runner))

	if outcome.Error == "" || !strings.Contains(outcome.Error, "pull") {
		t.Errorf("got error %q, want the pull failure recorded", outcome.Error)
	}
	if runner.ran("up") {
		t.Error("compose up must not run after a failed pull")
	}
	if outcome.Action == ActionDeploy {
		t.Error("a failed pull is not a deploy")
	}
}

// An unanswerable image lookup means divergence is unknown, and unknown must
// not mean deploy — otherwise every daemon hiccup recreates containers.
func TestCycleResolveFailureIsRecordedAndBlocksUp(t *testing.T) {
	daemon := &fakeDocker{
		containers: []docker.Container{
			shopContainer("api", "shop/api:1.4", "sha256:old", true),
		},
		resolveErr: errors.New("daemon went away"),
	}
	runner := &fakeRunner{}

	outcome := cycle(t, newDeployer(daemon, runner))

	if outcome.Error == "" {
		t.Error("a failed image resolution must be recorded")
	}
	if runner.ran("up") {
		t.Error("compose up must not run when divergence could not be determined")
	}
}

// An enrolled name with no containers and no compose file cannot be acted on;
// the outcome must say so rather than pretending a check happened.
func TestCycleSkipsAProjectWithNoKnownFiles(t *testing.T) {
	daemon := &fakeDocker{}
	runner := &fakeRunner{}

	outcome := cycle(t, newDeployer(daemon, runner))

	if outcome.Action != ActionSkip {
		t.Fatalf("got action %q, want %q", outcome.Action, ActionSkip)
	}
	if outcome.Error == "" {
		t.Error("a skip must carry its reason")
	}
	if len(runner.commands) != 0 {
		t.Errorf("nothing should run for a skipped project, got %v", runner.commands)
	}
}

// Prune runs only when enabled and only after a deploy actually happened:
// that is the moment a superseded image became dangling.
func TestCyclePrunesOnlyAfterADeploy(t *testing.T) {
	stale := func() *fakeDocker {
		return &fakeDocker{
			containers: []docker.Container{
				shopContainer("api", "shop/api:1.4", "sha256:old", true),
			},
			imageIDs: map[string]string{"shop/api:1.4": "sha256:new"},
		}
	}
	current := func() *fakeDocker {
		return &fakeDocker{
			containers: []docker.Container{
				shopContainer("api", "shop/api:1.4", "sha256:same", true),
			},
			imageIDs: map[string]string{"shop/api:1.4": "sha256:same"},
		}
	}

	daemon := stale()
	deployer := newDeployer(daemon, &fakeRunner{})
	deployer.Settings.Prune = true
	cycle(t, deployer)
	if !daemon.pruned {
		t.Error("prune enabled and a deploy happened: images should have been pruned")
	}

	daemon = current()
	deployer = newDeployer(daemon, &fakeRunner{})
	deployer.Settings.Prune = true
	cycle(t, deployer)
	if daemon.pruned {
		t.Error("nothing was deployed, so nothing should be pruned")
	}

	daemon = stale()
	deployer = newDeployer(daemon, &fakeRunner{})
	cycle(t, deployer)
	if daemon.pruned {
		t.Error("prune is off by default and must not run")
	}
}

// The argv must identify the project explicitly and pull quietly: the daemon
// has no working directory relationship with the project and no terminal for
// progress bars.
func TestPullAndUpArgvConstruction(t *testing.T) {
	binary := compose.Binary{"docker", "compose"}
	project := compose.Project{
		Name:        "shop",
		WorkingDir:  "/srv/shop",
		ConfigFiles: []string{"/srv/shop/compose.yaml"},
	}

	wantPull := []string{
		"docker", "compose",
		"-f", "/srv/shop/compose.yaml",
		"--project-directory", "/srv/shop",
		"-p", "shop",
		"pull", "-q",
	}
	if got := pullArgv(binary, project); !slices.Equal(got, wantPull) {
		t.Errorf("pull argv:\n got %v\nwant %v", got, wantPull)
	}

	wantUp := []string{
		"docker", "compose",
		"-f", "/srv/shop/compose.yaml",
		"--project-directory", "/srv/shop",
		"-p", "shop",
		"up", "-d",
	}
	if got := upArgv(binary, project); !slices.Equal(got, wantUp) {
		t.Errorf("up argv:\n got %v\nwant %v", got, wantUp)
	}
}

// Replicas share an image; the daemon must not ask about the same reference
// once per container.
func TestChangedServicesResolvesEachImageOnce(t *testing.T) {
	calls := 0
	daemon := &countingDocker{
		fakeDocker: fakeDocker{
			imageIDs: map[string]string{"shop/api:1.4": "sha256:same"},
		},
		calls: &calls,
	}
	deployer := newDeployer(&daemon.fakeDocker, &fakeRunner{})
	deployer.Docker = daemon

	replicas := compose.Project{Name: "shop", Containers: []docker.Container{
		shopContainer("api", "shop/api:1.4", "sha256:same", true),
		shopContainer("api", "shop/api:1.4", "sha256:same", true),
	}}
	if _, err := deployer.changedServices(context.Background(), replicas); err != nil {
		t.Fatalf("changedServices: %v", err)
	}
	if calls != 1 {
		t.Errorf("got %d ImageID calls, want 1 for one distinct image", calls)
	}
}

// countingDocker counts ImageID calls on top of the scripted answers.
type countingDocker struct {
	fakeDocker
	calls *int
}

func (c *countingDocker) ImageID(ctx context.Context, reference string) (string, error) {
	*c.calls++
	return c.fakeDocker.ImageID(ctx, reference)
}
