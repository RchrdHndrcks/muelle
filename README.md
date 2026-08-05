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
 NAME              STATE    CPU     MEM       IMAGE              PORTS                AGE    UPTIME
 shop-api          up       0.7%    383MiB    shop/api:1.4       8080->8080/tcp       3d     2m
 shop-db           up       0.8%    747MiB    mysql:8.0          3306->3306/tcp       3d     3d
 shop-cache        up       0.1%    12MiB     redis:7-alpine     6379->6379/tcp       3d     3d
 blog-web          exited   -       -         blog/web:2.1                            9d     -
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

Requires Go 1.25+, a reachable Docker daemon, and — for exec actions only —
the `docker` CLI on `PATH`. Compose actions additionally need Compose itself,
in either of its forms: the `docker compose` plugin or a standalone
`docker-compose` binary.

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
| `s` `t` | start / stop |
| `r` | restart, or recreate to apply an edited compose file |
| `p` | pause or unpause |
| `K` | kill (SIGKILL) |
| `D` | remove |
| `Space` | mark for a bulk action; on a group heading, the whole group |
| `a` | include stopped containers |
| `A` | group the list by application |
| `z` | fold an application away (also `Enter` on a heading) |
| `P` | prune (images and volumes views) |
| `S` | toggle the host metrics panel |
| `Ctrl-r` | refresh now |
| `q`, `Ctrl-c` | quit |

Destructive actions always ask first, and a destructive prompt will not accept
a stray `Enter` — it wants an explicit `y`.

While any containers are marked, `s`, `t`, `K` and `D` act on all of them at
once: kill and remove still confirm, stating the count ("Remove 4 containers?
This cannot be undone."), and one status line reports the aggregate outcome.
Marks follow the container, not its row, so they survive a refresh. `Esc`
clears the marks first and the filter second.

In the log viewer: `f` follow, `w` wrap, `t` timestamps, `F` formatting,
`/` filter, `c` clear. Scrolling up pauses follow; scrolling back to the
bottom resumes it.

## Log formatting

Structured logs are written for programs to read. A Go service logging through
`log/slog` spends ninety characters of JSON to say three words:

```
{"time":"2026-07-27T20:55:34.503644157Z","level":"INFO","msg":"user connected","user_id":"263924de","count":1}
```

The log viewer takes that apart, and lines up whatever it can:

```
20:55:34 INFO  user connected user_id=263924de count=1
20:55:35 ERROR send failed attempt=3 err="connection refused"
20:44:05 WARN  [MY-011810] [Server] Insecure configuration for --pid-file
14:13:48 INFO  [1] LOG:  database system is ready to accept connections
               classifier ready: models loaded
```

`F` switches back to exactly what the container wrote, for when the question is
about the log format itself.

Recognised: `slog`, `logrus`, `zap`, `zerolog`, `bunyan` and `pino` field
names, timestamps written as strings or epoch seconds, and the severity
conventions of MySQL, PostgreSQL, nginx and the Go runtime.

Unstructured output is not reformatted, only scanned for a severity and a
leading timestamp. Text whose layout was deliberate is left alone.

Severity detection is deliberately conservative: only the first 64 characters
are searched, and only for a bracketed, colon-suffixed or upper-case token. A
message that merely mentions the word "error" is not an error, and colouring
that would make the colouring untrustworthy — being trustworthy at a glance is
its only job.

For the same reason a parsed severity outranks the stream. Plenty of programs
write their entire log to stderr, so treating the stream as a severity paints a
healthy container's whole output red.

HTTP verbs in a message are coloured apart, on a scale that runs from cold to
warm with how much the request can change: `GET` green, `HEAD` blue, `OPTIONS`
grey, `POST` orange, `PUT` yellow, `PATCH` violet, `DELETE` red. Whole
upper-case words only, so `TARGET` is not a `GET`. A line already painted red
for coming from stderr keeps that colour instead: the stream says more than the
verb, and a colour opened inside another one would end it early.

`t`, `w` and `F` are remembered between sessions, the same as the ordering
you choose with `o`.

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

## Grouping by application

Docker has no notion of an application. Compose comes closest, stamping a
project label on everything it creates, but plenty of containers are started
with `docker run` and carry nothing at all — and those are exactly the ones you
still think of as belonging together, because you named them that way.
`shop-db` and `shop-db-replica` are one application to everyone except the
daemon.

`A` gathers the list under one heading per application:

```
NAME                STATE     CPU    MEM      IMAGE
▾ shop              2/2 up    1.2%   1.1GiB
  shop-db           up ✓      0.5%   390MiB   mysql:8.0
  shop-db-replica   up        0.7%   758MiB   mysql:8.0
▾ blog              2/2 up    0.3%   165MiB   compose
  blog-web          up        0.2%   120MiB   blog/web:2.1
  blog-cache        up ✓      0.1%    45MiB   redis:7-alpine
▸ ungrouped         0/28 up
```

An application is read from two places, in this order:

1. **The Compose project**, where there is one. That is you having already said
   which containers belong together, so nothing muelle infers overrides it.
2. **The name**, up to the first hyphen. A prefix needs at least two containers
   to count — one container with a hyphen in its name is not an application,
   and without that rule a host carrying a few dozen one-off containers becomes
   a few dozen one-member groups, which is the flat list again with twice as
   many rows.

Everything else lands in `ungrouped`. Docker's own generated names use
underscores (`agitated_gagarin`), so they never share a prefix and fall there
without needing a rule of their own. A standalone container named after an
existing project joins it: a database started by hand as `shop-cache` belongs
to the `shop` stack rather than beside it.

The heading carries the application's totals, which is the reading grouping
exists to make possible — what a whole application costs, rather than what each
of its parts does. Sorting by `cpu` or `memory` orders the applications by
those totals and their containers within them, so "which application is eating
this host" becomes a question the list answers.

`z` folds an application away, or `Enter` on a heading. Grouping is on by
default; `A` turns it off, and both that and which applications are folded are
remembered between sessions, like the ordering and the log viewer's settings —
collapsing `ungrouped` is something you do once.

Container keys do nothing while a heading is selected. They could act on the
whole group, but a `restart` that touches six containers because the cursor was
one line high is exactly the accident not to build into a tool you run on a
server.

Filtering keeps the heading of any application with matches and opens it if it
was folded, so a search never looks like it found nothing. Which application a
container belongs to is worked out from the whole host rather than from the
filtered list: otherwise searching for one container would dissolve the group
it is in and move the row as you typed.

## Health and restart indicators

The state column carries a healthcheck marker and, for containers that look
unwell, how many times they have restarted:

```
shop-db-1        up ✓          healthcheck passing
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

### Probing an endpoint yourself

Docker's `HEALTHCHECK` runs the test *inside* the container, so the image has
to carry something that can make an HTTP request. Images built on `scratch` or
a distroless base carry no `curl`, no `wget` and no shell — and those are
exactly the images where "is it actually serving?" is hardest to answer by
looking.

A container opts in by naming its endpoint in an environment variable, and
muelle makes the request itself, from the host:

```yaml
services:
  api:
    image: shop/api:1.4
    ports: ["8080:8080"]
    environment:
      MUELLE_HEALTH: /health
```

Four shapes are accepted:

| Value | Meaning |
|---|---|
| `/health` | that path, on the container's only port |
| `8080/health` | that path, on port 8080 inside the container |
| `8080` | `/` on port 8080 |
| `http://x:8080/health` | a full URL; `https` works too |

Anything else is ignored rather than guessed at — a health indicator that is
quietly wrong is worse than none, because it is believed. Naming the port is
required when the container has more than one; muelle will not pick.

Any `2xx` is healthy and everything else is not, including a redirect: a
`/health` that bounces to a login page has not said it is well. Requests are
`GET`, not `HEAD`, because a handler written for Docker's healthcheck may
answer `405` to a `HEAD` it considers perfectly healthy.

muelle probes the **published port on the loopback** first, which is the only
route that works everywhere. A container that publishes nothing is probed at
its own address on the bridge network, which works when muelle runs on the same
Linux host as the daemon — a server over SSH, which is what muelle is for —
but not through Docker Desktop, where the bridge lives inside a VM.

Where a container sets the variable, its verdict replaces the image's own
healthcheck in the state column. Setting it is a deliberate instruction, made
by someone with a reason to want that endpoint asked.

Reading an environment variable needs an inspect call, which muelle otherwise
avoids doing per refresh. It does not have to: a container's environment is
fixed when it is created, and a recreated container arrives with a new ID. Each
container is inspected once, and containers that never set the variable cost
that one call and nothing more. Set `health_probe` to `false` to skip it
entirely.

## Watching a deployment

A deployment does not restart a container, it replaces one: the old container
is destroyed and a new one created under a different ID. Anything keyed on the
container ID loses track precisely when you most want to be told what is going
on — the row vanishes from the list and a different row appears in its place,
with nothing to say the two are the same service.

muelle follows the Compose **service** instead, which is the thing that
persists, and the row says which phase it is in:

```
NAME              STATE             CPU     MEM       AGE
shop-api          recreating 0:03   -       -         3d
shop-worker       starting 0:07     -       -         3d
shop-db           up ✓              0.8%    747MiB    3d
```

The phases are `creating` (a service that had no container), `recreating` (one
being replaced) and `starting` (running, not yet shown to be serving). The
timer is how long it has been in *that* phase, which is the question a row
saying "starting" invites.

There is no percentage, and there cannot be. Docker has no notion of a
container being partly started; it is created, then it runs. The only figure in
the whole sequence that is a genuine fraction is the bytes of an image pull,
and that is known solely to whoever ran the pull — when your CI or a webhook
deploys, muelle is a bystander watching the daemon, and the daemon reports a
pull once, when it has finished.

### Where it comes from

This is the one part of muelle that is not polled, because it cannot be: a
deployment is over in less time than the refresh interval, so polling would
show the aftermath and never the act. muelle follows the daemon's event stream,
which reports container lifecycle as it happens and carries Compose's labels on
every event — so the service each container belonged to is known without
inspecting anything.

Stopping a container is not deploying it. A stop leaves the container in place;
only its removal means something is being replaced, and that is the distinction
muelle keys on. Pressing `t` does not make a row claim a deploy.

A deploy ends when the service says so — a healthcheck reporting in, or a
`MUELLE_HEALTH` endpoint answering. Where there is neither there is nothing to
wait for, so a started container is called up after fifteen seconds. That
window is long enough for a crash-loop to give itself away: a container dying
after three seconds is destroyed and recreated well inside it, so the row
cycles through `recreating` rather than ever settling on `up`. A replacement
that never arrives at all is given up on after a minute.

Because the row is followed by service rather than by container, it stays on
screen through the gap where no container is running — which is exactly the
moment that otherwise makes a deployment invisible.

This is live only. `muelle -dump` renders a single frame and exits, and a
deployment is a thing that happens over time.

## Age and uptime

Two columns, because they answer different questions:

```
shop-api     3d     2m      created three days ago, restarted two minutes ago
shop-db      3d     3d      created and started three days ago, untouched since
blog-web     9d     -       not running, so there is no uptime
```

`AGE` is the container's creation time, the same figure `docker ps` reports as
`CREATED`. It does not move when a container is restarted, because restarting
reuses the container rather than making a new one — which is exactly why a
restart that appeared to do nothing needs a second column to show that it did.

`UPTIME` is the current run. It comes from the status text the daemon already
returns with the container list (`Up 8 weeks`), not from an inspect call per
container, so the column costs no extra requests. The price is the daemon's own
granularity: eight weeks is reported as eight weeks, not to the hour. That is
precise where it matters, since anything recently restarted is measured in
seconds and minutes.

On a narrow terminal `UPTIME` is the first column dropped.

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

Press `Enter` on a project for the action menu (`up -d`, `recreate`, `down`,
`restart`, `pull`, `build`, `ps`, `logs`), or `u` / `d` / `r` directly. `l`
follows every service in the project at once, with each line labelled by
service. `e` edits the project's configuration; see below.

Actions shell out to Compose with the project identified explicitly
(`-f <file> --project-directory <dir> -p <name>`), so they behave the same
regardless of where muelle was started.

Compose ships in two shapes and muelle detects which one is installed at
startup: the `docker compose` CLI plugin where it is present, and the
standalone `docker-compose` binary otherwise. The docker CLI being on PATH
proves nothing about the plugin sitting alongside it — a Homebrew
`docker-compose` with no plugin installed is an ordinary setup — so the plugin
is probed for rather than assumed. The action menu shows the argv it will
actually run, naming whichever binary was found.

### Editing a project

`e` on a project lists the files that define it — every file Compose was
invoked with, plus the project's `.env` — and opens the chosen one in your
editor, with the terminal handed over the way `exec` hands it over.

Files named with `env_file:` are not listed. Compose resolves them into the
rendered configuration and records them in no label, so finding them would
mean parsing YAML, which muelle does not do.

On return, muelle compares the file's contents. If nothing changed, nothing
happens. If something did, it runs `compose config -q` first: that renders the
project in memory without touching a container, so a typo is reported in
milliseconds instead of after half the stack has come down. A rejected file is
shown with Compose's own diagnostic and an offer to go back to the editor.

Once the configuration is valid, muelle offers to apply it with `up -d`, or
with `up -d --force-recreate` for the case where Compose decides nothing
changed and you know better.

The editor is `editor` from the configuration file, then `$VISUAL`, then
`$EDITOR`, then `vi`. The value may carry flags, which is what makes a
graphical editor usable here: `code --wait` works, plain `code` returns before
you have typed anything and muelle sees an unchanged file.

### Restart does not reload configuration

`r` on a container restarts it: the same container, started again. Its
environment, image and ports were fixed when it was created, so a changed
`docker-compose.yml` has no effect and the `AGE` column does not move. This is
Docker's design, not a limitation of muelle — no restart of any kind applies a
configuration change.

Replacing the container is what applies one. Where muelle can do that — the
container carries Compose's labels, Compose is installed, and the project's
files are known — `r` offers both:

```
  shop-api

  › restart     same container, kept as it was created
    recreate    apply the current compose.yml and .env

  esc cancel
```

Recreating one service runs `up -d --no-deps --force-recreate <service>`.
`--no-deps` is the important flag: without it Compose replaces everything the
service `depends_on` as well, turning "restart the API" into an outage of the
database behind it.

Where recreating is not possible, `r` restarts directly and shows no menu.

To apply changes across a whole project instead, `u` (`up -d`) from the Compose
view compares each service against the config hash on its running container and
recreates only the ones that differ — which is why those containers, and only
those, come back with a fresh age.

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
| `sort` | container ordering: `project`, `cpu`, `memory`, `age` or `state`. Updated when you press `o`, so the ordering you chose is the one you come back to |
| `log_timestamps` | timestamps in the log viewer. Updated when you press `t` |
| `log_wrap` | wrapping in the log viewer. Updated when you press `w` |
| `log_format` | structured lines rendered as their parts. Updated when you press `F` |
| `editor` | editor for `e` on a Compose project. Empty leaves the choice to `$VISUAL` and `$EDITOR`, where you have already made it. May carry flags, as in `code --wait` |
| `health_probe` | probe containers that set `MUELLE_HEALTH`. One inspect per container, once each |

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
