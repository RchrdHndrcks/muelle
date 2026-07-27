package ui

import (
	"fmt"
	"strings"

	"github.com/RchrdHndrcks/muelle/internal/docker"
	"github.com/RchrdHndrcks/muelle/internal/tui"
)

// systemPanelHeight is how many rows the panel occupies, including its
// separator. Fixed so the list above it does not jump as values change width.
const systemPanelHeight = 5

// Bar characters. The light shade for the empty portion reads as a track
// rather than as absence, which matters when the bar is near zero.
const (
	barFilled = "█"
	barEmpty  = "░"
)

// HostMetrics is everything the system panel displays.
type HostMetrics struct {
	Info docker.Info
	Disk docker.DiskUsage
	// CPUPercent and MemBytes are the sums across running containers.
	// Docker exposes no host-level utilisation, so these describe what the
	// containers are using, which is the number this tool can honestly
	// report — and the one that explains the host's load anyway.
	CPUPercent float64
	MemBytes   uint64
	// DiskKnown reports whether disk usage has been fetched yet; it is
	// refreshed on a slower cadence than everything else.
	DiskKnown bool
}

// minListRowsWithPanel is how many list rows must survive for the panel to be
// worth drawing. Below this the panel would be most of the screen, so it is
// dropped rather than crowding out the thing the user came to look at.
const minListRowsWithPanel = 4

// systemPanel returns the panel's rows, or nothing when it is switched off,
// irrelevant to the current mode, or there is no room for it.
func (a *App) systemPanel(width, available int) []string {
	if !a.showSystem {
		return nil
	}
	// The log and inspect viewers are full-screen by intent: someone
	// reading a stack trace does not want five rows of host metrics.
	if a.mode != ModeList {
		return nil
	}
	if available-systemPanelHeight < minListRowsWithPanel {
		return nil
	}
	return a.renderSystemPanel(width)
}

// renderSystemPanel draws the host summary shown beneath the list.
//
// CPU and memory are explicitly labelled as container totals. The Docker API
// reports the host's capacity but never its utilisation, so presenting these
// as "host CPU" would be a claim the data does not support: anything running
// outside Docker is invisible here.
func (a *App) renderSystemPanel(width int) []string {
	style := a.screen.Style
	metrics := a.metrics

	if metrics.Info.NCPU == 0 && !metrics.DiskKnown {
		return []string{
			style(styleMuted, strings.Repeat("─", width)),
			" " + style(styleMuted, "Collecting host metrics..."),
			"", "", "",
		}
	}

	// Bars take a fixed share of the width so both lines align, with a
	// floor that keeps them meaningful on a narrow terminal.
	barWidth := max(min(width/5, 24), 8)
	labelWidth := 5

	lines := make([]string, 0, systemPanelHeight)
	lines = append(lines, style(styleMuted, strings.Repeat("─", width)))
	lines = append(lines, " "+a.renderHostLine())

	// CPU: the aggregate is out of NCPU * 100%, since a container may use
	// more than one core.
	cpuCapacity := float64(max(metrics.Info.NCPU, 1)) * 100
	cpuFraction := metrics.CPUPercent / cpuCapacity
	cpuDetail := fmt.Sprintf("%.1f%% of %s",
		metrics.CPUPercent, Plural(max(metrics.Info.NCPU, 1), "core", "cores"))
	lines = append(lines, " "+style(styleColumn, tui.Pad("CPU", labelWidth))+
		a.renderBar(cpuFraction, barWidth)+"  "+
		style(styleMuted, cpuDetail)+
		"   "+style(styleMuted, a.renderContainerCounts()))

	memFraction := 0.0
	memDetail := FormatBytes(metrics.MemBytes)
	if metrics.Info.MemTotal > 0 {
		memFraction = float64(metrics.MemBytes) / float64(metrics.Info.MemTotal)
		memDetail = fmt.Sprintf("%s / %s",
			FormatBytes(metrics.MemBytes), FormatBytes(uint64(metrics.Info.MemTotal)))
	}
	lines = append(lines, " "+style(styleColumn, tui.Pad("MEM", labelWidth))+
		a.renderBar(memFraction, barWidth)+"  "+
		style(styleMuted, memDetail)+
		"   "+style(styleMuted, fmt.Sprintf("%d images · %d volumes",
		metrics.Info.Images, metrics.Disk.VolumesCount)))

	lines = append(lines, " "+style(styleColumn, tui.Pad("DISK", labelWidth))+a.renderDiskLine())
	return lines
}

// renderHostLine describes the machine the daemon runs on.
func (a *App) renderHostLine() string {
	style := a.screen.Style
	info := a.metrics.Info

	parts := make([]string, 0, 4)
	if info.Name != "" {
		parts = append(parts, style(tui.Join(tui.StyleBold, tui.Foreground(colourBlue)), info.Name))
	}
	if info.OperatingSystem != "" {
		parts = append(parts, style(styleMuted, info.OperatingSystem))
	}
	if info.OSType != "" && info.Architecture != "" {
		parts = append(parts, style(styleMuted, info.OSType+"/"+info.Architecture))
	}
	if info.ServerVersion != "" {
		parts = append(parts, style(styleMuted, "docker "+info.ServerVersion))
	}
	return strings.Join(parts, style(styleMuted, " · "))
}

// renderContainerCounts summarises container states, colouring only what is
// worth noticing.
func (a *App) renderContainerCounts() string {
	style := a.screen.Style
	info := a.metrics.Info

	summary := fmt.Sprintf("%d running", info.ContainersRunning)
	if info.ContainersPaused > 0 {
		summary += fmt.Sprintf(" · %d paused", info.ContainersPaused)
	}
	if info.ContainersStopped > 0 {
		summary += " · " + style(styleWarning, fmt.Sprintf("%d stopped", info.ContainersStopped))
	}
	return summary
}

// renderDiskLine breaks down what Docker is storing.
func (a *App) renderDiskLine() string {
	style := a.screen.Style
	disk := a.metrics.Disk

	if !a.metrics.DiskKnown {
		return style(styleMuted, "measuring...")
	}

	parts := []string{
		fmt.Sprintf("images %s", FormatBytes(uint64(disk.ImagesSize))),
	}
	if disk.VolumeSizesKnown {
		parts = append(parts, fmt.Sprintf("volumes %s", FormatBytes(uint64(disk.VolumesSize))))
	}
	if disk.ContainersSize > 0 {
		parts = append(parts, fmt.Sprintf("containers %s", FormatBytes(uint64(disk.ContainersSize))))
	}
	if disk.BuildCacheSize > 0 {
		parts = append(parts, fmt.Sprintf("build cache %s", FormatBytes(uint64(disk.BuildCacheSize))))
	}

	line := style(styleMuted, strings.Join(parts, " · "))
	if reclaimable := disk.Reclaimable(); reclaimable > 0 {
		// Worth highlighting: it is the actionable number on the line.
		line += "   " + style(styleWarning,
			fmt.Sprintf("%s reclaimable", FormatBytes(uint64(reclaimable))))
		line += style(styleMuted, fmt.Sprintf(" (%d unused images)", disk.ImagesUnused))
	}
	return line
}

// renderBar draws a proportion as a bar, coloured by how full it is.
func (a *App) renderBar(fraction float64, width int) string {
	style := a.screen.Style
	if width < 3 {
		return ""
	}
	if fraction < 0 {
		fraction = 0
	}
	if fraction > 1 {
		fraction = 1
	}

	inner := width - 2
	filled := int(fraction * float64(inner))
	// Any non-zero usage should show at least one cell, or an idle host and
	// a lightly loaded one look identical.
	if filled == 0 && fraction > 0 {
		filled = 1
	}
	if filled > inner {
		filled = inner
	}

	return style(styleMuted, "[") +
		style(usageStyle(fraction*100), strings.Repeat(barFilled, filled)) +
		style(styleMuted, strings.Repeat(barEmpty, inner-filled)+"]")
}
