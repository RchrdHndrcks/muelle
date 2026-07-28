package ui

import (
	"fmt"
	"strings"
	"time"
)

// FormatBytes renders a byte count in binary units, with the precision
// dropping as the magnitude grows: "512B", "1.4MiB", "23GiB".
func FormatBytes(bytes uint64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%dB", bytes)
	}
	value := float64(bytes)
	units := []string{"KiB", "MiB", "GiB", "TiB", "PiB"}
	index := -1
	for value >= unit && index < len(units)-1 {
		value /= unit
		index++
	}
	if value >= 100 {
		return fmt.Sprintf("%.0f%s", value, units[index])
	}
	return fmt.Sprintf("%.1f%s", value, units[index])
}

// FormatAge renders how long ago a Unix timestamp was, in the compact form a
// container list wants: "3d", "5h", "12m", "just now".
func FormatAge(unix int64) string {
	if unix <= 0 {
		return "-"
	}
	return FormatDuration(time.Since(time.Unix(unix, 0)))
}

// FormatUptime renders how long a container has been running, or "-" when it
// is not running and so has no uptime.
//
// The blank is deliberate rather than showing how long ago it stopped: the
// column reads as "has been up for", and a number there would say the opposite
// of what the state column says.
func FormatUptime(uptime time.Duration, running bool) string {
	if !running {
		return "-"
	}
	return FormatDuration(uptime)
}

// FormatDuration renders a duration to a single significant unit.
func FormatDuration(d time.Duration) string {
	switch {
	case d < 0:
		return "-"
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	case d < 365*24*time.Hour:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	default:
		return fmt.Sprintf("%dy", int(d.Hours()/24/365))
	}
}

// FormatPercent renders a percentage with one decimal, or "-" when there is no
// reading. Stats arrive asynchronously, so a blank cell is normal rather than
// an error.
func FormatPercent(value float64, known bool) string {
	if !known {
		return "-"
	}
	return fmt.Sprintf("%.1f%%", value)
}

// ShortenImage trims an image reference to its most identifying part.
//
// Registry and namespace prefixes are the least interesting part of a long
// reference and are the first thing to go when the column is tight; the tag is
// kept because it is usually what the reader is checking.
func ShortenImage(image string, width int) string {
	if len(image) <= width {
		return image
	}
	trimmed := image
	if index := strings.LastIndex(image, "/"); index >= 0 {
		trimmed = image[index+1:]
	}
	if len(trimmed) <= width {
		return trimmed
	}
	// Still too long: drop the digest, which is never readable at this size
	// anyway.
	if index := strings.Index(trimmed, "@"); index > 0 {
		trimmed = trimmed[:index]
	}
	return trimmed
}

// StateLabel normalises a container state into a short display label.
func StateLabel(state string) string {
	switch state {
	case "running":
		return "up"
	case "exited":
		return "exited"
	case "created":
		return "created"
	case "restarting":
		return "restart"
	case "paused":
		return "paused"
	case "removing":
		return "removing"
	case "dead":
		return "dead"
	default:
		return state
	}
}

// Plural returns the singular or plural form based on a count.
func Plural(count int, singular, plural string) string {
	if count == 1 {
		return fmt.Sprintf("%d %s", count, singular)
	}
	return fmt.Sprintf("%d %s", count, plural)
}
