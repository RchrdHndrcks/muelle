package ui

import (
	"strings"
	"testing"

	"github.com/RchrdHndrcks/muelle/internal/docker"
	"github.com/RchrdHndrcks/muelle/internal/tui"
)

func sampleProcesses() docker.Processes {
	return docker.Processes{
		Titles: []string{"UID", "PID", "PPID", "C", "STIME", "TTY", "TIME", "CMD"},
		Rows: [][]string{
			{"999", "1722060", "1722040", "0", "Jul15", "?", "02:52:32", "mysqld"},
			{"root", "1722100", "1722060", "0", "Jul15", "?", "00:00:01", "/bin/sh -c entrypoint"},
		},
	}
}

func TestRenderProcessesShowsCommands(t *testing.T) {
	app := newTestApp(t)
	app.processes = sampleProcesses()
	app.mode = ModeProcesses

	rendered := strings.Join(app.renderProcesses(120, 20), "\n")

	if !strings.Contains(rendered, "mysqld") {
		t.Errorf("process table is missing the command:\n%s", rendered)
	}
	if !strings.Contains(rendered, "PID") {
		t.Errorf("process table is missing its headings:\n%s", rendered)
	}
}

// The daemon's column set depends on the host's ps, so the layout must come
// from what it returned rather than a fixed list.
func TestProcessColumnsFollowTheDaemonsTitles(t *testing.T) {
	processes := docker.Processes{
		Titles: []string{"PID", "USER", "COMMAND"},
		Rows:   [][]string{{"1", "root", "nginx"}},
	}

	columns := processColumnWidths(processes, 100)

	if len(columns) != 3 {
		t.Fatalf("got %d columns, want one per reported title", len(columns))
	}
	if columns[2].Title != "COMMAND" || !columns[2].Flex {
		t.Errorf("got %+v, want the command column to flex", columns[2])
	}
}

// Numeric columns should not reserve space for a heading wider than any value.
func TestProcessColumnsSizeToContent(t *testing.T) {
	processes := docker.Processes{
		Titles: []string{"C", "CMD"},
		Rows:   [][]string{{"0", "a-much-longer-command"}},
	}

	columns := processColumnWidths(processes, 100)

	if columns[0].Width != 1 {
		t.Errorf("got width %d for a single-character column, want 1", columns[0].Width)
	}
}

// Without a recognised command column the last one should still absorb slack,
// or the row ends in a gap.
func TestProcessColumnsFlexWhenNoCommandColumn(t *testing.T) {
	processes := docker.Processes{
		Titles: []string{"PID", "STAT"},
		Rows:   [][]string{{"1", "Ss"}},
	}

	columns := processColumnWidths(processes, 100)

	if !columns[len(columns)-1].Flex {
		t.Error("the last column should flex when there is no command column")
	}
}

func TestRenderProcessesHandlesEmptyTable(t *testing.T) {
	app := newTestApp(t)
	app.processes = docker.Processes{}

	rendered := strings.Join(app.renderProcesses(80, 10), "\n")

	if !strings.Contains(rendered, "No processes") {
		t.Errorf("got %q, want an empty state", rendered)
	}
}

func TestProcessRowsFitTheWidth(t *testing.T) {
	app := newTestApp(t)
	app.processes = sampleProcesses()

	for _, width := range []int{40, 80, 200} {
		for _, line := range app.renderProcesses(width, 20) {
			if tui.VisibleWidth(line) > width {
				t.Errorf("at width %d a row overflowed: %q", width, line)
			}
		}
	}
}

func TestEscapeLeavesTheProcessView(t *testing.T) {
	app := loadedApp(t)
	app.mode = ModeProcesses

	press(app, typeKey(tui.KeyEscape))

	if app.mode != ModeList {
		t.Error("Escape should return to the list")
	}
}

func TestProcessViewScrolls(t *testing.T) {
	app := loadedApp(t)
	app.mode = ModeProcesses
	app.processes = docker.Processes{
		Titles: []string{"PID", "CMD"},
	}
	for i := range 100 {
		app.processes.Rows = append(app.processes.Rows, []string{"1", string(rune('a' + i%26))})
	}

	press(app, runeKey('G'))

	if app.processesPager.Offset() == 0 {
		t.Error("G should scroll to the bottom of a long table")
	}
}

// A stopped container has no process table; the daemon's error is a worse way
// to learn that than a plain message.
func TestProcessesRefusedForStoppedContainer(t *testing.T) {
	app := loadedApp(t)
	app.filter = "shop-db" // the exited container

	press(app, runeKey('T'))

	if app.mode == ModeProcesses {
		t.Error("the process view should not open for a stopped container")
	}
	if !app.status.isError {
		t.Error("the refusal should be explained")
	}
}

func TestProcessSummaryCountsRows(t *testing.T) {
	app := newTestApp(t)
	app.processesTitle = "web"
	app.processes = sampleProcesses()

	if got := app.processSummary(); !strings.Contains(got, "2 processes") {
		t.Errorf("got %q, want the process count", got)
	}
}

func TestCommandOfFindsEitherColumnName(t *testing.T) {
	cmd := docker.Processes{Titles: []string{"PID", "CMD"}}
	command := docker.Processes{Titles: []string{"PID", "COMMAND"}}

	if got := commandOf(cmd, []string{"1", "nginx"}); got != "nginx" {
		t.Errorf("got %q, want the CMD column", got)
	}
	if got := commandOf(command, []string{"1", "nginx"}); got != "nginx" {
		t.Errorf("got %q, want the COMMAND column", got)
	}
	if got := commandOf(docker.Processes{Titles: []string{"PID"}}, []string{"1"}); got != "" {
		t.Errorf("got %q, want empty when no command column exists", got)
	}
}
