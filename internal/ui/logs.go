package ui

import (
	"context"
	"strings"
	"time"

	"github.com/RchrdHndrcks/muelle/internal/compose"
	"github.com/RchrdHndrcks/muelle/internal/docker"
)

// logBatchInterval is how long streamed lines are gathered before being handed
// to the event loop. Posting every line individually would wake the loop —
// and redraw the screen — once per line, which a busy container makes
// unusable.
const logBatchInterval = 100 * time.Millisecond

// openLogs starts following a container's logs and switches to the viewer.
func (a *App) openLogs(ctx context.Context, container docker.Container) {
	a.closeLogs()

	a.mode = ModeLogs
	a.logTitle = container.Name()
	a.logs.Reset()
	a.logPager = NewPager(true)
	a.logFilter = ""
	a.logStreamed = false
	a.logToken++
	token := a.logToken

	streamCtx, cancel := context.WithCancel(ctx)
	a.logCancel = cancel

	go a.streamLogs(streamCtx, token, container)
}

// streamLogs follows one container's output, batching lines to the event loop.
func (a *App) streamLogs(ctx context.Context, token int, container docker.Container) {
	// The TTY flag decides whether the stream is multiplexed. Reading it
	// wrong fills the viewer with binary framing bytes, so it comes from
	// inspect rather than a guess.
	tty := false
	if detail, err := a.docker.Inspect(ctx, container.ID); err == nil {
		tty = detail.Config.Tty
	}

	scanner, err := a.docker.Logs(ctx, container.ID, docker.LogOptions{
		Follow: true,
		Tail:   a.config.LogTail,
		TTY:    tty,
	})
	if err != nil {
		a.post(logFailed{token: token, err: err})
		return
	}
	defer scanner.Close()

	// Closing the stream is what unblocks the scanner when the user leaves
	// the viewer; without this the goroutine would live until the container
	// stopped.
	go func() {
		<-ctx.Done()
		scanner.Close()
	}()

	a.pumpLines(ctx, token, scanner, "")
}

// pumpLines drains a scanner into batched events until the stream ends.
//
// prefix labels lines when several containers share one viewer, as the Compose
// project log view does.
func (a *App) pumpLines(ctx context.Context, token int, scanner *docker.LogScanner, prefix string) {
	batch := make([]docker.LogLine, 0, 64)
	ticker := time.NewTicker(logBatchInterval)
	defer ticker.Stop()

	lines := make(chan docker.LogLine, 256)
	go func() {
		defer close(lines)
		for scanner.Scan() {
			line := scanner.Line()
			if prefix != "" {
				line.Text = prefix + " " + line.Text
			}
			select {
			case lines <- line:
			case <-ctx.Done():
				return
			}
		}
	}()

	flush := func() {
		if len(batch) == 0 {
			return
		}
		a.post(logLines{token: token, lines: batch})
		batch = make([]docker.LogLine, 0, 64)
	}

	for {
		select {
		case <-ctx.Done():
			return
		case line, ok := <-lines:
			if !ok {
				flush()
				if err := scanner.Err(); err != nil {
					a.post(logFailed{token: token, err: err})
				}
				return
			}
			batch = append(batch, line)
			if len(batch) >= 256 {
				flush()
			}
		case <-ticker.C:
			flush()
		}
	}
}

// openProjectLogs follows every container in a Compose project at once, with
// each line labelled by its service.
func (a *App) openProjectLogs(ctx context.Context, project compose.Project) {
	running := make([]docker.Container, 0, len(project.Containers))
	for _, container := range project.Containers {
		if container.Running() {
			running = append(running, container)
		}
	}
	if len(running) == 0 {
		a.setError("no running containers in %s", project.Name)
		return
	}

	a.closeLogs()
	a.mode = ModeLogs
	a.logTitle = project.Name + " (" + Plural(len(running), "service", "services") + ")"
	a.logs.Reset()
	a.logPager = NewPager(true)
	a.logFilter = ""
	a.logStreamed = false
	a.logToken++
	token := a.logToken

	streamCtx, cancel := context.WithCancel(ctx)
	a.logCancel = cancel

	for _, container := range running {
		label := container.Service()
		if label == "" {
			label = container.Name()
		}
		go func(container docker.Container, label string) {
			tty := false
			if detail, err := a.docker.Inspect(streamCtx, container.ID); err == nil {
				tty = detail.Config.Tty
			}
			scanner, err := a.docker.Logs(streamCtx, container.ID, docker.LogOptions{
				Follow: true,
				Tail:   a.config.LogTail / max(len(running), 1),
				TTY:    tty,
			})
			if err != nil {
				a.post(logFailed{token: token, err: err})
				return
			}
			defer scanner.Close()
			go func() {
				<-streamCtx.Done()
				scanner.Close()
			}()
			a.pumpLines(streamCtx, token, scanner, "["+label+"]")
		}(container, label)
	}
}

// closeLogs stops any active stream and returns to the list.
//
// Bumping the token first means lines already in flight are discarded on
// arrival, so they cannot leak into the next container's output.
func (a *App) closeLogs() {
	a.logToken++
	if a.logCancel != nil {
		a.logCancel()
		a.logCancel = nil
	}
	if a.mode == ModeLogs {
		a.mode = ModeList
	}
}

// openInspect fetches a container's full inspect payload and shows it.
func (a *App) openInspect(ctx context.Context, container docker.Container) {
	go func() {
		fetchCtx, cancel := context.WithTimeout(ctx, refreshTimeout)
		defer cancel()

		raw, err := a.docker.InspectRaw(fetchCtx, container.ID)
		if err != nil {
			a.post(inspectLoaded{err: err})
			return
		}
		a.post(inspectLoaded{
			title: container.Name(),
			lines: a.highlightJSON(strings.Split(string(raw), "\n")),
		})
	}()
}

// highlightJSON applies light syntax colouring to inspect output: keys in one
// colour, string values in another. Enough structure to scan quickly, without
// a JSON parser.
func (a *App) highlightJSON(lines []string) []string {
	style := a.screen.Style
	out := make([]string, 0, len(lines))

	for _, line := range lines {
		key, rest, found := strings.Cut(line, ":")
		if !found || !strings.Contains(key, `"`) {
			out = append(out, style(styleMuted, line))
			continue
		}
		out = append(out, style(styleAccent, key)+":"+style(styleSuccess, rest))
	}
	return out
}
