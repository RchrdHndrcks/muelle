package ui

import "strconv"

// Pager scrolls through a list of pre-rendered rows.
//
// It holds only a scroll position: the rows are supplied on each render, so
// content that changes underneath (a followed log stream) needs no
// synchronisation with the pager itself.
type Pager struct {
	offset int
	// follow keeps the view pinned to the end as new rows arrive.
	follow bool
}

// NewPager returns a pager, optionally starting in follow mode.
func NewPager(follow bool) *Pager { return &Pager{follow: follow} }

// Following reports whether the pager is pinned to the end of the content.
func (p *Pager) Following() bool { return p.follow }

// SetFollow turns follow mode on or off.
func (p *Pager) SetFollow(follow bool) { p.follow = follow }

// ToggleFollow flips follow mode and returns the new state.
func (p *Pager) ToggleFollow() bool {
	p.follow = !p.follow
	return p.follow
}

// Offset returns the index of the first visible row.
func (p *Pager) Offset() int { return p.offset }

// Window returns the slice bounds to render for content of the given length in
// a viewport of the given height, and updates the scroll position.
//
// In follow mode the window is always the tail, so new output appears without
// the user having to scroll.
func (p *Pager) Window(total, height int) (start, end int) {
	if height <= 0 || total <= 0 {
		p.offset = 0
		return 0, 0
	}
	if total <= height {
		p.offset = 0
		return 0, total
	}
	if p.follow {
		p.offset = total - height
		return p.offset, total
	}
	if p.offset > total-height {
		p.offset = total - height
	}
	if p.offset < 0 {
		p.offset = 0
	}
	return p.offset, p.offset + height
}

// ScrollBy moves the view by delta rows, clamped to the content.
//
// Scrolling up leaves follow mode: the user has asked to look at something
// specific, and yanking them back to the tail on the next log line would make
// reading impossible. Scrolling back to the bottom re-engages it, which is the
// behaviour every pager with a follow mode has settled on.
func (p *Pager) ScrollBy(delta, total, height int) {
	if delta < 0 {
		p.follow = false
	}
	p.offset += delta
	maxOffset := max(total-height, 0)
	if p.offset >= maxOffset {
		p.offset = maxOffset
		if delta > 0 {
			p.follow = true
		}
	}
	if p.offset < 0 {
		p.offset = 0
	}
}

// ScrollToTop jumps to the beginning and stops following.
func (p *Pager) ScrollToTop() {
	p.offset = 0
	p.follow = false
}

// ScrollToBottom jumps to the end and resumes following.
func (p *Pager) ScrollToBottom(total, height int) {
	p.offset = total - height
	if p.offset < 0 {
		p.offset = 0
	}
	p.follow = true
}

// ScrollInfo renders a position indicator for the status bar.
func (p *Pager) ScrollInfo(total, height int) string {
	if total <= height {
		return "all"
	}
	if p.follow {
		return "following"
	}
	percent := 100 * p.offset / max(total-height, 1)
	return strconv.Itoa(percent) + "%"
}
