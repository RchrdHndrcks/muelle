# muelle — Terminal UI for Docker

**Date:** 2026-07-27
**Status:** Approved (design decisions delegated to implementer)

## Purpose

A terminal UI to manage Docker containers and Compose projects on a single
host: see what is running, read logs, and get a shell (or a `mysql` client)
into a container without remembering flags. Think Dokploy, minus the web
server, the database, and the multi-host orchestration.

The target user runs a handful of Compose stacks on one machine (a home
server, a VPS) and reaches them over SSH.

## Non-goals

- Multi-host / cluster orchestration, deploy pipelines, or a web UI.
- Building images or editing Compose files.
- Swarm, Kubernetes, or registry management.
- Windows support (`npipe://`). Unix sockets and TCP only.

## Constraints

- **Zero third-party dependencies.** `go.mod` has no `require` block. The
  standard library covers everything needed; anything it does not cover is
  either out of scope or delegated to the `docker` CLI.
- Go 1.25+.
- Must degrade gracefully: no Docker daemon, no `docker` CLI, no colour
  (`NO_COLOR`), or a 60x20 terminal must all produce something usable rather
  than a panic.

## Key decisions

### 1. Talk to the Docker Engine API directly, over the raw socket

The Engine API is plain HTTP with a JSON body; the only unusual part is the
transport (a Unix socket). `net/http` supports that in six lines:

```go
&http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
    return (&net.Dialer{}).DialContext(ctx, "unix", socketPath)
}}
```

So the Docker Go SDK — which would pull in ~40 transitive modules — buys
nothing. We hand-roll the four endpoints we use and get a `Client` that is
trivially testable against `httptest.NewServer`.

**Host resolution order**, first hit wins:

1. `DOCKER_HOST` env (`unix://`, `tcp://`, `http://`).
2. Probe well-known socket paths: `/var/run/docker.sock`,
   `$HOME/.docker/run/docker.sock` (Docker Desktop),
   `$HOME/.colima/default/docker.sock` (Colima),
   `$XDG_RUNTIME_DIR/docker.sock` (rootless).
3. `docker context inspect --format '{{.Endpoints.docker.Host}}'`, if the CLI
   is present.

Step 3 matters: on the development machine the daemon is Colima, and only the
CLI context knows where it lives. Probing alone would work here but breaks for
custom contexts, so the CLI is the backstop, not the primary path.

**Endpoints used:** `/containers/json`, `/containers/{id}/json`,
`/containers/{id}/{start,stop,restart,kill,pause,unpause}`,
`DELETE /containers/{id}`, `/containers/{id}/logs`, `/containers/{id}/stats`,
`/images/json`, `/volumes`, prune endpoints, `/version`.

Requests are sent unversioned (`GET /containers/json`); the daemon serves
these with its newest API version, and every field we read has been stable
since API 1.24 (2016).

### 2. Raw terminal mode via `syscall`, not `golang.org/x/term`

`x/term` is a thin wrapper over two ioctls. We issue them ourselves through
`syscall.Syscall6(SYS_IOCTL, ...)`, in build-tagged files supplying the
per-OS constants:

| | darwin | linux |
|---|---|---|
| get | `TIOCGETA` | `TCGETS` |
| set | `TIOCSETA` | `TCSETS` |

`syscall.Termios` field widths differ between the two (`uint64` vs `uint32`),
but flag manipulation uses untyped constants, so the shared code compiles
against both without conversion. Window size comes from `TIOCGWINSZ`, and
`SIGWINCH` via `os/signal` triggers a re-measure.

This was validated with a spike before committing to the approach: on
darwin/arm64 the ioctl dispatches correctly and returns `ENOTTY` when stdin is
not a terminal, which is exactly the error path we want.

### 3. Rendering: immediate mode, one write per frame

Views return `[]string` where each string is a fully-styled terminal line.
Each frame the app positions the cursor at home, emits every line followed by
`ESC[K` (erase to end of line), and wraps the whole thing in synchronized
output markers (`ESC[?2026h` / `ESC[?2026l`). One `Write` per frame, no
double-buffer diffing. At 60 lines x 200 cols this is a few KB — far below the
point where diffing pays for itself, and it eliminates a whole class of
stale-cell bugs.

Truncation is the subtle part: lines carry SGR escapes, so cutting at byte or
rune offset can slice an escape sequence in half and corrupt the rest of the
screen. A small ANSI-aware scanner computes visible width and truncates on
cell boundaries, re-emitting a reset. This is pure and gets direct unit tests.

### 4. Delegate interactive sessions to the `docker` CLI

Attaching a terminal to `docker exec` over the API means hijacking the HTTP
connection, proxying raw bytes bidirectionally, and forwarding `SIGWINCH` as
resize API calls. That is the highest-risk code in the project and produces an
experience identical to what the CLI already does correctly.

Instead, for exec and Compose lifecycle commands the app: leaves the alternate
screen, restores cooked mode, runs the child with inherited stdio, waits, then
re-enters raw mode and redraws. The TUI is a launcher; the CLI does the
session.

Consequence: exec and Compose actions require the `docker` binary on `PATH`.
Everything else — the entire read path, container lifecycle, logs, stats —
works with only a reachable socket. The status bar reports the CLI as missing
rather than failing at the moment of use.

### 5. Quick commands: read credentials out of the container

The motivating annoyance is opening a MySQL shell. Doing it by hand means
recalling both the client invocation and the password that was set at `docker
run` time. But the password is right there in the container's
`Config.Env`, and `/containers/{id}/json` returns it.

So a pure function maps image name + env to candidate commands:

| image matches | command |
|---|---|
| `mysql`, `mariadb`, `percona` | `mysql -u root -p<MYSQL_ROOT_PASSWORD>` |
| `postgres`, `pgvector`, `timescale` | `psql -U <POSTGRES_USER> -d <POSTGRES_DB>` |
| `redis`, `valkey` | `redis-cli` (`-a <pass>` when set) |
| `mongo` | `mongosh` (with `-u/-p` when set) |
| `rabbitmq` | `rabbitmqctl status` |
| `node` | `node` |
| `python` | `python3` |
| *(always)* | `bash`, `sh` |

The list is offered in a menu; `bash`/`sh` are always appended so the fallback
is one keystroke away. Matching is substring-based on the image reference,
which covers registry prefixes and tags (`docker.io/library/mysql:8.0`).

### 6. Compose projects come from container labels, plus a directory scan

Compose stamps every container it creates with `com.docker.compose.project`,
`.project.working_dir`, `.project.config_files`, and `.service`. Grouping the
container list by that label reconstructs the project view for free, with no
Compose file parsing.

That only finds projects with at least one existing container, so a fully
stopped stack is invisible. To fix it, configured directories are scanned one
level deep for `docker-compose.y[a]ml` / `compose.y[a]ml`, and those results
merge with the label-derived ones (label data wins on conflict, since it
reflects what the daemon actually has). The default scan root is `~/deployments`, a
common layout for a directory of self-hosted stacks; it is configurable.

Project actions build argv explicitly —
`docker compose -f <config> --project-directory <dir> up -d` — rather than
relying on the working directory, so they behave the same regardless of where
`muelle` was launched.

### 7. Logs: stream, ring-buffer, and demultiplex

`/containers/{id}/logs?follow=1` returns a stream. For containers without a
TTY it is *multiplexed*: 8-byte frame headers carry the stream id (stdout vs
stderr) and payload length. The header must be stripped or the log text is
peppered with binary garbage — so an `io.Reader` wrapper does the demuxing,
selected based on the container's `Config.Tty` from inspect.

Lines land in a fixed-capacity ring buffer (5000 lines) so a chatty container
cannot grow memory without bound. The pager supports follow mode (auto-tail,
toggleable — scrolling up drops out of follow, as a pager should), substring
filtering, and soft wrapping.

## Architecture

```
cmd/muelle              flag parsing, terminal setup/teardown, signal wiring
internal/docker         Engine API client, log demux, stats math
internal/compose        project discovery (labels + dir scan), argv builders
internal/quickcmd       image+env -> candidate exec commands
internal/tui            raw mode, window size, ANSI screen writer, key decoder
internal/ui             app model, event loop, views, overlays, formatting
internal/config         JSON config load/save with defaults
```

Dependencies point one way: `ui` -> {`docker`, `compose`, `quickcmd`, `tui`,
`config`}. Nothing below `ui` knows the UI exists, so every lower package is
testable in isolation without a terminal.

### Event loop

A single goroutine owns all mutable state; everything else communicates over
channels. This is Go's usual answer to shared-state UI and it means no mutexes
in the model.

```
for {
    select {
    case k := <-keys:      // stdin decoder goroutine
    case <-resize:         // SIGWINCH
    case <-tick:           // periodic refresh trigger
    case ev := <-events:   // async results: container list, stats, log lines, errors
    }
    draw()
}
```

Fetches run in their own goroutines and deliver results as events, so a slow
or hung daemon never freezes input — the UI stays responsive and shows a stale
frame with an error in the status bar. An in-flight flag keeps a slow refresh
from stacking up behind the ticker.

### Views

Four top-level views (`1`-`4` / `Tab`): Containers, Compose, Images, Volumes.
Two full-screen pagers (Logs, Inspect) and modal overlays (confirm, menu,
filter input, help) layer on top. Destructive actions — stop, kill, remove,
prune, Compose `down` — always route through a confirm overlay.

Keys follow vim/`k9s` convention: `j`/`k` + arrows, `g`/`G`, `Ctrl-d`/`Ctrl-u`,
`/` to filter, `Esc` to back out, `q` to quit, `?` for help. Lifecycle keys sit
on unshifted letters where safe (`s` start, `t` stop, `r` restart) and shifted
where destructive (`K` kill, `D` remove), keeping `k` free for navigation.

## Testing

Plain `testing`, no assertion library — consistent with the zero-dependency
rule.

| Package | Covered by tests |
|---|---|
| `docker` | host resolution precedence, URL/query construction, JSON decode against `httptest`, log demux (incl. split reads and unknown stream ids), CPU/memory math, error mapping |
| `compose` | label grouping, dir scan, merge precedence, argv construction |
| `quickcmd` | per-image suggestions, env credential extraction, ordering, fallbacks |
| `tui` | ANSI-aware width/truncate, key decoding (escape sequences, UTF-8, ctrl) |
| `ui` | formatting helpers, selection clamping, filtering, log wrap/filter, full-frame golden render |
| `config` | defaults, `~` expansion, malformed file handling |

Terminal-owning code (`MakeRaw`, `Restore`, `Size`) is not unit-tested — it is
ioctl plumbing with no logic. It was validated by spike and is exercised by
manual run.

**Verification without a TTY.** The sandbox this is built in has no
controlling terminal, so the interactive app cannot be driven there. `muelle
-dump` renders exactly one frame at a fixed 120x40 to stdout and exits,
sharing the full model and render path with the interactive app. It is both
the end-to-end check against a live daemon and a golden-test surface.

## Error handling

- **Daemon unreachable at startup:** print the resolved host, the underlying
  error, and the probed paths, then exit non-zero. No empty TUI.
- **Daemon fails mid-session:** keep the last good frame, show the error in the
  status bar, keep retrying on the ticker. Recovery is automatic.
- **Action fails** (e.g. removing a running container): status bar message,
  model untouched. No overlay to dismiss for a recoverable error.
- **Panic while in raw mode:** deferred restore in `main` runs on the panic
  path, so the terminal is never left unusable. This is the one bug class that
  outlives the process, so it gets the belt-and-braces treatment.
