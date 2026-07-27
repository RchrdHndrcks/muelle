package ui

import (
	"strings"
	"testing"

	"github.com/RchrdHndrcks/muelle/internal/tui"
)

func testColumns() []Column {
	return []Column{
		{Title: "name", Width: 20, Flex: true},
		{Title: "state", Width: 8},
		{Title: "image", Width: 15},
	}
}

func TestLayoutColumnsGivesSlackToFlexibleColumn(t *testing.T) {
	widths := LayoutColumns(testColumns(), 60)

	if len(widths) != 3 {
		t.Fatalf("got %d columns, want all 3 to fit", len(widths))
	}
	// 20 + 8 + 15 + 2 gaps = 45, leaving 15 for the flexible column.
	if widths[0] != 35 {
		t.Errorf("got name width %d, want 35 (20 preferred + 15 slack)", widths[0])
	}
	if widths[1] != 8 || widths[2] != 15 {
		t.Errorf("got fixed widths %d and %d, want them unchanged", widths[1], widths[2])
	}
}

// A squeezed column is worse than an absent one: a name clipped to four
// characters identifies nothing.
func TestLayoutColumnsDropsTrailingColumnsWhenNarrow(t *testing.T) {
	widths := LayoutColumns(testColumns(), 30)

	if len(widths) >= 3 {
		t.Fatalf("got %d columns in 30 cells, want the trailing one dropped", len(widths))
	}
	total := 0
	for _, w := range widths {
		total += w
	}
	if total > 30 {
		t.Errorf("laid out %d cells into a 30-cell width", total)
	}
}

func TestLayoutColumnsClipsWhenNothingFits(t *testing.T) {
	widths := LayoutColumns(testColumns(), 10)

	if len(widths) != 1 || widths[0] != 10 {
		t.Errorf("got %v, want a single clipped column", widths)
	}
}

func TestLayoutColumnsHandlesZeroWidth(t *testing.T) {
	if got := LayoutColumns(testColumns(), 0); got != nil {
		t.Errorf("got %v, want nothing laid out", got)
	}
}

func TestRenderRowAlignsToColumnWidths(t *testing.T) {
	widths := []int{10, 6}

	row := RenderRow([]string{"web", "up"}, widths)

	// 10 + 1 gap + 2 for the final unpadded cell.
	if got := tui.VisibleWidth(row); got != 13 {
		t.Errorf("got width %d for %q, want the first cell padded and the last not", got, row)
	}
}

func TestRenderRowIgnoresCellsBeyondLayout(t *testing.T) {
	row := RenderRow([]string{"web", "up", "dropped"}, []int{10, 6})

	if strings.Contains(row, "dropped") {
		t.Errorf("got %q, want the extra cell omitted", row)
	}
}

func TestRenderRowTruncatesOverlongCell(t *testing.T) {
	row := RenderRow([]string{"a-very-long-container-name", "up"}, []int{10, 6})

	if got := tui.VisibleWidth(row); got != 13 {
		t.Errorf("got width %d for %q, want the long cell clipped to its column", got, row)
	}
}

// The window should not move while the cursor travels within it; a list that
// re-centres on every keypress is unreadable.
func TestVisibleWindowStaysStillWhileSelectionIsVisible(t *testing.T) {
	start, end, offset := visibleWindow(100, 10, 5, 0)

	if start != 0 || end != 10 || offset != 0 {
		t.Errorf("got %d..%d offset %d, want the window unchanged", start, end, offset)
	}
}

func TestVisibleWindowScrollsWhenSelectionPassesBottom(t *testing.T) {
	start, end, offset := visibleWindow(100, 10, 12, 0)

	if offset != 3 || start != 3 || end != 13 {
		t.Errorf("got %d..%d offset %d, want it scrolled just enough to reveal row 12",
			start, end, offset)
	}
}

func TestVisibleWindowScrollsWhenSelectionPassesTop(t *testing.T) {
	_, _, offset := visibleWindow(100, 10, 2, 20)

	if offset != 2 {
		t.Errorf("got offset %d, want it scrolled up to the selection", offset)
	}
}

func TestVisibleWindowShowsEverythingWhenItFits(t *testing.T) {
	start, end, offset := visibleWindow(5, 10, 3, 0)

	if start != 0 || end != 5 || offset != 0 {
		t.Errorf("got %d..%d offset %d, want the whole list", start, end, offset)
	}
}

// A list shrinking below the scroll position (containers removed) must not
// leave the window past the end.
func TestVisibleWindowClampsOffsetPastEnd(t *testing.T) {
	start, end, offset := visibleWindow(12, 10, 11, 50)

	if offset != 2 || start != 2 || end != 12 {
		t.Errorf("got %d..%d offset %d, want the window pulled back to the end",
			start, end, offset)
	}
}

func TestVisibleWindowHandlesEmptyList(t *testing.T) {
	start, end, _ := visibleWindow(0, 10, 0, 0)

	if start != 0 || end != 0 {
		t.Errorf("got %d..%d, want an empty window", start, end)
	}
}

func TestClampKeepsSelectionInRange(t *testing.T) {
	cases := []struct{ index, length, want int }{
		{5, 10, 5},
		{-1, 10, 0},
		{10, 10, 9},
		{99, 10, 9},
		{0, 0, 0},
		{5, 0, 0},
	}

	for _, tc := range cases {
		if got := clamp(tc.index, tc.length); got != tc.want {
			t.Errorf("clamp(%d, %d) = %d, want %d", tc.index, tc.length, got, tc.want)
		}
	}
}
