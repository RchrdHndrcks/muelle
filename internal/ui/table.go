package ui

import (
	"strings"

	"github.com/RchrdHndrcks/muelle/internal/tui"
)

// Column describes one column of a list view.
type Column struct {
	Title string
	// Width is the preferred width in cells. For a flexible column it is
	// the minimum.
	Width int
	// Flex marks the column that absorbs leftover space, or gives up space
	// first when the terminal is narrow. Exactly one column per table
	// should be flexible.
	Flex bool
}

// columnGap is the spacing between columns.
const columnGap = 1

// LayoutColumns distributes total width across columns.
//
// Fixed columns keep their width; the flexible column takes whatever is left.
// When the terminal is too narrow for even the fixed columns, trailing columns
// are dropped entirely rather than squeezed into unreadable slivers — a
// truncated name column is useless, but losing the ports column is survivable.
// The returned slice has one entry per rendered column, so it can be shorter
// than columns.
func LayoutColumns(columns []Column, total int) []int {
	if total <= 0 || len(columns) == 0 {
		return nil
	}

	// Find how many columns fit at their preferred widths.
	count := len(columns)
	for count > 0 {
		required := 0
		for i := range count {
			required += columns[i].Width + columnGap
		}
		if required-columnGap <= total {
			break
		}
		count--
	}
	if count == 0 {
		// Not even the first column fits; render it clipped rather than
		// nothing at all.
		return []int{total}
	}

	widths := make([]int, count)
	used := 0
	for i := range count {
		widths[i] = columns[i].Width
		used += columns[i].Width + columnGap
	}
	used -= columnGap

	// Hand the slack to the flexible column.
	if slack := total - used; slack > 0 {
		for i := range count {
			if columns[i].Flex {
				widths[i] += slack
				break
			}
		}
	}
	return widths
}

// RenderRow joins cells into a single line, padding each to its column width.
// Cells beyond the laid-out columns are ignored.
func RenderRow(cells []string, widths []int) string {
	var b strings.Builder
	for i, width := range widths {
		if i >= len(cells) {
			break
		}
		if i > 0 {
			b.WriteString(strings.Repeat(" ", columnGap))
		}
		if i == len(widths)-1 {
			// The last column needs no trailing padding; leaving it off
			// avoids a run of spaces carrying the selection highlight to
			// the screen edge when it should stop at the text.
			b.WriteString(tui.TruncateEllipsis(cells[i], width))
			continue
		}
		b.WriteString(tui.Pad(cells[i], width))
	}
	return b.String()
}

// RenderHeader renders the column titles.
func RenderHeader(columns []Column, widths []int) string {
	titles := make([]string, len(widths))
	for i := range widths {
		titles[i] = strings.ToUpper(columns[i].Title)
	}
	return RenderRow(titles, widths)
}

// visibleWindow computes which slice of a list to draw so that the selected
// row stays on screen.
//
// The window only moves when the selection would leave it, which keeps the
// list still while the cursor travels within the visible rows — a list that
// re-centres on every keypress is disorienting to read.
func visibleWindow(total, height, selected, offset int) (start, end, newOffset int) {
	if height <= 0 || total == 0 {
		return 0, 0, 0
	}
	if height >= total {
		return 0, total, 0
	}
	if selected < offset {
		offset = selected
	}
	if selected >= offset+height {
		offset = selected - height + 1
	}
	if maxOffset := total - height; offset > maxOffset {
		offset = maxOffset
	}
	if offset < 0 {
		offset = 0
	}
	return offset, offset + height, offset
}

// clamp constrains a selection index to a valid range for a list of the given
// length. Lists change size under the cursor constantly as containers come and
// go, so every navigation path funnels through this.
func clamp(index, length int) int {
	if length <= 0 {
		return 0
	}
	if index < 0 {
		return 0
	}
	if index >= length {
		return length - 1
	}
	return index
}
