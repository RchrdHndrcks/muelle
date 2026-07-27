package ui

import "testing"

func TestPagerFollowShowsTail(t *testing.T) {
	pager := NewPager(true)

	start, end := pager.Window(100, 10)

	if start != 90 || end != 100 {
		t.Errorf("got %d..%d, want the last 10 rows", start, end)
	}
}

func TestPagerWindowShowsEverythingWhenItFits(t *testing.T) {
	pager := NewPager(true)

	start, end := pager.Window(5, 20)

	if start != 0 || end != 5 {
		t.Errorf("got %d..%d, want the whole content", start, end)
	}
}

// Scrolling up means the user wants to read something; yanking them back to
// the tail on the next log line would make that impossible.
func TestScrollingUpLeavesFollowMode(t *testing.T) {
	pager := NewPager(true)
	pager.Window(100, 10)

	pager.ScrollBy(-5, 100, 10)

	if pager.Following() {
		t.Error("scrolling up should pause follow")
	}
	if pager.Offset() != 85 {
		t.Errorf("got offset %d, want 85", pager.Offset())
	}
}

// Returning to the bottom is the natural way to say "keep going".
func TestScrollingBackToBottomResumesFollow(t *testing.T) {
	pager := NewPager(true)
	pager.Window(100, 10)
	pager.ScrollBy(-20, 100, 10)

	pager.ScrollBy(20, 100, 10)

	if !pager.Following() {
		t.Error("reaching the bottom should resume follow")
	}
}

func TestScrollCannotPassTheStart(t *testing.T) {
	pager := NewPager(false)

	pager.ScrollBy(-100, 50, 10)

	if pager.Offset() != 0 {
		t.Errorf("got offset %d, want 0", pager.Offset())
	}
}

func TestScrollCannotPassTheEnd(t *testing.T) {
	pager := NewPager(false)

	pager.ScrollBy(1000, 50, 10)

	if pager.Offset() != 40 {
		t.Errorf("got offset %d, want 40 (50 rows less a 10-row viewport)", pager.Offset())
	}
}

func TestScrollToTopStopsFollowing(t *testing.T) {
	pager := NewPager(true)

	pager.ScrollToTop()

	if pager.Offset() != 0 || pager.Following() {
		t.Errorf("got offset %d following %v, want 0 and paused", pager.Offset(), pager.Following())
	}
}

func TestScrollToBottomResumesFollowing(t *testing.T) {
	pager := NewPager(false)

	pager.ScrollToBottom(100, 10)

	if pager.Offset() != 90 || !pager.Following() {
		t.Errorf("got offset %d following %v, want 90 and following", pager.Offset(), pager.Following())
	}
}

// Content shrinking under a paused pager (the buffer was cleared) must not
// leave the window past the end.
func TestWindowClampsWhenContentShrinks(t *testing.T) {
	pager := NewPager(false)
	pager.ScrollBy(80, 100, 10)

	start, end := pager.Window(20, 10)

	if start != 10 || end != 20 {
		t.Errorf("got %d..%d, want the window pulled back into range", start, end)
	}
}

func TestWindowHandlesEmptyContent(t *testing.T) {
	pager := NewPager(true)

	start, end := pager.Window(0, 10)

	if start != 0 || end != 0 {
		t.Errorf("got %d..%d, want an empty window", start, end)
	}
}

func TestScrollInfoDescribesPosition(t *testing.T) {
	pager := NewPager(true)
	if got := pager.ScrollInfo(100, 10); got != "following" {
		t.Errorf("got %q, want %q", got, "following")
	}

	if got := pager.ScrollInfo(5, 10); got != "all" {
		t.Errorf("got %q, want %q when everything fits", got, "all")
	}

	pager.SetFollow(false)
	pager.ScrollToTop()
	if got := pager.ScrollInfo(100, 10); got != "0%" {
		t.Errorf("got %q, want %q at the top", got, "0%")
	}
}

func TestToggleFollowReportsNewState(t *testing.T) {
	pager := NewPager(false)

	if !pager.ToggleFollow() {
		t.Error("toggling from paused should return true")
	}
	if pager.ToggleFollow() {
		t.Error("toggling from following should return false")
	}
}
