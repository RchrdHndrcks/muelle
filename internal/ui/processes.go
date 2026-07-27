package ui

import (
	"context"
	"strings"

	"github.com/RchrdHndrcks/muelle/internal/docker"
	"github.com/RchrdHndrcks/muelle/internal/tui"
)

// processesLoaded carries a container's process table.
type processesLoaded struct {
	title     string
	processes docker.Processes
	err       error
}

func (e processesLoaded) apply(a *App) {
	if e.err != nil {
		a.setError("processes: %v", e.err)
		return
	}
	a.processes = e.processes
	a.processesTitle = e.title
	a.processesPager = NewPager(false)
	a.mode = ModeProcesses
}

// openProcesses fetches and shows what is running inside a container.
//
// This answers a question the container list cannot: a container can be up,
// healthy and responding to nothing, because the process that matters died
// while the entrypoint stayed alive.
func (a *App) openProcesses(ctx context.Context, container docker.Container) {
	if !container.Running() {
		// A stopped container has no process table; the daemon returns an
		// error, which is a worse way to learn this than a plain message.
		a.setError("%s is not running", container.Name())
		return
	}

	go func() {
		fetchCtx, cancel := context.WithTimeout(ctx, refreshTimeout)
		defer cancel()

		processes, err := a.docker.Top(fetchCtx, container.ID, "")
		a.post(processesLoaded{title: container.Name(), processes: processes, err: err})
	}()
}

// processColumnWidths sizes the process table.
//
// The daemon's column set depends on the host's ps, so the layout is derived
// from whatever it returned rather than from a fixed list. The command column
// flexes because it is both the widest and the one worth reading.
func processColumnWidths(processes docker.Processes, width int) []Column {
	columns := make([]Column, 0, len(processes.Titles))

	for i, title := range processes.Titles {
		// Measure the content so numeric columns stay narrow instead of
		// reserving space for a heading that is wider than any value.
		size := tui.VisibleWidth(title)
		for _, row := range processes.Rows {
			if i < len(row) {
				if cell := tui.VisibleWidth(row[i]); cell > size {
					size = cell
				}
			}
		}
		// The command is normally last and always the longest.
		flex := title == "CMD" || title == "COMMAND"
		if flex && size > 40 {
			size = 40
		}
		columns = append(columns, Column{Title: title, Width: size, Flex: flex})
	}

	// If the daemon reported no command column, let the last one absorb the
	// slack so the row does not end in a gap.
	if len(columns) > 0 && !hasFlex(columns) {
		columns[len(columns)-1].Flex = true
	}
	return columns
}

func hasFlex(columns []Column) bool {
	for _, c := range columns {
		if c.Flex {
			return true
		}
	}
	return false
}

// renderProcesses draws the process table.
func (a *App) renderProcesses(width, height int) []string {
	style := a.screen.Style

	if len(a.processes.Rows) == 0 {
		return a.emptyState(width, height, "No processes reported.")
	}

	columns := processColumnWidths(a.processes, width)
	widths := LayoutColumns(columns, width)

	lines := []string{style(styleColumn, RenderHeader(columns, widths))}

	start, end := a.processesPager.Window(len(a.processes.Rows), height-1)
	for _, row := range a.processes.Rows[start:end] {
		cells := make([]string, len(row))
		copy(cells, row)
		// Dim everything but the command, so the eye lands on what is
		// actually running rather than on a wall of numbers.
		for i := range cells {
			if i < len(columns) && !columns[i].Flex {
				cells[i] = style(styleMuted, cells[i])
			}
		}
		lines = append(lines, RenderRow(cells, widths))
	}
	return lines
}

// handleProcessesKey handles the process view's keys.
func (a *App) handleProcessesKey(key tui.Key) bool {
	_, height := a.screen.Size()
	viewport := height - 3
	total := len(a.processes.Rows)

	switch {
	case key.Type == tui.KeyEscape, key.IsRune('q'):
		a.mode = ModeList
	case key.Type == tui.KeyDown, key.IsRune('j'):
		a.processesPager.ScrollBy(1, total, viewport)
	case key.Type == tui.KeyUp, key.IsRune('k'):
		a.processesPager.ScrollBy(-1, total, viewport)
	case key.Type == tui.KeyPageDown, key.Type == tui.KeyCtrlD:
		a.processesPager.ScrollBy(viewport/2, total, viewport)
	case key.Type == tui.KeyPageUp, key.Type == tui.KeyCtrlU:
		a.processesPager.ScrollBy(-viewport/2, total, viewport)
	case key.IsRune('g'), key.Type == tui.KeyHome:
		a.processesPager.ScrollToTop()
	case key.IsRune('G'), key.Type == tui.KeyEnd:
		a.processesPager.ScrollToBottom(total, viewport)
	default:
		return false
	}
	return true
}

// processSummary describes the table for the status bar.
func (a *App) processSummary() string {
	return a.processesTitle + "  " + Plural(len(a.processes.Rows), "process", "processes")
}

// commandOf returns a row's command column, for tests and summaries.
func commandOf(processes docker.Processes, row []string) string {
	index := processes.Column("CMD")
	if index < 0 {
		index = processes.Column("COMMAND")
	}
	if index < 0 || index >= len(row) {
		return ""
	}
	return strings.TrimSpace(row[index])
}
