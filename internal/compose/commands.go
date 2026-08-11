package compose

import (
	"context"
	"fmt"
	"os/exec"
)

// Binary is the argv prefix that invokes Compose.
//
// Compose ships in two shapes and muelle cannot assume either. The modern one
// is a CLI plugin invoked as "docker compose", installed under
// ~/.docker/cli-plugins or inside Docker Desktop. The other is a standalone
// "docker-compose" executable on PATH, which is what Homebrew installs and
// what every pre-plugin installation has. A machine can have either, both, or
// only the standalone one — and in that last case the docker binary exists and
// answers, so checking for it proves nothing about Compose.
type Binary []string

// Available reports whether Compose can be run at all.
func (b Binary) Available() bool { return len(b) > 0 }

// Probes for each form, as variables so the selection rule can be tested
// without depending on what happens to be installed on the machine running the
// tests.
var (
	// pluginInstalled asks the docker CLI whether it knows the subcommand.
	// Looking for the plugin file instead would have to guess at every
	// directory Docker searches, which differs by platform and install
	// method; asking the CLI is the only answer that cannot be wrong.
	pluginInstalled = func() bool {
		return exec.Command("docker", "compose", "version").Run() == nil
	}
	standaloneInstalled = func() bool {
		_, err := exec.LookPath("docker-compose")
		return err == nil
	}
)

// Detect finds an installed Compose, preferring the plugin, and returns nil
// when there is none.
//
// Called once at startup: the probe runs a subprocess, and the answer cannot
// change while muelle is on screen without the user installing something.
func Detect() Binary {
	if pluginInstalled() {
		return Binary{"docker", "compose"}
	}
	if standaloneInstalled() {
		return Binary{"docker-compose"}
	}
	return nil
}

// Action is a Compose lifecycle operation.
type Action string

// Supported project actions.
//
// Most are Compose subcommands, but two are not: recreate and config stand
// for a subcommand plus the flags that give it its meaning. See args.
const (
	ActionUp       Action = "up"
	ActionDown     Action = "down"
	ActionRestart  Action = "restart"
	ActionRecreate Action = "recreate"
	ActionStop     Action = "stop"
	ActionStart    Action = "start"
	ActionPull     Action = "pull"
	ActionBuild    Action = "build"
	ActionPS       Action = "ps"
	ActionLogs     Action = "logs"
	ActionConfig   Action = "config"
	// ActionConfigHash is never offered in a menu: it exists for muelle to
	// read, not the user to run. See ServiceHashes.
	ActionConfigHash Action = "config-hash"
)

// args returns the subcommand and flags the action runs as.
//
// Recreate is the reason this is a method rather than the name of a
// subcommand: a container's environment, image and ports are fixed when it is
// created, so no restart of any kind can apply an edited compose file. Only
// replacing the container does, and Compose spells that "up --force-recreate".
func (a Action) args() []string {
	switch a {
	case ActionUp:
		// Detached: the TUI resumes as soon as the stack is up rather
		// than sitting attached to the aggregated log stream.
		return []string{"up", "-d"}
	case ActionRecreate:
		return []string{"up", "-d", "--force-recreate"}
	case ActionLogs:
		return []string{"logs", "-f", "--tail", "200"}
	case ActionConfig:
		// Renders and validates the configuration without touching a
		// container, which is what makes it safe to run before an edit
		// is applied.
		return []string{"config", "-q"}
	case ActionConfigHash:
		// Prints one "<service> <hash>" line per service. The "*" is an
		// argument for Compose, not a glob for a shell — argv is executed
		// directly — so it needs no quoting and cannot expand.
		return []string{"config", "--hash", "*"}
	default:
		return []string{string(a)}
	}
}

// Destructive reports whether an action removes containers, and so should be
// confirmed before running.
func (a Action) Destructive() bool { return a == ActionDown }

// Label returns a human-readable description for menus.
func (a Action) Label() string {
	switch a {
	case ActionUp:
		return "up -d (create and start)"
	case ActionDown:
		return "down (stop and remove)"
	case ActionRestart:
		return "restart (does not apply config changes)"
	case ActionRecreate:
		return "recreate (apply current config)"
	case ActionStop:
		return "stop"
	case ActionStart:
		return "start"
	case ActionPull:
		return "pull images"
	case ActionBuild:
		return "build images"
	case ActionPS:
		return "ps (list containers)"
	case ActionLogs:
		return "logs (follow)"
	default:
		return string(a)
	}
}

// Command builds the argv for running an action against a project, optionally
// scoped to particular services.
//
// The project is identified explicitly — by config file, project directory and
// project name — rather than by relying on the process working directory, so
// the command behaves identically no matter where muelle was launched from.
// The returned argv is executed directly, never through a shell, so paths
// containing spaces need no quoting.
func (b Binary) Command(project Project, action Action, services ...string) []string {
	// Copied rather than appended to in place: the same Binary builds every
	// command for the whole session.
	argv := append([]string(nil), b...)

	for _, file := range project.ConfigFiles {
		argv = append(argv, "-f", file)
	}
	if project.WorkingDir != "" {
		argv = append(argv, "--project-directory", project.WorkingDir)
	}
	if project.Name != "" {
		// Without this, Compose derives the project name from the
		// directory, which can differ from the name the containers were
		// actually created under.
		argv = append(argv, "-p", project.Name)
	}

	argv = append(argv, action.args()...)

	// Recreating one service must leave the rest of the stack alone.
	// Without this, Compose replaces everything the service depends_on as
	// well, turning "restart the API" into an outage of the database behind
	// it. Naming a service in any other action is a request for it to be
	// running, which its dependencies are part of.
	if len(services) > 0 && action == ActionRecreate {
		argv = append(argv, "--no-deps")
	}
	return append(argv, services...)
}

// RunCaptured executes an argv built by Command with its output captured
// rather than attached to a terminal.
//
// The TUI hands the terminal over and lets Compose talk to the user directly;
// an unattended caller — the auto-deploy daemon — has no terminal to hand
// over and nobody watching, so the output is collected instead, to be quoted
// in an error when something fails. Stdout and stderr are combined because
// Compose spreads its diagnostics across both and only their interleaving
// tells the story.
func RunCaptured(ctx context.Context, argv []string) (string, error) {
	if len(argv) == 0 {
		return "", fmt.Errorf("compose: empty command")
	}
	command := exec.CommandContext(ctx, argv[0], argv[1:]...)
	// No stdin: a prompt from Compose must fail rather than hang a daemon
	// forever waiting for an answer nobody is there to give.
	command.Stdin = nil
	output, err := command.CombinedOutput()
	return string(output), err
}

// Actions lists the actions offered for a project, in menu order. A project
// with nothing running cannot be restarted or stopped, so those are omitted.
func Actions(project Project) []Action {
	if project.Running() == 0 {
		return []Action{ActionUp, ActionPull, ActionBuild, ActionPS, ActionDown}
	}
	return []Action{
		ActionRestart,
		ActionRecreate,
		ActionUp,
		ActionStop,
		ActionLogs,
		ActionPS,
		ActionPull,
		ActionBuild,
		ActionDown,
	}
}
