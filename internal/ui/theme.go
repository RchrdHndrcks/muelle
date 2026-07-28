package ui

import (
	"github.com/RchrdHndrcks/muelle/internal/docker"
	"github.com/RchrdHndrcks/muelle/internal/logfmt"
	"github.com/RchrdHndrcks/muelle/internal/tui"
)

// Palette entries, as 256-colour indices. Sticking to the 256-colour cube
// rather than truecolor keeps the UI legible over SSH to older terminals,
// which is where this tool spends most of its life.
const (
	colourGreen  = 78
	colourRed    = 203
	colourYellow = 179
	colourOrange = 215
	colourBlue   = 75
	colourPurple = 141
	colourGrey   = 245
	colourDark   = 237
	colourText   = 252
	colourWhite  = 231
)

// Styles used across the views.
var (
	styleHeader     = tui.Join(tui.StyleBold, tui.Foreground(colourWhite), tui.Background(colourDark))
	styleTabActive  = tui.Join(tui.StyleBold, tui.Foreground(colourWhite))
	styleTabIdle    = tui.Foreground(colourGrey)
	styleColumn     = tui.Join(tui.StyleBold, tui.Foreground(colourGrey))
	styleSelected   = tui.Join(tui.Background(colourDark), tui.Foreground(colourWhite))
	styleMuted      = tui.Foreground(colourGrey)
	styleError      = tui.Foreground(colourRed)
	styleSuccess    = tui.Foreground(colourGreen)
	styleWarning    = tui.Foreground(colourYellow)
	styleAccent     = tui.Foreground(colourBlue)
	styleKey        = tui.Join(tui.StyleBold, tui.Foreground(colourPurple))
	styleStderr     = tui.Foreground(colourRed)
	styleTimestamp  = tui.Foreground(colourGrey)
	styleFieldKey   = tui.Foreground(colourBlue)
	styleFieldValue = tui.Foreground(colourGrey)
)

// stateStyle maps a container state to the colour that conveys it at a glance:
// green for healthy, red for failed, yellow for in-between.
func stateStyle(state string) tui.Style {
	switch state {
	case "running":
		return tui.Foreground(colourGreen)
	case "exited", "dead":
		return tui.Foreground(colourRed)
	case "restarting", "removing":
		return tui.Foreground(colourYellow)
	case "paused":
		return tui.Foreground(colourBlue)
	default:
		return tui.Foreground(colourGrey)
	}
}

// projectStatusStyle colours a Compose project's aggregate status.
func projectStatusStyle(status string) tui.Style {
	switch status {
	case "running":
		return tui.Foreground(colourGreen)
	case "partial":
		return tui.Foreground(colourYellow)
	case "exited":
		return tui.Foreground(colourRed)
	default:
		return tui.Foreground(colourGrey)
	}
}

// healthStyle colours a healthcheck state.
func healthStyle(health docker.Health) tui.Style {
	switch health {
	case docker.HealthHealthy:
		return tui.Foreground(colourGreen)
	case docker.HealthUnhealthy:
		return tui.Foreground(colourRed)
	case docker.HealthStarting:
		return tui.Foreground(colourYellow)
	default:
		return tui.StyleNone
	}
}

// healthGlyph marks a healthcheck state with a distinct character, not only a
// colour. Colour alone would leave the distinction invisible under NO_COLOR
// and to anyone who cannot separate red from green.
func healthGlyph(health docker.Health) string {
	switch health {
	case docker.HealthHealthy:
		return "✓"
	case docker.HealthUnhealthy:
		return "✗"
	case docker.HealthStarting:
		return "…"
	default:
		return ""
	}
}

// levelStyle colours a log severity.
//
// Only the two that mean something is wrong get a strong colour. Painting INFO
// green as well would leave a healthy log fully coloured, and the point of the
// colouring is that the eye lands on the exceptions.
func levelStyle(level logfmt.Level) tui.Style {
	switch level {
	case logfmt.LevelFatal:
		return tui.Join(tui.StyleBold, tui.Foreground(colourRed))
	case logfmt.LevelError:
		return tui.Foreground(colourRed)
	case logfmt.LevelWarn:
		return tui.Foreground(colourYellow)
	case logfmt.LevelDebug, logfmt.LevelTrace:
		return tui.Foreground(colourGrey)
	default:
		return tui.Foreground(colourGrey)
	}
}

// methodStyles colours the HTTP verbs an access log is built around.
//
// The scale runs from cold to warm with how much damage the request can do:
// the reads are green and blue, the writes warm up through orange and yellow
// to violet, and DELETE is the red that everything else in this UI reserves
// for something being destroyed. Scanning a log for the request that broke
// something is mostly scanning for the verbs that change state.
var methodStyles = map[string]tui.Style{
	"GET":     tui.Foreground(colourGreen),
	"HEAD":    tui.Foreground(colourBlue),
	"OPTIONS": tui.Foreground(colourGrey),
	"POST":    tui.Foreground(colourOrange),
	"PUT":     tui.Foreground(colourYellow),
	"PATCH":   tui.Foreground(colourPurple),
	"DELETE":  tui.Foreground(colourRed),
}

// usageStyle colours a utilisation percentage, drawing attention only once a
// value is worth attention.
func usageStyle(percent float64) tui.Style {
	switch {
	case percent >= 90:
		return tui.Foreground(colourRed)
	case percent >= 70:
		return tui.Foreground(colourYellow)
	default:
		return tui.StyleNone
	}
}
