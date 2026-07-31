package ui

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/RchrdHndrcks/muelle/internal/docker"
	"github.com/RchrdHndrcks/muelle/internal/group"
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
	if a.build != "" {
		summary += "  muelle " + a.build
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
	case ViewNetworks:
		return a.renderNetworks(width, height)
	}
	return nil
}

// containerColumns is the container list layout. Name flexes because it is
// what identifies the row; ports and image are the first to be dropped when
// the terminal is narrow.
func (a *App) containerColumns() []Column {
	columns := []Column{
		{Title: "name", Width: 22, Flex: true},
		{Title: "state", Width: 13},
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
		// Eight cells, not seven, because "just now" is eight and a
		// seven-cell column renders it as "just n…" — in the uptime column
		// that is the value a user is most likely to be looking at, having
		// just restarted something.
		Column{Title: "age", Width: 8},
		// Uptime sits last because that is the position LayoutColumns
		// drops first on a narrow terminal, and age is the older habit:
		// someone reading at 80 columns is better served losing the newer
		// column than having it displace one they already read by position.
		Column{Title: "uptime", Width: 8},
	)
}

func (a *App) renderContainers(width, height int) []string {
	style := a.screen.Style
	rows := a.rows()

	if len(rows) == 0 {
		return a.emptyState(width, height, "No containers."+a.showAllHint())
	}

	columns := a.containerColumns()
	widths := LayoutColumns(columns, width)

	lines := []string{style(styleColumn, RenderHeader(columns, widths))}
	rowsAvailable := height - 1

	selected := a.selected()
	start, end, offset := visibleWindow(len(rows), rowsAvailable, selected, a.offset[ViewContainers])
	a.offset[ViewContainers] = offset

	for i := start; i < end; i++ {
		var cells []string
		if rows[i].Header != nil {
			cells = a.headerCells(*rows[i].Header)
		} else {
			cells = a.containerCells(rows[i].Container)
		}

		row := RenderRow(cells, widths)
		if i == selected {
			row = style(styleSelected, tui.Pad(row, width))
		}
		lines = append(lines, row)
	}
	return lines
}

// containerCells renders one container as the columns of the list.
func (a *App) containerCells(container docker.Container) []string {
	style := a.screen.Style
	stat, hasStat := a.stats[container.ID]

	// Indented under the heading it belongs to. Without it the headings and
	// their containers share a column and the whole thing reads as a flat
	// list with odd extra rows in it.
	name := container.Name()
	if a.grouped {
		name = "  " + name
	}

	cells := []string{
		name,
		a.stateCell(container),
	}
	if a.config.Stats {
		cells = append(cells,
			style(usageStyle(stat.CPUPercent), FormatPercent(stat.CPUPercent, hasStat)),
			statCell(style, stat.MemUsage, stat.MemPercent, hasStat),
		)
	}
	uptime, running := container.Uptime()
	return append(cells,
		style(styleMuted, ShortenImage(container.Image, 24)),
		container.PortSummary(),
		style(styleMuted, FormatAge(container.Created)),
		style(styleMuted, FormatUptime(uptime, running)),
	)
}

// headerCells renders an application's summary in the same columns as the
// containers beneath it, so the totals line up under the figures they total.
func (a *App) headerCells(header Header) []string {
	style := a.screen.Style

	marker := "▾ "
	if header.Collapsed {
		marker = "▸ "
	}
	cells := []string{
		style(tui.StyleBold, marker+header.Name),
		style(styleMuted, fmt.Sprintf("%d/%d up", header.Running, header.Total)),
	}
	if a.config.Stats {
		cells = append(cells,
			style(usageStyle(header.CPUPercent), FormatPercent(header.CPUPercent, header.Total > 0)),
			statCell(style, header.MemUsage, 0, header.MemUsage > 0),
		)
	}
	// The remaining columns describe a container, and a group is not one.
	// An image or an age here would be the first member's, presented as the
	// application's.
	origin := ""
	if header.Source == group.FromCompose {
		origin = "compose"
	}
	return append(cells, style(styleMuted, origin), "", "", "")
}

// stateCell renders a container's state with everything the word alone hides:
// the code it exited with, its healthcheck marker, and how many times it has
// restarted.
//
// Each covers a different blind spot. "exited" does not distinguish a clean
// shutdown from an out-of-memory kill. "up" does not distinguish a service
// that has run for a month from one crash-looping every thirty seconds, since
// the daemon reports the state of the current attempt. And a failing
// healthcheck is invisible in the state entirely.
func (a *App) stateCell(container docker.Container) string {
	style := a.screen.Style

	// A service being replaced says so instead of reporting the state of
	// whichever container happens to exist at this instant — which during a
	// deploy is either the one on its way out or the one not serving yet.
	if state, deploying := a.deployPhase(container); deploying {
		return style(styleWarning, string(state.Phase)) + " " +
			style(styleMuted, FormatElapsed(time.Since(state.Since)))
	}

	cell := style(stateStyle(container.State), StateLabel(container.State))

	// A stopped container's exit code replaces the label: "exit 137" says
	// strictly more than "exited". A zero stays muted so only failures draw
	// the eye.
	if code, known := container.ExitCode(); known && !container.Running() {
		if code == 0 {
			cell = style(styleMuted, "exit 0")
		} else {
			cell = style(tui.Foreground(colourRed), "exit "+strconv.Itoa(code))
		}
	} else {
		health := container.Health()
		if probed, wasProbed := a.probeHealth(container); wasProbed {
			health = probed
		}
		if glyph := healthGlyph(health); glyph != "" {
			cell += " " + style(healthStyle(health), glyph)
		}
	}

	// The restart count applies either way. A container that crash-looped
	// forty times and finally stayed down is precisely the case where both
	// halves matter.
	if count := a.restartCounts[container.ID]; count > 0 {
		// Capped so the column has a fixed worst case: past a hundred
		// restarts the exact figure tells you nothing the cap does not.
		text := "×" + strconv.Itoa(count)
		if count > 99 {
			text = "×99+"
		}
		cell += style(styleWarning, text)
	}
	return cell
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
		{Title: "usage", Width: 7},
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
			a.usageCell(image),
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

// usageCell says whether an image is holding disk for nothing.
//
// The metrics panel reports a reclaimable total but names no images, which
// leaves the figure impossible to act on. This is the column that turns it
// into a list you can work through: "unused" marks a removal candidate, a
// count marks one that is pinned.
func (a *App) usageCell(image docker.Image) string {
	style := a.screen.Style

	if count := a.imageUsage[image.ID]; count > 0 {
		// Kept short so the column stays narrow: the count is what matters,
		// and "used" says what it counts.
		return style(styleMuted, strconv.Itoa(count)+" used")
	}
	if len(a.imageUsage) == 0 {
		// The container list has not arrived yet; claiming "unused" now
		// would mark every image as removable.
		return style(styleMuted, "-")
	}
	return style(styleWarning, "unused")
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

func (a *App) renderNetworks(width, height int) []string {
	style := a.screen.Style
	networks := a.filteredNetworks()

	if len(networks) == 0 {
		return a.emptyState(width, height, "No networks.")
	}

	columns := []Column{
		{Title: "name", Width: 28, Flex: true},
		{Title: "driver", Width: 8},
		{Title: "scope", Width: 7},
		{Title: "subnet", Width: 20},
		{Title: "project", Width: 18},
	}
	widths := LayoutColumns(columns, width)

	lines := []string{style(styleColumn, RenderHeader(columns, widths))}
	selected := a.selected()
	start, end, offset := visibleWindow(len(networks), height-1, selected, a.offset[ViewNetworks])
	a.offset[ViewNetworks] = offset

	for i := start; i < end; i++ {
		network := networks[i]
		name := network.Name
		if network.Predefined() {
			// The daemon's own networks cannot be removed, so they are
			// dimmed to set them apart from the user's.
			name = style(styleMuted, name)
		}
		cells := []string{
			name,
			style(styleMuted, network.Driver),
			style(styleMuted, network.Scope),
			network.Subnet(),
			style(styleMuted, network.Project()),
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
	rows := RenderLogs(lines, a.logOptions(width), style)

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

// logOptions gathers the log view's current settings.
func (a *App) logOptions(width int) LogOptions {
	return LogOptions{
		Width:      width,
		Wrap:       a.logWrap,
		Timestamps: a.logStamps,
		Format:     a.logFormat,
		Levelled:   a.logs.Levelled(),
	}
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

	// When the two do not fit, the hints give way rather than the state.
	// Hints are a reminder of keys the user can also get from "?", while
	// the right side carries information found nowhere else on screen —
	// the filter in effect, the position, why the selected container died.
	rightWidth := tui.VisibleWidth(right)
	if rightWidth >= width {
		return style(styleMuted, tui.TruncateEllipsis(right, width))
	}
	gap := width - tui.VisibleWidth(left) - rightWidth
	if gap < 1 {
		left = tui.TruncateEllipsis(left, width-rightWidth-1)
		gap = width - tui.VisibleWidth(left) - rightWidth
	}
	return style(styleMuted, left) + strings.Repeat(" ", gap) + style(styleMuted, right)
}

// contextHints lists the most useful keys for the current mode.
func (a *App) contextHints() string {
	switch a.mode {
	case ModeLogs:
		return "f follow  w wrap  t timestamps  F format  / filter  esc back  ? help"
	case ModeInspect:
		return "j/k scroll  g/G top/bottom  esc back"
	case ModeProcesses:
		return "j/k scroll  g/G top/bottom  esc back"
	case ModeHelp:
		return "esc back"
	}
	switch a.view {
	case ViewContainers:
		return "enter inspect  l logs  x exec  s start  t stop  r restart  D remove  T top  a all  A group  o sort  P prune  / filter  ? help"
	case ViewCompose:
		return "enter actions  l logs  e edit  u up  d down  r restart  / filter  ? help"
	case ViewImages:
		return "D remove  P prune  u unused only  / filter  ? help"
	case ViewVolumes:
		return "D remove  P prune  / filter  ? help"
	case ViewNetworks:
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
	case ModeProcesses:
		parts = append(parts, a.processSummary())
	default:
		// Explain a non-zero exit for the selected container. 137 and 143
		// are both "stopped" to the daemon but mean very different things,
		// and the bare number does not say which.
		if a.view == ViewContainers {
			if container, ok := a.selectedContainer(); ok && !container.Running() {
				if code, known := container.ExitCode(); known && code != 0 {
					if reason := docker.ExitReason(code); reason != "" {
						parts = append(parts, reason)
					}
				}
			}
		}
		if a.filter != "" {
			parts = append(parts, "filter:"+a.filter)
		}
		if a.view == ViewContainers && a.sortKey != SortDefault {
			parts = append(parts, "sort:"+a.sortKey.Label())
		}
		if a.view == ViewImages {
			if a.unusedImagesOnly {
				parts = append(parts, "unused only")
			}
			// The panel's reclaimable figure is meaningless without a way
			// to find the images behind it; naming the total here ties the
			// two together.
			if count, reclaimable := a.unusedImages(); count > 0 {
				parts = append(parts, fmt.Sprintf("%d unused · %s reclaimable",
					count, FormatBytes(uint64(reclaimable))))
			}
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
