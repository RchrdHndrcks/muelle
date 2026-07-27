# muelle

A terminal UI for managing Docker containers and Compose projects on a single
host. See what is running, read logs, and drop into a `mysql` prompt without
looking up the password you set six months ago.

Think Dokploy, minus the web server, the database and the multi-host
orchestration — just a binary you run over SSH.

**Zero dependencies.** `go.mod` has no `require` block. The Docker Engine API
is HTTP over a Unix socket, which `net/http` speaks natively; raw terminal mode
is two ioctls. Nothing else was needed.

```
 muelle [1 Containers]  2 Compose  3 Images  4 Volumes            9/12 up  docker 27.4.0
 NAME              STATE    CPU     MEM       IMAGE              PORTS                AGE
 shop-api          up       0.7%    383MiB    shop/api:1.4       8080->8080/tcp       3d
 shop-db           up       0.8%    747MiB    mysql:8.0          3306->3306/tcp       3d
 shop-cache        up       0.1%    12MiB     redis:7-alpine     6379->6379/tcp       3d
 blog-web          exited   -       -         blog/web:2.1                            9d
 enter inspect  l logs  x exec  s start  t stop  r restart  D remove  a all  / filter  ? help
```

## Install

```sh
go install github.com/RchrdHndrcks/muelle/cmd/muelle@latest
```

`go install` places the binary in `$(go env GOPATH)/bin` — usually `~/go/bin`.
That is **not** the same directory as `/usr/local/go/bin`, which holds the Go
toolchain and is the one most people already have on `PATH`. If the install
succeeds but your shell reports `command not found: muelle`, that is the
reason.

Either put the directory on your `PATH` (this also reveals every other tool
you have installed with `go install`):

```sh
echo 'export PATH="$PATH:$HOME/go/bin"' >> ~/.zshrc && exec zsh
```

Or install straight into a directory already on your `PATH`, with `GOBIN`:

```sh
GOBIN=/usr/local/bin go install github.com/RchrdHndrcks/muelle/cmd/muelle@latest
```

A build tool will not edit your shell configuration for you, so one of these
steps is unavoidable the first time.

Or build from source:

```sh
git clone https://github.com/RchrdHndrcks/muelle
cd muelle
make build        # -> bin/muelle
make cross        # -> linux/amd64 and linux/arm64 binaries for a server
```

Requires Go 1.25+, a reachable Docker daemon, and — for exec and Compose
actions only — the `docker` CLI on `PATH`.

## Use

```sh
muelle                      # start on the containers view
muelle -view compose        # start on the compose view
muelle -all                 # include stopped containers
muelle -host tcp://host:2375
muelle -dump                # render one frame to stdout and exit
```

### Keys

Press `?` in the app for the full reference.

| Key | Action |
|---|---|
| `1` `2` `3` `4` `5`, `Tab`, `←` `→` | containers, compose, images, volumes, networks |
| `j` `k`, `↓` `↑` | move selection |
| `g` `G` | first / last |
| `Ctrl-d` `Ctrl-u` | half page down / up |
| `/` | filter the list |
| `Enter`, `i` | inspect (full JSON) |
| `l` | follow logs |
| `x` | exec menu |
| `e` | shell immediately (`bash`, falling back to `sh`) |
| `s` `t` `r` | start / stop / restart |
| `p` | pause or unpause |
| `K` | kill (SIGKILL) |
| `D` | remove |
| `a` | include stopped containers |
| `P` | prune (images and volumes views) |
| `S` | toggle the host metrics panel |
| `Ctrl-r` | refresh now |
| `q`, `Ctrl-c` | quit |

Destructive actions always ask first, and a destructive prompt will not accept
a stray `Enter` — it wants an explicit `y`.

In the log viewer: `f` follow, `w` wrap, `t` timestamps, `/` filter, `c` clear.
Scrolling up pauses follow; scrolling back to the bottom resumes it.

## Host metrics

A summary of the machine the daemon runs on sits beneath the list, toggled
with `S`:

```
────────────────────────────────────────────────────────────────────────────
 colima · Ubuntu 24.04.1 LTS · linux/aarch64 · docker 27.4.0
 CPU  [█░░░░░░░░░░░░░░░░░░░░░]  1.3% of 2 cores    2 running · 30 stopped
 MEM  [████████████░░░░░░░░░░]  1.1GiB / 1.9GiB    76 images · 75 volumes
 DISK images 10.2GiB · volumes 2.0GiB   7.9GiB reclaimable (17 unused images)
```

With Docker Desktop or Colima this describes the **VM**, which is the boundary
that actually constrains the containers.

The reclaimable figure names where the space is, because the total on its own
is not actionable — build cache is frequently most of it and lives nowhere
else in the interface. `P` prunes it.

CPU and memory are the **sum across running containers**, not host-wide
utilisation — the Docker API reports the host's capacity but never its usage,
so anything running outside Docker is invisible here. The figures are labelled
accordingly rather than presented as a claim the data cannot support.

Disk figures come from the same source as `docker system df`. Image size is
the deduplicated layer total, not the sum of image sizes, which double-counts
every shared base layer. That endpoint walks the storage driver and is slow,
so it refreshes once a minute rather than on every tick.

The panel hides itself in the log and inspect viewers, and on a terminal too
short to leave a usable list behind it.

## Health and restart indicators

The state column carries a healthcheck marker and, for containers that look
unwell, how many times they have restarted:

```
engi-mysql-1     up ✓          healthcheck passing
api-worker       up ✗×12       healthcheck failing, restarted 12 times
queue-consumer   restart ×47   crash-looping
web              up            no healthcheck defined
```

Markers differ by character as well as colour, so the distinction survives
`NO_COLOR` and does not depend on telling red from green.

Health comes from the container list, which appends it to the status text —
free to read. Restart counts need one inspect call each, so they are fetched
only for containers that already look unwell (restarting, unhealthy or dead).
On a healthy host that list is empty and costs nothing.

## Exit codes

A stopped container shows what it exited with, so a clean shutdown is
distinguishable from a kill at a glance:

```
worker      exit 0      stopped cleanly, shown muted
api         exit 137    killed — usually the OOM killer
scraper     exit 255    abnormal termination
```

Selecting a container that failed puts the meaning in the status bar
(`SIGKILL (often out of memory)`). `137` and `143` are both just "stopped" to
the daemon, and the bare number does not say which happened.

## Reclaiming disk

`P` opens a system prune with three scopes, from least to most destructive:

```
▸ prune unused data (about 6.9GiB)      stopped containers, unused networks,
                                        dangling images, build cache
  prune unused data and all unused images
  prune everything including volumes    DATA WILL BE LOST
```

The estimate comes from the same measurement the metrics panel shows, so the
number you are acting on is the one you were looking at.

Volumes are a separate scope rather than a flag, because they are the only
prune target that cannot be rebuilt or re-pulled. Choosing that scope is a
deliberate act, and the confirmation says so.

The Engine API has no single prune endpoint — the CLI calls each object type's
in turn, and so does this, containers first so the images and networks they
held become collectable in the same pass. A failing step is recorded and the
rest still run: a prune that stops halfway would leave you unsure what state
the host is in.

## The exec menu

This is the feature the tool exists for. Press `x` on a container and muelle
reads its image and environment to offer commands that are ready to run:

```
┌────────────────────────────────────────────────────────┐
│ Exec in shop-db                                        │
├────────────────────────────────────────────────────────┤
│ ▸ mysql as root (shop)   mysql -u root -p******** shop │
│   mysql as app (shop)    mysql -u app -p******** shop  │
│   mysqldump              mysqldump -u root -p********  │
│   bash shell             bash                          │
│   sh shell               sh                            │
│                                                        │
│ Enter = run    Esc = cancel                            │
└────────────────────────────────────────────────────────┘
```

The password comes from the container's own environment — the daemon has had
it all along. It is masked on screen and passed to the real command intact.

Recognised images: MySQL / MariaDB / Percona, PostgreSQL (and pgvector,
TimescaleDB, PostGIS), Redis / Valkey, MongoDB, RabbitMQ, nginx, Node, Python.

Every container also gets a `shell` entry, which runs bash where the image has
it and sh otherwise — decided inside the container rather than guessed. Most
images are Alpine- or slim-based and carry no bash at all, so invoking it
directly fails on the majority of them. A container built from scratch or on a
distroless base has no shell whatsoever; muelle says so rather than passing on
the runtime's error.

## Processes inside a container

`T` shows what is actually running inside a container — `docker top` without
leaving the UI:

```
UID   PID      PPID     C  STIME  TTY  TIME      CMD
999   1722060  1722040  0  Jul15  ?    02:52:32  mysqld
root  1722100  1722060  0  Jul15  ?    00:00:01  /bin/sh -c entrypoint
```

A container can be up, healthy, and serving nothing because the process that
mattered died while the entrypoint stayed alive. The container list cannot
show that; this can.

Columns come from whatever the daemon reports, since they depend on the host's
`ps`, and are sized to their contents so the command gets the room.

## Finding images worth deleting

The metrics panel reports how much disk unused images are holding. The images
view says which ones:

```
REPOSITORY:TAG            ID             SIZE      USAGE   CREATED
app:latest                5442b24f3786   27.6MiB   1 used  31m
old-build:latest          79c65f43ee28   309MiB    unused  19d
<none>:<none>             e93e3c66305f   1.2GiB    unused  15d
                                          17 unused · 7.9GiB reclaimable
```

`u` narrows the list to just the candidates, and `D` removes the selected one.

Usage is derived from the container list rather than fetched: the images
endpoint reports `-1` for every image's container count, and only
`docker system df` computes it properly. Cross-referencing the containers we
already have is both free and exact — it produces the same set the expensive
call does.

Stopped containers count as users. A stopped container still pins its image,
and calling it unused would offer a deletion the daemon refuses.

The size shown is the image's total; the reclaimable figure excludes layers
other images still need, so it is what removing them would actually free.

## Compose

The Compose view is built from the labels Compose stamps on its containers, so
projects appear with no configuration. Stopped projects have no containers to
read labels from, so directories listed in `compose_dirs` are also scanned one
level deep for a compose file.

Press `Enter` on a project for the action menu (`up -d`, `down`, `restart`,
`pull`, `build`, `ps`, `logs`), or `u` / `d` / `r` directly. `l` follows every
service in the project at once, with each line labelled by service.

Actions shell out to `docker compose` with the project identified explicitly
(`-f <file> --project-directory <dir> -p <name>`), so they behave the same
regardless of where muelle was started.

## Configuration

Written on first run to `$MUELLE_CONFIG` if set, otherwise
`~/.config/muelle/config.json` on Linux and
`~/Library/Application Support/muelle/config.json` on macOS.

```json
{
  "docker_host": "",
  "compose_dirs": ["~/deployments"],
  "refresh_seconds": 3,
  "log_tail": 500,
  "stats": true,
  "stop_timeout": 10,
  "colour": true
}
```

| Field | Meaning |
|---|---|
| `docker_host` | daemon endpoint; empty means autodetect |
| `compose_dirs` | directories scanned for stopped Compose projects |
| `refresh_seconds` | polling interval, clamped to 1–60 |
| `log_tail` | lines of history loaded when opening logs |
| `stats` | CPU and memory columns; each sample holds a daemon request open for about a second, so turn this off on a busy host or a slow remote socket |
| `stop_timeout` | seconds a container gets to exit before being killed |
| `colour` | styled output; `NO_COLOR` in the environment always wins |

The daemon is found by trying, in order: `DOCKER_HOST`; the well-known socket
paths (`/var/run/docker.sock`, Docker Desktop, Colima, rootless); and finally
`docker context inspect`. Custom contexts only appear in the last of those,
which is why it is checked.

## `-dump`

`muelle -dump` renders one frame to stdout and exits, using the same model and
render path as the interactive app. It needs no terminal, so it works in a
pipe, in CI, or from a script:

```sh
muelle -dump -all
muelle -dump -view compose | grep partial
```

## Design notes

- **No Docker SDK.** The endpoints used are few and stable; the SDK would add
  ~40 transitive modules to save a couple of hundred lines.
- **No TUI framework.** Rendering is immediate mode: views return styled lines,
  and the whole frame is written in one `write` bracketed by synchronized-output
  markers. Truncation is ANSI-aware, because slicing a string mid-escape
  corrupts the rest of the screen.
- **Interactive sessions are delegated.** Attaching a terminal to `docker exec`
  over the API means hijacking the connection and proxying raw bytes. The CLI
  already does that correctly, so muelle suspends itself and runs it.
- **One goroutine owns the state.** Everything asynchronous reports back as an
  event on a channel, so there are no locks on the model and a slow daemon
  never freezes the keyboard.

The full design rationale is in
[`docs/superpowers/specs`](docs/superpowers/specs).

## Development

```sh
make check    # format, vet, and test with the race detector
make test     # tests only
make dump     # run against your daemon without a terminal
```

Supports Linux and macOS. Windows is not supported (`npipe://` is not
implemented).

## Licence

MIT
