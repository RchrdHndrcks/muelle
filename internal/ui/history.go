package ui

import (
	"context"
	"strings"

	"github.com/RchrdHndrcks/muelle/internal/docker"
	"github.com/RchrdHndrcks/muelle/internal/tui"
)

// historyLoaded carries an image's layer history.
type historyLoaded struct {
	title   string
	entries []docker.HistoryEntry
	err     error
}

func (e historyLoaded) apply(a *App) {
	if e.err != nil {
		a.setError("history: %v", e.err)
		return
	}
	a.history = e.entries
	a.historyTitle = e.title
	a.historyPager = NewPager(false)
	a.mode = ModeHistory
}

// openHistory fetches and shows the layers an image is made of.
//
// This answers the question the SIZE column raises but cannot answer: why is
// this image 1.2GB? The history names each build step and what it cost, which
// is what turns "it is big" into "this apt-get install is most of it".
func (a *App) openHistory(ctx context.Context, image docker.Image) {
	go func() {
		fetchCtx, cancel := context.WithTimeout(ctx, refreshTimeout)
		defer cancel()

		entries, err := a.docker.History(fetchCtx, image.ID)
		a.post(historyLoaded{title: image.Tag(), entries: entries, err: err})
	}()
}

// TrimCreatedBy reduces a layer's creating command to the part worth reading.
//
// The classic builder records every Dockerfile instruction wrapped in
// "/bin/sh -c", with a "#(nop)" marker on metadata-only steps, so half of
// every row would be the same noise — "docker history" strips it, and so does
// this. A real RUN keeps its "/bin/sh -c", which is what distinguishes a
// command that executed from an instruction that only wrote metadata. Tabs and
// newlines collapse to single spaces because the command must survive being a
// one-line table cell.
func TrimCreatedBy(createdBy string) string {
	flattened := strings.Join(strings.Fields(createdBy), " ")
	trimmed := strings.TrimPrefix(flattened, "/bin/sh -c #(nop)")
	return strings.TrimSpace(trimmed)
}

// historyTotal sums the layers' sizes.
//
// Summed from the layers rather than taken from the image list so the header
// and the rows beneath it cannot disagree: this figure is the one the rows add
// up to, which is the claim the header is making.
func historyTotal(entries []docker.HistoryEntry) int64 {
	var total int64
	for _, entry := range entries {
		total += entry.Size
	}
	return total
}

// renderHistory draws the layer history viewer.
func (a *App) renderHistory(width, height int) []string {
	style := a.screen.Style

	if len(a.history) == 0 {
		return a.emptyState(width, height, "No history reported.")
	}

	// The header carries the two figures the layers only imply: what the
	// image costs in total, and across how many layers.
	summary := " " + style(tui.StyleBold, a.historyTitle) +
		style(styleMuted, "  "+FormatBytes(uint64(historyTotal(a.history)))+
			" in "+Plural(len(a.history), "layer", "layers"))

	columns := []Column{
		{Title: "size", Width: 9},
		{Title: "created", Width: 9},
		{Title: "created by", Width: 30, Flex: true},
	}
	widths := LayoutColumns(columns, width)

	lines := []string{summary, style(styleColumn, RenderHeader(columns, widths))}

	// Newest first, as the daemon reports: the layers your own Dockerfile
	// added sit at the top, and they are the ones you can act on.
	start, end := a.historyPager.Window(len(a.history), height-2)
	for _, entry := range a.history[start:end] {
		size := FormatBytes(uint64(max(entry.Size, 0)))
		if entry.Size == 0 {
			// Metadata-only steps (ENV, CMD, LABEL) add nothing, and in a
			// view about where the bytes went a loud zero is a distraction.
			size = style(styleMuted, "0B")
		}
		cells := []string{
			size,
			style(styleMuted, FormatAge(entry.Created)),
			TrimCreatedBy(entry.CreatedBy),
		}
		lines = append(lines, RenderRow(cells, widths))
	}
	return lines
}

// handleHistoryKey handles the layer history viewer's keys.
func (a *App) handleHistoryKey(key tui.Key) bool {
	_, height := a.screen.Size()
	// Header, status bar, summary line and column titles all sit outside the
	// scrolling region.
	viewport := height - 4
	total := len(a.history)

	switch {
	case key.Type == tui.KeyEscape, key.IsRune('q'):
		a.mode = ModeList
	case key.Type == tui.KeyDown, key.IsRune('j'):
		a.historyPager.ScrollBy(1, total, viewport)
	case key.Type == tui.KeyUp, key.IsRune('k'):
		a.historyPager.ScrollBy(-1, total, viewport)
	case key.Type == tui.KeyPageDown, key.Type == tui.KeyCtrlD:
		a.historyPager.ScrollBy(viewport/2, total, viewport)
	case key.Type == tui.KeyPageUp, key.Type == tui.KeyCtrlU:
		a.historyPager.ScrollBy(-viewport/2, total, viewport)
	case key.IsRune('g'), key.Type == tui.KeyHome:
		a.historyPager.ScrollToTop()
	case key.IsRune('G'), key.Type == tui.KeyEnd:
		a.historyPager.ScrollToBottom(total, viewport)
	default:
		return false
	}
	return true
}
