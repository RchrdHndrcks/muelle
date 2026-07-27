package ui

import (
	"strings"

	"github.com/RchrdHndrcks/muelle/internal/tui"
)

// helpEntry is one key binding in the reference.
type helpEntry struct {
	keys        string
	description string
}

// helpSection groups related bindings.
type helpSection struct {
	title   string
	entries []helpEntry
}

// helpSections is the full key reference.
var helpSections = []helpSection{
	{"Navigation", []helpEntry{
		{"j / k, ↓ / ↑", "move selection"},
		{"g / G", "jump to first / last"},
		{"Ctrl-d / Ctrl-u", "half page down / up"},
		{"1 2 3 4 5", "containers / compose / images / volumes / networks"},
		{"Tab, → / ←", "cycle views"},
		{"/", "filter the list"},
		{"Esc", "clear filter, or leave the current view"},
		{"S", "toggle the host metrics panel"},
		{"Ctrl-r", "refresh now"},
		{"? ", "toggle this help"},
		{"q, Ctrl-c", "quit"},
	}},
	{"Containers", []helpEntry{
		{"Enter, i", "inspect (full JSON)"},
		{"l", "follow logs"},
		{"x", "exec menu (database clients, shells)"},
		{"e", "shell straight away (bash, falling back to sh)"},
		{"s / t / r", "start / stop / restart"},
		{"p", "pause or unpause"},
		{"K", "kill (SIGKILL)"},
		{"D", "remove"},
		{"a", "include stopped containers"},
	}},
	{"Compose", []helpEntry{
		{"Enter", "action menu for the project"},
		{"u", "up -d"},
		{"d", "down (asks first)"},
		{"r", "restart"},
		{"l", "follow logs for every service at once"},
	}},
	{"Images, volumes, networks", []helpEntry{
		{"D", "remove the selected item"},
		{"P", "prune unused items"},
	}},
	{"Log viewer", []helpEntry{
		{"f", "toggle follow (scrolling up pauses it)"},
		{"w", "toggle wrapping"},
		{"t", "toggle timestamps"},
		{"/", "filter lines"},
		{"c", "clear the buffer"},
		{"Esc, q", "back to the list"},
	}},
}

// renderHelp draws the key reference.
func (a *App) renderHelp(width, height int) []string {
	style := a.screen.Style
	lines := make([]string, 0, height)

	lines = append(lines, "")
	lines = append(lines, "  "+style(tui.Join(tui.StyleBold, tui.Foreground(colourPurple)), "muelle")+
		style(styleMuted, " — terminal UI for docker"))
	lines = append(lines, "")

	// Two columns when there is room; one when there is not.
	columnWidth := 46
	twoColumns := width >= columnWidth*2+6

	var left, right []string
	for i, section := range helpSections {
		rendered := renderHelpSection(style, section, columnWidth)
		if twoColumns && i%2 == 1 {
			right = append(right, rendered...)
			continue
		}
		left = append(left, rendered...)
	}

	if !twoColumns {
		lines = append(lines, left...)
		lines = append(lines, right...)
		return append(lines, "", "  "+style(styleMuted, "Press Esc to go back."))
	}

	for i := 0; i < len(left) || i < len(right); i++ {
		var leftText, rightText string
		if i < len(left) {
			leftText = left[i]
		}
		if i < len(right) {
			rightText = right[i]
		}
		lines = append(lines, "  "+tui.Pad(leftText, columnWidth)+"  "+rightText)
	}
	return append(lines, "", "  "+style(styleMuted, "Press Esc to go back."))
}

// renderHelpSection renders one titled group of bindings.
func renderHelpSection(style func(tui.Style, string) string, section helpSection, width int) []string {
	lines := []string{style(tui.Join(tui.StyleBold, tui.Foreground(colourBlue)), section.title)}
	for _, entry := range section.entries {
		key := tui.Pad(style(styleKey, strings.TrimSpace(entry.keys)), 18)
		lines = append(lines, key+style(styleMuted, entry.description))
	}
	return append(lines, "")
}
