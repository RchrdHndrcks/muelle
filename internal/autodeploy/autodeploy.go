// Package autodeploy keeps enrolled Compose projects running their newest
// images, unattended.
//
// The problem it replaces is a manual one: images move in a registry, and
// someone has to notice, log in, and run pull and up by hand. On a single
// host that ceremony has no coordination to justify it — there is no fleet,
// no rollout order, no approval step — so the whole job reduces to a loop:
// pull, compare, apply.
//
// Exactly one deployer. The daemon started by "muelle -deploy" is the only
// thing that ever deploys automatically; the TUI reads the same configuration
// and shows the daemon's outcomes, but never acts on them. Two deployers
// racing each other — one pulling while the other is mid-up — is the failure
// mode this rule exists to prevent, and a rule is cheaper than a lock file
// that can go stale.
//
// Detection is by image ID divergence, not by pull output. A running
// container records the exact image ID it was created from; after a pull, the
// daemon asks the Docker daemon what each container's image *name* resolves
// to now. If the two IDs differ, the tag has moved and the service is stale.
// This is the same comparison a human makes reading "docker images" next to
// "docker ps", but exact: no parsing of Compose's progress text, no guessing
// whether "Pulled" meant "changed". A service with no running container is
// stale by definition — whatever the images say, it is not serving.
//
// The pull itself is delegated to Compose rather than reimplemented against
// the registry API. Compose already knows which services name which images,
// how to authenticate, and what platform to select; doing any of that here
// would duplicate the tool this project explicitly leans on for lifecycle
// work, and could disagree with it. The daemon runs "compose pull -q"
// captured, then decides, then — only when something diverged — runs
// "compose up -d", which recreates exactly the services whose configuration
// or image changed and leaves the rest alone.
package autodeploy

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/RchrdHndrcks/muelle/internal/compose"
	"github.com/RchrdHndrcks/muelle/internal/config"
	"github.com/RchrdHndrcks/muelle/internal/docker"
)

// Docker is the slice of the daemon client a deploy cycle needs. An interface
// so the decision logic is testable without a Docker daemon, which the test
// environment does not have.
type Docker interface {
	Containers(ctx context.Context, all bool) ([]docker.Container, error)
	ImageID(ctx context.Context, reference string) (string, error)
	PruneImages(ctx context.Context) (docker.PruneResult, error)
}

// Runner executes a Compose argv and returns its captured output. Production
// wiring passes compose.RunCaptured; tests record the argv and script the
// result.
type Runner func(ctx context.Context, argv []string) (string, error)

// Deployer runs the deploy cycle for the enrolled projects.
type Deployer struct {
	// Settings is the auto_deploy block from the configuration file.
	Settings config.AutoDeploy
	// ComposeDirs are scanned for fully stopped projects, exactly as the
	// TUI scans them: an enrolled project that is down still has files on
	// disk, and those are what "up -d" needs.
	ComposeDirs []string
	// Binary is how Compose is invoked on this machine.
	Binary compose.Binary
	// Docker answers the image questions and prunes.
	Docker Docker
	// Run executes Compose commands.
	Run Runner
	// StatePath is where outcomes are recorded for the TUI to read. Empty
	// disables recording.
	StatePath string
	// Log receives one plain line per check or deploy. Nil discards. No
	// styling on purpose: this is written for journald and log files, so
	// NO_COLOR is honoured by never colouring in the first place.
	Log io.Writer
	// Now is injected for tests; nil means time.Now.
	Now func() time.Time
}

// Actions taken by a cycle, recorded in each outcome.
const (
	// ActionDeploy means "compose up -d" ran (successfully or not).
	ActionDeploy = "deploy"
	// ActionNone means everything was already current.
	ActionNone = "none"
	// ActionSkip means the project could not be acted on at all — not
	// found, or no compose file known.
	ActionSkip = "skip"
)

// Outcome records what one cycle decided for one project. It is what the
// state file stores and what the TUI's AUTO column summarises.
type Outcome struct {
	Time    time.Time `json:"time"`
	Project string    `json:"project"`
	// Changed lists the services that diverged, empty for a project that
	// was deployed because nothing of it was running.
	Changed []string `json:"changed_services,omitempty"`
	// Action is ActionDeploy, ActionNone or ActionSkip.
	Action string `json:"action"`
	// Error is empty on success. A skip records its reason here too: an
	// enrolled project that cannot be deployed is a problem to surface,
	// not a neutral fact.
	Error string `json:"error,omitempty"`
}

// Failed reports whether the outcome needs attention.
func (o Outcome) Failed() bool { return o.Error != "" }

// Loop runs the cycle immediately and then on every interval tick until the
// context is cancelled. Cancellation is the clean-shutdown path — SIGINT and
// SIGTERM arrive here as a done context — so it returns nil rather than the
// context's error.
func (d *Deployer) Loop(ctx context.Context) error {
	// Immediately first: a daemon that waits a full interval before doing
	// anything looks broken for its first fifteen minutes.
	d.RunOnce(ctx)

	ticker := time.NewTicker(d.Settings.Interval())
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			d.RunOnce(ctx)
		}
	}
}

// RunOnce performs one full cycle: check every enrolled project, log each
// outcome, and record them in the state file.
func (d *Deployer) RunOnce(ctx context.Context) {
	outcomes, err := d.Cycle(ctx)
	if err != nil {
		// A whole-cycle failure means the daemon itself was unreachable;
		// no per-project outcome exists to record, but the check still
		// happened and the state file should say so.
		d.logf("check failed: %v", err)
	}
	for _, outcome := range outcomes {
		d.logf("%s: %s", outcome.Project, summarise(outcome))
	}

	if d.StatePath == "" {
		return
	}
	state := LoadState(d.StatePath)
	state.LastCheck = d.now()
	for _, outcome := range outcomes {
		state.Projects[outcome.Project] = outcome
	}
	if err := SaveState(d.StatePath, state); err != nil {
		d.logf("state: %v", err)
	}
}

// Cycle checks every enrolled project once and returns their outcomes. The
// error is a whole-cycle failure — the container list could not be fetched,
// so nothing could even be decided.
func (d *Deployer) Cycle(ctx context.Context) ([]Outcome, error) {
	if len(d.Settings.Projects) == 0 {
		return nil, nil
	}

	// Stopped containers included: a project whose containers all exited
	// still exists, and its stopped services are exactly what needs
	// deploying. The same merge the TUI does fills in projects that are
	// fully down and exist only on disk.
	containers, err := d.Docker.Containers(ctx, true)
	if err != nil {
		return nil, fmt.Errorf("list containers: %w", err)
	}
	projects := compose.Merge(
		compose.FromContainers(containers),
		compose.Discover(d.ComposeDirs),
	)
	byName := make(map[string]compose.Project, len(projects))
	for _, project := range projects {
		byName[project.Name] = project
	}

	outcomes := make([]Outcome, 0, len(d.Settings.Projects))
	for _, name := range d.Settings.Projects {
		outcomes = append(outcomes, d.deploy(ctx, name, byName))
	}
	return outcomes, nil
}

// deploy runs the cycle for one project: pull, compare, and apply when
// something diverged.
func (d *Deployer) deploy(ctx context.Context, name string, byName map[string]compose.Project) Outcome {
	outcome := Outcome{Time: d.now(), Project: name}

	project, found := byName[name]
	if !found {
		outcome.Action = ActionSkip
		outcome.Error = "project not found: no containers carry its label and no compose file was found in the scanned directories"
		return outcome
	}
	if len(project.ConfigFiles) == 0 && project.WorkingDir == "" {
		// Compose cannot be pointed at the project. This happens when the
		// containers were created by an older Compose that stamped no path
		// labels and the project directory is not in compose_dirs.
		outcome.Action = ActionSkip
		outcome.Error = "no compose file known for this project"
		return outcome
	}

	if output, err := d.Run(ctx, pullArgv(d.Binary, project)); err != nil {
		// No up after a failed pull: deploying on top of images in an
		// unknown state is how a partial registry outage becomes a
		// partial deployment.
		outcome.Action = ActionNone
		outcome.Error = fmt.Sprintf("pull: %v%s", err, quoteOutput(output))
		return outcome
	}

	changed, err := d.changedServices(ctx, project)
	if err != nil {
		// The comparison could not be completed, so whether anything
		// diverged is unknown — and deploying on "unknown" would make
		// every daemon hiccup a deployment.
		outcome.Action = ActionNone
		outcome.Error = err.Error()
		return outcome
	}
	// A project with no containers at all is enrolled to be running; there
	// is nothing to compare, and "up -d" is what makes it exist.
	if len(changed) == 0 && len(project.Containers) > 0 {
		outcome.Action = ActionNone
		return outcome
	}

	outcome.Action = ActionDeploy
	outcome.Changed = changed
	if output, err := d.Run(ctx, upArgv(d.Binary, project)); err != nil {
		outcome.Error = fmt.Sprintf("up: %v%s", err, quoteOutput(output))
		return outcome
	}

	// Pruning only after a deploy: that is the moment a superseded image
	// just became dangling, and the only reason this daemon has to delete
	// anything.
	if d.Settings.Prune {
		if _, err := d.Docker.PruneImages(ctx); err != nil {
			// The deploy itself succeeded; a failed cleanup must not
			// make it look like it did not. Recorded, not fatal.
			outcome.Error = fmt.Sprintf("prune after deploy: %v", err)
		}
	}
	return outcome
}

// changedServices names the services that need deploying: those whose running
// container was created from an image ID their image tag no longer resolves
// to, and those with no running container at all.
func (d *Deployer) changedServices(ctx context.Context, project compose.Project) ([]string, error) {
	changed := make(map[string]bool)
	running := make(map[string]bool)
	// Resolved once per image name, not per container: replicas of a
	// service share an image, and the answer cannot differ within a cycle.
	resolved := make(map[string]string)

	for _, container := range project.Containers {
		service := container.Service()
		if service == "" || !container.Running() {
			continue
		}
		running[service] = true

		id, seen := resolved[container.Image]
		if !seen {
			var err error
			id, err = d.Docker.ImageID(ctx, container.Image)
			if err != nil {
				return nil, fmt.Errorf("resolve image %s: %w", container.Image, err)
			}
			resolved[container.Image] = id
		}
		if container.ImageID != "" && id != container.ImageID {
			changed[service] = true
		}
	}

	// A service whose containers all stopped is not serving, whatever the
	// image IDs say; being enrolled means it should be.
	for _, service := range project.Services() {
		if !running[service] {
			changed[service] = true
		}
	}

	services := make([]string, 0, len(changed))
	for service := range changed {
		services = append(services, service)
	}
	sort.Strings(services)
	return services, nil
}

// pullArgv builds the pull command: quiet, because the progress bars are
// drawn for a terminal this process does not have, and captured output is
// only ever read when something failed.
func pullArgv(binary compose.Binary, project compose.Project) []string {
	return append(binary.Command(project, compose.ActionPull), "-q")
}

// upArgv builds the apply command. Plain "up -d": Compose itself recreates
// only the services whose image or configuration changed, which is exactly
// the divergence this daemon just measured.
func upArgv(binary compose.Binary, project compose.Project) []string {
	return binary.Command(project, compose.ActionUp)
}

// summarise renders an outcome as the daemon's log line for it.
func summarise(outcome Outcome) string {
	switch {
	case outcome.Action == ActionSkip:
		return "skipped: " + outcome.Error
	case outcome.Error != "" && outcome.Action == ActionDeploy:
		return "deploy failed: " + outcome.Error
	case outcome.Error != "":
		return "check failed: " + outcome.Error
	case outcome.Action == ActionDeploy && len(outcome.Changed) > 0:
		return "deployed (" + strings.Join(outcome.Changed, ", ") + ")"
	case outcome.Action == ActionDeploy:
		return "deployed (project was not running)"
	default:
		return "up to date"
	}
}

// quoteOutput appends captured Compose output to an error message, trimmed —
// the last lines are where Compose puts the reason.
func quoteOutput(output string) string {
	output = strings.TrimSpace(output)
	if output == "" {
		return ""
	}
	lines := strings.Split(output, "\n")
	if len(lines) > 5 {
		lines = lines[len(lines)-5:]
	}
	return ": " + strings.Join(lines, " / ")
}

// logf writes one plain line, stamped with the wall clock so the log is
// useful in a plain file as well as under journald's own timestamps.
func (d *Deployer) logf(format string, args ...any) {
	if d.Log == nil {
		return
	}
	fmt.Fprintf(d.Log, "%s %s\n", d.now().Format(time.RFC3339), fmt.Sprintf(format, args...))
}

// now returns the injected clock, or the real one.
func (d *Deployer) now() time.Time {
	if d.Now != nil {
		return d.Now()
	}
	return time.Now()
}
