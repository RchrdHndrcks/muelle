package ui

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Runner executes an external command with the terminal handed over to it.
//
// muelle uses this for anything interactive — "docker exec -it", "docker
// compose up" — rather than reimplementing stream hijacking and TTY proxying
// over the Engine API. The CLI already does that correctly; the TUI just gets
// out of the way while it runs.
type Runner struct {
	// Suspend restores the terminal to its normal state before the child
	// runs, and Resume puts the TUI back afterwards. Both are supplied by
	// the caller that owns the terminal.
	Suspend func() error
	Resume  func() error
}

// ErrDockerCLIMissing is returned when the docker binary is not on PATH.
// Everything except exec and Compose actions works without it, so this is
// reported where it is needed rather than at startup.
var ErrDockerCLIMissing = errors.New("the docker CLI is required for this action but was not found on PATH")

// ErrComposeMissing is returned when neither form of Compose is installed.
//
// It names both, because the fix depends on which one the user expected to
// have: someone who installed the standalone binary and someone who expected
// the bundled plugin are two different situations, and "compose not found"
// tells neither of them what to do.
var ErrComposeMissing = errors.New(
	"compose was not found: neither the docker compose plugin nor a docker-compose binary is installed")

// DockerCLIAvailable reports whether the docker binary can be found.
func DockerCLIAvailable() bool {
	_, err := exec.LookPath("docker")
	return err == nil
}

// Run executes argv with stdin, stdout and stderr connected to the terminal,
// waits for it to finish, and then waits for the user to press Enter before
// restoring the TUI.
//
// The pause matters: without it the alternate screen is restored the instant
// the command exits, wiping output the user needed to read — a failed "compose
// up" being the obvious case.
func (r *Runner) Run(argv []string, pauseAfter bool) error {
	if len(argv) == 0 {
		return errors.New("no command to run")
	}
	if _, err := exec.LookPath(argv[0]); err != nil {
		if argv[0] == "docker" {
			return ErrDockerCLIMissing
		}
		return fmt.Errorf("%s: not found on PATH", argv[0])
	}

	if r.Suspend != nil {
		if err := r.Suspend(); err != nil {
			return fmt.Errorf("suspend terminal: %w", err)
		}
	}
	// Restoring the TUI must happen whatever the child did, including
	// when it panicked the terminal into a strange state.
	defer func() {
		if r.Resume != nil {
			_ = r.Resume()
		}
	}()

	fmt.Fprintf(os.Stdout, "\r\n\x1b[1m$ %s\x1b[0m\r\n\r\n", strings.Join(argv, " "))

	command := exec.Command(argv[0], argv[1:]...)
	command.Stdin = os.Stdin
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	runErr := command.Run()

	if runErr != nil {
		fmt.Fprintf(os.Stdout, "\r\n\x1b[31mcommand failed: %v\x1b[0m\r\n", runErr)
		if hint := explain(runErr); hint != "" {
			fmt.Fprintf(os.Stdout, "\x1b[2m%s\x1b[0m\r\n", hint)
		}
	}
	if pauseAfter {
		fmt.Fprint(os.Stdout, "\r\n\x1b[2mPress Enter to return to muelle\x1b[0m")
		bufio.NewReader(os.Stdin).ReadString('\n')
	}
	return runErr
}

// Capture runs argv and returns what it wrote, without handing over the
// terminal.
//
// Run exists for commands the user watches; this exists for commands muelle
// reads. Validating a compose file is the case that needs it: the point is to
// show Compose's complaint inside the TUI, and suspending the interface to run
// something that prints one line would be a flicker for nothing.
//
// Output is combined, because a command that fails says why on stderr and a
// command that succeeds says so on stdout.
func (r *Runner) Capture(argv []string) (string, error) {
	if len(argv) == 0 {
		return "", errors.New("no command to run")
	}
	out, err := exec.Command(argv[0], argv[1:]...).CombinedOutput()
	return string(out), err
}

// notExecutable is the status docker returns when the thing asked for is not
// runnable inside the container — most often because the image does not
// contain it.
const notExecutable = 126

// explain turns an exit status into something worth reading.
//
// A container with no shell answers a request for one with an OCI runtime
// error, which describes the mechanism rather than the situation. Images built
// from scratch or on a distroless base contain no shell at all, and there is
// nothing muelle can do about that — but saying so is considerably more use
// than repeating the runtime's complaint.
func explain(err error) string {
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != notExecutable {
		return ""
	}
	return "That command is not present in this container. " +
		"Images built from scratch or on a distroless base carry no shell, " +
		"in which case there is nothing to exec into — read the logs instead."
}

// ExecArgv builds the argv for running a command inside a container.
//
// Interactive by default (-it): the point of this feature is a usable prompt.
// Non-interactive one-shots would need their own flag, and none of the
// suggested commands are one-shots.
func ExecArgv(containerID string, command []string) []string {
	argv := []string{"docker", "exec", "-it", containerID}
	return append(argv, command...)
}
