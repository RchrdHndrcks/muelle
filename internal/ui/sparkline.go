package ui

import "strings"

// A stats column is a point in time: a spike that lands between two glances
// leaves no trace, so "was the CPU quiet while I looked away" is a question
// the list cannot answer. This file gives each container a short memory — a
// fixed ring of the CPU readings the refresh would otherwise discard — and
// renders it as a sparkline beside the current figure, so the last minute or
// so of behaviour is visible in the row itself.

// cpuHistoryLen is how many samples a container's history keeps. The stats
// stream pushes one a second, so this is about a minute and a half — long
// enough that a spike survives a glance away, short enough that the row
// still describes now rather than ten minutes ago.
const cpuHistoryLen = 90

// sparkCells is how many of those samples the sparkline draws, newest last.
// Fewer than the ring holds: a cell per sample across the whole ring would
// cost the row thirty cells, and the recent end of the history is the part
// a reader is asking about.
const sparkCells = 12

// history is a fixed-capacity ring of CPU percentages. A ring rather than an
// appended slice for the same reason the log buffer is one: a container that
// runs for a week must not grow memory with every refresh.
type history struct {
	samples [cpuHistoryLen]float64
	start   int
	count   int
}

// push appends a sample, discarding the oldest once the ring is full.
func (h *history) push(value float64) {
	if h.count < len(h.samples) {
		h.samples[(h.start+h.count)%len(h.samples)] = value
		h.count++
		return
	}
	h.samples[h.start] = value
	h.start = (h.start + 1) % len(h.samples)
}

// last returns the newest n samples, oldest first — the order a sparkline
// reads in. A nil history answers with nothing, so callers can index the
// history map without checking whether a container has one yet.
func (h *history) last(n int) []float64 {
	if h == nil || n <= 0 {
		return nil
	}
	if n > h.count {
		n = h.count
	}
	values := make([]float64, n)
	for i := range values {
		values[i] = h.samples[(h.start+h.count-n+i)%len(h.samples)]
	}
	return values
}

// sparkRunes are the eight block heights a cell can take, lowest first. Plain
// characters carry the whole reading, so the sparkline survives NO_COLOR
// unchanged.
var sparkRunes = []rune("▁▂▃▄▅▆▇█")

// Sparkline renders CPU percentages as one block character each, scaled to
// the row's own range: the highest reading in the window fills the top bar
// and everything below it sits proportionally, so a quietly idling container
// at half a percent shows just as much shape as a busy one.
//
// A fixed 0-100% axis — which is what every other sparkline drawing tool
// settles on — turns out to be the wrong scale for this column: an idle
// host's readings all land in the bottom bar, and a whole row of identical
// lowest bars is a row of dashes, which reads as broken. Scaling to the
// window's own maximum keeps the same reading visible regardless of the
// host's load; the current figure sits beside the bars for the absolute
// level, and per-row scaling keeps that figure honest across neighbours.
//
// Readings are clamped to the scale's ends rather than rescaling on their
// own: a negative glitch or a multi-core burst must not stretch the axis
// and flatten the rest of the window.
func Sparkline(values []float64) string {
	if len(values) == 0 {
		return ""
	}
	max := values[0]
	for _, value := range values[1:] {
		if value > max {
			max = value
		}
	}
	var b strings.Builder
	for _, value := range values {
		if max <= 0 {
			// Every reading is zero: nothing has run, so the row stays
			// at the lowest bar rather than panicking on a zero axis.
			b.WriteRune(sparkRunes[0])
			continue
		}
		index := int(value / max * float64(len(sparkRunes)))
		if index < 0 {
			index = 0
		}
		if index >= len(sparkRunes) {
			index = len(sparkRunes) - 1
		}
		b.WriteRune(sparkRunes[index])
	}
	return b.String()
}
