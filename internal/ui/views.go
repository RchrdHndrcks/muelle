package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/RchrdHndrcks/muelle/internal/tui"
)

// renderHeader draws the tab bar and daemon summary.
func (a *App) renderHeader(width int) string {
	style := a.screen.Style

	var tabs strings.Builder
	tabs.WriteString(style(tui.Join(tui.StyleBold, tui.Foreground(colourPurple)), " muelle "))
	for view := ViewContainers; view < viewCount; view++ {
		label := fmt.Sprintf(" %d %s ", int(view)+1, view.Title())
		if view == a.view {
			tabs.WriteString(style(styleTabActive, "["+strings.TrimSpace(label)+"]"))
			tabs.WriteString(" ")
			continue
		}
		tabs.WriteString(style(styleTabIdle, label))
	}

	right := a.headerSummary()
	left := tabs.String()
	gap := width - tui.VisibleWidth(left) - tui.VisibleWidth(right)
	if gap < 1 {
		return tui.TruncateEllipsis(left, width)
	}
	return left + strings.Repeat(" ", gap) + right
}

// headerSummary describes the daemon and what is running, on the right of the
// header.
func (a *App) headerSummary() string {
	style := a.screen.Style

	running := 0
	for _, c := range a.containers {
		if c.Running() {
			running++
		}
	}
	summary := fmt.Sprintf("%d/%d up", running, len(a.containers))
	if a.version.Version != "" {
		summary += "  docker " + a.version.Version
	}
	return style(styleMuted, summary+" ")
}

// renderList draws the active view's list.
func (a *App) renderList(width, height int) []string {
	switch a.view {
	case ViewContainers:
		return a.renderContainers(width, height)
	case ViewCompose:
		return a.renderCompose(width, height)
	case ViewImages:
		return a.renderImages(width, height)
	case ViewVolumes:
		return a.renderVolumes(width, height)
	}
	return nil
}

// containerColumns is the container list layout. Name flexes because it is
// what identifies the row; ports and image are the first to be dropped when
// the terminal is narrow.
func (a *App) containerColumns() []Column {
	columns := []Column{
		{Title: "name", Width: 22, Flex: true},
		{Title: "state", Width: 8},
	}
	if a.config.Stats {
		columns = append(columns,
			Column{Title: "cpu", Width: 7},
			Column{Title: "mem", Width: 9},
		)
	}
	return append(columns,
		Column{Title: "image", Width: 24},
		Column{Title: "ports", Width: 20},
		Column{Title: "age", Width: 7},
	)
}

func (a *App) renderContainers(width, height int) []string {
	style := a.screen.Style
	containers := a.filteredContainers()

	if len(containers) == 0 {
		return a.emptyState(width, height, "No containers."+a.showAllHint())
	}

	columns := a.containerColumns()
	widths := LayoutColumns(columns, width)

	lines := []string{style(styleColumn, RenderHeader(columns, widths))}
	rowsAvailable := height - 1

	selected := a.selected()
	start, end, offset := visibleWindow(len(containers), rowsAvailable, selected, a.offset[ViewContainers])
	a.offset[ViewContainers] = offset

	for i := start; i < end; i++ {
		container := containers[i]
		stat, hasStat := a.stats[container.ID]

		cells := []string{
			container.Name(),
			style(stateStyle(container.State), StateLabel(container.State)),
		}
		if a.config.Stats {
			cells = append(cells,
				style(usageStyle(stat.CPUPercent), FormatPercent(stat.CPUPercent, hasStat)),
				statCell(style, stat.MemUsage, stat.MemPercent, hasStat),
			)
		}
		cells = append(cells,
			style(styleMuted, ShortenImage(container.Image, 24)),
			container.PortSummary(),
			style(styleMuted, FormatAge(container.Created)),
		)

		row := RenderRow(cells, widths)
		if i == selected {
			row = style(styleSelected, tui.Pad(row, width))
		}
		lines = append(lines, row)
	}
	return lines
}

// statCell renders memory usage with its percentage colouring.
func statCell(style func(tui.Style, string) string, usage uint64, percent float64, known bool) string {
	if !known {
		return "-"
	}
	return style(usageStyle(percent), FormatBytes(usage))
}

func (a *App) renderCompose(width, height int) []string {
	style := a.screen.Style
	projects := a.filteredProjects()

	if len(projects) == 0 {
		hint := "No compose projects found."
		if len(a.config.ComposeDirs) > 0 {
			hint += " Scanned: " + strings.Join(a.config.ComposeDirs, ", ")
		}
		return a.emptyState(width, height, hint)
	}

	columns := []Column{
		{Title: "project", Width: 24, Flex: true},
		{Title: "status", Width: 9},
		{Title: "services", Width: 9},
		{Title: "running", Width: 8},
		{Title: "directory", Width: 34},
	}
	widths := LayoutColumns(columns, width)

	lines := []string{style(styleColumn, RenderHeader(columns, widths))}
	selected := a.selected()
	start, end, offset := visibleWindow(len(projects), height-1, selected, a.offset[ViewCompose])
	a.offset[ViewCompose] = offset

	for i := start; i < end; i++ {
		project := projects[i]
		cells := []string{
			project.Name,
			style(projectStatusStyle(project.Status()), project.Status()),
			fmt.Sprintf("%d", len(project.Services())),
			fmt.Sprintf("%d/%d", project.Running(), len(project.Containers)),
			style(styleMuted, project.WorkingDir),
		}
		row := RenderRow(cells, widths)
		if i == selected {
			row = style(styleSelected, tui.Pad(row, width))
		}
		lines = append(lines, row)
	}
	return lines
}

func (a *App) renderImages(width, height int) []string {
	style := a.screen.Style
	images := a.filteredImages()

	if len(images) == 0 {
		return a.emptyState(width, height, "No images.")
	}

	columns := []Column{
		{Title: "repository:tag", Width: 30, Flex: true},
		{Title: "id", Width: 14},
		{Title: "size", Width: 10},
		{Title: "created", Width: 9},
	}
	widths := LayoutColumns(columns, width)

	lines := []string{style(styleColumn, RenderHeader(columns, widths))}
	selected := a.selected()
	start, end, offset := visibleWindow(len(images), height-1, selected, a.offset[ViewImages])
	a.offset[ViewImages] = offset

	for i := start; i < end; i++ {
		image := images[i]
		tag := image.Tag()
		if image.Dangling() {
			tag = style(styleMuted, tag)
		}
		cells := []string{
			tag,
			style(styleMuted, image.ShortID()),
			FormatBytes(uint64(image.Size)),
			style(styleMuted, FormatAge(image.Created)),
		}
		row := RenderRow(cells, widths)
		if i == selected {
			row = style(styleSelected, tui.Pad(row, width))
		}
		lines = append(lines, row)
	}
	return lines
}

func (a *App) renderVolumes(width, height int) []string {
	style := a.screen.Style
	volumes := a.filteredVolumes()

	if len(volumes) == 0 {
		return a.emptyState(width, height, "No volumes.")
	}

	columns := []Column{
		{Title: "name", Width: 30, Flex: true},
		{Title: "driver", Width: 8},
		{Title: "project", Width: 18},
		{Title: "size", Width: 10},
	}
	widths := LayoutColumns(columns, width)

	lines := []string{style(styleColumn, RenderHeader(columns, widths))}
	selected := a.selected()
	start, end, offset := visibleWindow(len(volumes), height-1, selected, a.offset[ViewVolumes])
	a.offset[ViewVolumes] = offset

	for i := start; i < end; i++ {
		volume := volumes[i]
		size := "-"
		if volume.UsageData != nil && volume.UsageData.Size >= 0 {
			size = FormatBytes(uint64(volume.UsageData.Size))
		}
		cells := []string{
			volume.Name,
			style(styleMuted, volume.Driver),
			style(styleMuted, volume.Project()),
			size,
		}
		row := RenderRow(cells, widths)
		if i == selected {
			row = style(styleSelected, tui.Pad(row, width))
		}
		lines = append(lines, row)
	}
	return lines
}

// emptyState renders a centred message for a list with nothing in it.
func (a *App) emptyState(width, height int, message string) []string {
	style := a.screen.Style
	lines := make([]string, 0, height)
	for range height / 3 {
		lines = append(lines, "")
	}
	padding := (width - tui.VisibleWidth(message)) / 2
	if padding < 0 {
		padding = 0
	}
	return append(lines, strings.Repeat(" ", padding)+style(styleMuted, message))
}

// showAllHint nudges toward the key that reveals stopped containers, which is
// the usual reason the list looks emptier than expected.
func (a *App) showAllHint() string {
	if a.showAll {
		return ""
	}
	return " Press a to include stopped ones."
}

// renderLogs draws the log viewer.
func (a *App) renderLogs(width, height int) []string {
	style := a.screen.Style

	lines := a.logs.Lines(a.logFilter)
	rows := RenderLogs(lines, width, a.logWrap, a.logStamps, style)

	if len(rows) == 0 {
		message := "Waiting for output..."
		if a.logStreamed {
			message = "No output."
		}
		if a.logFilter != "" {
			message = "No lines match " + a.logFilter
		}
		return a.emptyState(width, height, message)
	}

	start, end := a.logPager.Window(len(rows), height)
	return rows[start:end]
}

// renderInspect draws the JSON viewer.
func (a *App) renderInspect(width, height int) []string {
	if len(a.inspect) == 0 {
		return a.emptyState(width, height, "Nothing to inspect.")
	}
	start, end := a.inspectPager.Window(len(a.inspect), height)
	return a.inspect[start:end]
}

// renderStatusBar draws the bottom line: a transient message when there is
// one, otherwise contextual key hints.
func (a *App) renderStatusBar(width int) string {
	style := a.screen.Style

	if a.status.text != "" && time.Since(a.status.at) < statusLifetime {
		messageStyle := styleSuccess
		prefix := " "
		if a.status.isError {
			messageStyle = styleError
			prefix = " ! "
		}
		return style(messageStyle, tui.TruncateEllipsis(prefix+a.status.text, width))
	}

	left := " " + a.contextHints()
	right := a.contextState() + " "
	gap := width - tui.VisibleWidth(left) - tui.VisibleWidth(right)
	if gap < 1 {
		return style(styleMuted, tui.TruncateEllipsis(left, width))
	}
	return style(styleMuted, left) + strings.Repeat(" ", gap) + style(styleMuted, right)
}

// contextHints lists the most useful keys for the current mode.
func (a *App) contextHints() string {
	switch a.mode {
	case ModeLogs:
		return "f follow  w wrap  t timestamps  / filter  esc back  ? help"
	case ModeInspect:
		return "j/k scroll  g/G top/bottom  esc back"
	case ModeHelp:
		return "esc back"
	}
	switch a.view {
	case ViewContainers:
		return "enter inspect  l logs  x exec  s start  t stop  r restart  D remove  a all  o sort  S system  / filter  ? help"
	case ViewCompose:
		return "enter actions  l logs  u up  d down  r restart  / filter  ? help"
	case ViewImages:
		return "D remove  P prune  / filter  ? help"
	case ViewVolumes:
		return "D remove  P prune  / filter  ? help"
	}
	return "? help"
}

// contextState summarises the toggles in effect, on the right of the status
// bar.
func (a *App) contextState() string {
	var parts []string

	switch a.mode {
	case ModeLogs:
		parts = append(parts, a.logTitle)
		if a.logFilter != "" {
			parts = append(parts, "filter:"+a.logFilter)
		}
		if a.logs.Dropped() > 0 {
			parts = append(parts, fmt.Sprintf("+%d dropped", a.logs.Dropped()))
		}
		_, height := a.screen.Size()
		parts = append(parts, a.logPager.ScrollInfo(a.logs.Len(), height-2))
	case ModeInspect:
		parts = append(parts, a.inspectTitle)
	default:
		if a.filter != "" {
			parts = append(parts, "filter:"+a.filter)
		}
		if a.view == ViewContainers && a.sortKey != SortDefault {
			parts = append(parts, "sort:"+a.sortKey.Label())
		}
		if a.showAll {
			parts = append(parts, "all")
		}
		if length := a.currentLength(); length > 0 {
			parts = append(parts, fmt.Sprintf("%d/%d", a.selected()+1, length))
		}
	}
	return strings.Join(parts, "  ")
}
