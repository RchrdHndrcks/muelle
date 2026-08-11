// Command muelle is a terminal UI for managing Docker containers and Compose
// projects on a single host.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"runtime/debug"
	"strings"
	"syscall"

	"github.com/RchrdHndrcks/muelle/internal/autodeploy"
	"github.com/RchrdHndrcks/muelle/internal/compose"
	"github.com/RchrdHndrcks/muelle/internal/config"
	"github.com/RchrdHndrcks/muelle/internal/docker"
	"github.com/RchrdHndrcks/muelle/internal/tui"
	"github.com/RchrdHndrcks/muelle/internal/ui"
)

// version is stamped at build time with -ldflags "-X main.version=...".
var version = ""

// buildVersion describes the running binary.
//
// A release build stamps version through ldflags, but "go install" does not,
// and the module version the toolchain records is then the only evidence of
// what is actually running. Reporting it matters more than it sounds: a stale
// binary looks exactly like a bug that was never fixed, and without this there
// is no way to tell the two apart from inside the program.
func buildVersion() string {
	if version != "" {
		return version
	}
	info, ok := debug.ReadBuildInfo()
	if !ok || info.Main.Version == "" {
		return "unknown"
	}
	if info.Main.Version == "(devel)" {
		// A local build: the module version is meaningless, but the
		// revision the toolchain stamped in is not.
		for _, setting := range info.Settings {
			if setting.Key == "vcs.revision" && len(setting.Value) >= 7 {
				return "devel-" + setting.Value[:7]
			}
		}
	}
	return info.Main.Version
}

// shortBuildVersion condenses the build for the header, where a
// forty-character pseudo-version would crowd out everything else. A tagged
// release keeps its tag; anything else shows the commit.
func shortBuildVersion() string {
	full := buildVersion()
	if !strings.HasPrefix(full, "v0.0.0-") && !strings.HasPrefix(full, "devel-") {
		return full
	}
	trimmed := strings.TrimSuffix(full, "+dirty")
	if index := strings.LastIndex(trimmed, "-"); index >= 0 && index+1 < len(trimmed) {
		revision := trimmed[index+1:]
		if len(revision) > 7 {
			revision = revision[:7]
		}
		if strings.HasSuffix(full, "+dirty") {
			return revision + "+dirty"
		}
		return revision
	}
	return full
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "muelle: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		hostFlag    = flag.String("host", "", "docker daemon endpoint (default: $DOCKER_HOST, then autodetect)")
		viewFlag    = flag.String("view", "containers", "initial view: containers, compose, images, volumes, networks or events")
		dumpFlag    = flag.Bool("dump", false, "render a single frame to stdout and exit, for use without a terminal")
		deployFlag  = flag.Bool("deploy", false, "run the auto-deploy daemon in the foreground: no TUI, one log line per check")
		allFlag     = flag.Bool("all", false, "include stopped containers")
		versionFlag = flag.Bool("version", false, "print the version and exit")
	)
	flag.Usage = usage
	flag.Parse()

	if *versionFlag {
		fmt.Println("muelle", buildVersion())
		return nil
	}

	cfg, configPath, err := config.LoadOrCreate()
	if err != nil {
		// A broken config file should not be fatal; report it and carry
		// on with defaults.
		fmt.Fprintf(os.Stderr, "muelle: %v (using defaults)\n", err)
	}
	if *hostFlag != "" {
		cfg.DockerHost = *hostFlag
	}

	client, err := docker.New(cfg.DockerHost)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if _, err := client.Ping(ctx); err != nil {
		return fmt.Errorf("cannot reach the docker daemon at %s: %w\n\n"+
			"Check that docker is running, or set DOCKER_HOST / -host.\n"+
			"Config: %s", client.Host(), err, configPath)
	}

	if *deployFlag {
		return autoDeployDaemon(ctx, cfg, configPath, client)
	}

	view, err := parseView(*viewFlag)
	if err != nil {
		return err
	}

	if *dumpFlag {
		return dump(ctx, cfg, configPath, client, view, *allFlag)
	}
	return interactive(ctx, cfg, configPath, client, view, *allFlag)
}

// autoDeployDaemon runs the unattended deploy loop in the foreground until
// SIGINT or SIGTERM, which arrive here as context cancellation.
//
// This is the one process allowed to deploy automatically. The TUI enrols
// projects and shows outcomes but never acts on them, so running exactly one
// "muelle -deploy" — typically under systemd — is what keeps two deployers
// from racing each other.
func autoDeployDaemon(ctx context.Context, cfg config.Config, configPath string, client *docker.Client) error {
	binary := compose.Detect()
	if !binary.Available() {
		// Without Compose nothing can be pulled or applied, and a daemon
		// that would fail every cycle should say so once and exit instead.
		return fmt.Errorf("auto-deploy needs Compose: install the docker compose plugin or docker-compose")
	}
	if len(cfg.AutoDeploy.Projects) == 0 {
		// Not fatal: exiting non-zero under Restart=on-failure would make
		// systemd burst-restart a daemon whose only problem is an empty
		// list. It stays up, says why it is idle, and keeps checking in
		// case that ever reads differently — but the list is loaded once,
		// so enrolments need a restart to take effect.
		fmt.Println("auto-deploy: no projects enrolled; press W on a project in the compose view, then restart this daemon")
	}

	deployer := &autodeploy.Deployer{
		Settings:    cfg.AutoDeploy,
		ComposeDirs: cfg.ComposeDirs,
		Binary:      binary,
		Docker:      client,
		Run:         compose.RunCaptured,
		StatePath:   autodeploy.StatePathFor(configPath),
		// Plain lines on stdout, no styling at all — which is also the
		// whole of honouring NO_COLOR here.
		Log: os.Stdout,
	}
	return deployer.Loop(ctx)
}

// usage prints help text with more context than flag's default.
func usage() {
	fmt.Fprintf(os.Stderr, `muelle — terminal UI for docker

Usage:
  muelle [flags]

Flags:
`)
	flag.PrintDefaults()
	fmt.Fprintf(os.Stderr, `
Keys:
  1-6 / Tab   switch views        l   logs
  j / k       move                x   exec menu (mysql, psql, redis-cli, shells)
  Enter       inspect             ?   full key reference
  q           quit

Configuration is read from $MUELLE_CONFIG, or the muelle directory under
your user config directory (~/.config on Linux).
`)
}

// parseView maps the -view flag to a view.
func parseView(name string) (ui.View, error) {
	switch strings.ToLower(name) {
	case "containers", "container", "c":
		return ui.ViewContainers, nil
	case "compose", "projects", "p":
		return ui.ViewCompose, nil
	case "images", "image", "i":
		return ui.ViewImages, nil
	case "volumes", "volume", "v":
		return ui.ViewVolumes, nil
	case "networks", "network", "n":
		return ui.ViewNetworks, nil
	case "events", "event", "e":
		return ui.ViewEvents, nil
	default:
		return ui.ViewContainers, fmt.Errorf("unknown view %q: expected containers, compose, images, volumes, networks or events", name)
	}
}

// colourEnabled reports whether styled output should be produced. NO_COLOR is
// honoured whatever the config says, per the convention at no-color.org.
func colourEnabled(cfg config.Config) bool {
	if _, disabled := os.LookupEnv("NO_COLOR"); disabled {
		return false
	}
	return cfg.Colour
}

// dump renders one frame to stdout and exits.
//
// This exists because the interactive UI needs a controlling terminal, and
// plenty of useful contexts do not have one: CI, a script, an agent driving
// the tool. It shares the model and render path with the real app, so what it
// prints is what the TUI would show.
func dump(ctx context.Context, cfg config.Config, configPath string, client *docker.Client, view ui.View, all bool) error {
	const dumpWidth, dumpHeight = 120, 40

	width, height := dumpWidth, dumpHeight
	if tui.IsTerminal(tui.StdoutFd) {
		width, height = tui.SizeOrDefault(tui.StdoutFd)
	}

	screen := tui.NewScreen(os.Stdout, width, height, colourEnabled(cfg))
	app := ui.New(cfg, client, screen, nil)
	app.SetShowAll(all)
	app.SetView(view)
	app.SetBuild(shortBuildVersion())
	app.SetDeployStatePath(autodeploy.StatePathFor(configPath))

	if err := app.LoadOnce(ctx); err != nil {
		return err
	}
	for _, line := range app.Frame(width, height) {
		fmt.Println(strings.TrimRight(line, " "))
	}
	return nil
}

// interactive runs the full TUI.
func interactive(ctx context.Context, cfg config.Config, configPath string, client *docker.Client, view ui.View, all bool) error {
	if !tui.IsTerminal(tui.StdinFd) {
		return fmt.Errorf("stdin is not a terminal; use -dump to render a single frame instead")
	}

	state, err := tui.MakeRaw(tui.StdinFd)
	if err != nil {
		return err
	}
	// Restoring the terminal is the one thing that must happen no matter
	// how this function ends: a process that exits in raw mode leaves the
	// user's shell with no echo and no line editing. The deferred call runs
	// on the panic path too, and the recover below re-panics only after the
	// terminal is safe.
	defer tui.Restore(tui.StdinFd, state)

	width, height := tui.SizeOrDefault(tui.StdoutFd)
	screen := tui.NewScreen(os.Stdout, width, height, colourEnabled(cfg))
	// Only here, where a terminal is known to be reading the output, may
	// yank emit OSC 52 clipboard sequences. The dump mode writes to a pipe
	// and never enables this, so it reports an error instead of sending
	// escape sequences to whatever is parsing the frame.
	screen.EnableClipboard()
	if err := screen.Enter(); err != nil {
		return err
	}
	defer screen.Leave()

	keys := tui.NewTerminalKeyReader(tui.StdinFd)
	defer keys.Stop()

	runner := &ui.Runner{
		// Handing the terminal to a child means undoing everything the
		// TUI set up, then putting it back exactly as it was.
		//
		// Releasing the keyboard comes first and matters most. A reader
		// left parked in read(2) goes on competing for input the child now
		// owns: an exec session loses keystrokes to the TUI, and the
		// "press enter to continue" that follows never sees its newline,
		// because the parked reader consumes it first.
		Suspend: func() error {
			keys.Pause()
			if err := screen.Leave(); err != nil {
				return err
			}
			return tui.Restore(tui.StdinFd, state)
		},
		Resume: func() error {
			newState, err := tui.MakeRaw(tui.StdinFd)
			if err != nil {
				return err
			}
			state = newState
			// The terminal may have been resized while the child had
			// it, so re-measure rather than trusting the old size.
			width, height := tui.SizeOrDefault(tui.StdoutFd)
			screen.Resize(width, height)
			if err := screen.Enter(); err != nil {
				return err
			}
			keys.Resume()
			return nil
		},
	}

	app := ui.New(cfg, client, screen, runner)
	app.SetShowAll(all)
	app.SetView(view)
	app.SetBuild(shortBuildVersion())
	app.SetPreferenceWriter(preferenceWriter(configPath))
	app.SetDeployStatePath(autodeploy.StatePathFor(configPath))

	resize := watchResize(ctx)

	defer func() {
		if recovered := recover(); recovered != nil {
			// The deferred restore above has not run yet, so put the
			// terminal back before the panic reaches the runtime and
			// prints over a raw-mode screen.
			screen.Leave()
			tui.Restore(tui.StdinFd, state)
			panic(recovered)
		}
	}()

	return app.Run(ctx, keys.Keys(), resize)
}

// preferenceWriter returns a function that records a setting change.
//
// The write is synchronous. Doing it in a goroutine loses it: a preference is
// usually the last thing someone changes before quitting, and Go does not wait
// for goroutines when main returns — measured at better than nine times in ten
// for a change followed by a quit. Writing a couple of hundred bytes to the
// configuration directory takes microseconds, so there was never a delay worth
// avoiding, and avoiding it cost the feature entirely.
func preferenceWriter(path string) func(func(*config.Config)) error {
	if path == "" {
		return nil
	}
	return func(change func(*config.Config)) error {
		return config.Update(path, change)
	}
}

// watchResize converts SIGWINCH into a channel the event loop can select on.
func watchResize(ctx context.Context) <-chan struct{} {
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGWINCH)

	resize := make(chan struct{}, 1)
	go func() {
		defer signal.Stop(signals)
		for {
			select {
			case <-ctx.Done():
				return
			case <-signals:
				// Non-blocking: a burst of resize events while
				// dragging a window edge only needs one redraw.
				select {
				case resize <- struct{}{}:
				default:
				}
			}
		}
	}()
	return resize
}
