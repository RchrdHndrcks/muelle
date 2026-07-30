package ui

import (
	"strings"

	"github.com/RchrdHndrcks/muelle/internal/tui"
)

// OverlayKind identifies what an overlay is asking for.
type OverlayKind int

const (
	// OverlayConfirm asks a yes/no question before a destructive action.
	OverlayConfirm OverlayKind = iota
	// OverlayMenu offers a list of choices.
	OverlayMenu
	// OverlayInput collects a line of text.
	OverlayInput
)

// MenuItem is one selectable entry in a menu overlay.
type MenuItem struct {
	Label string
	// Detail is shown dimmed after the label, for example the command a
	// menu entry would run.
	Detail string
	// Value identifies the choice to the handler.
	Value any
}

// Overlay is a modal prompt drawn over the current view.
//
// Only one can be open at a time, which is deliberate: stacked modals in a
// terminal leave the user unsure what Escape will dismiss.
type Overlay struct {
	Kind   OverlayKind
	Title  string
	Prompt string
	Items  []MenuItem
	// Selected is the highlighted menu item.
	Selected int
	// Input is the text typed so far in an input overlay.
	Input string
	// Danger marks a confirmation for a destructive action, styling it as
	// a warning and defaulting to "no".
	Danger bool
	// OnAccept receives the chosen menu value, the typed text, or nil for
	// a confirmation.
	OnAccept func(value any)
}

// NewConfirm builds a yes/no overlay.
func NewConfirm(title, prompt string, danger bool, onAccept func(any)) *Overlay {
	return &Overlay{
		Kind:     OverlayConfirm,
		Title:    title,
		Prompt:   prompt,
		Danger:   danger,
		OnAccept: onAccept,
	}
}

// NewMenu builds a selection overlay.
func NewMenu(title string, items []MenuItem, onAccept func(any)) *Overlay {
	return &Overlay{Kind: OverlayMenu, Title: title, Items: items, OnAccept: onAccept}
}

// NewInput builds a text-entry overlay.
func NewInput(title, prompt, initial string, onAccept func(any)) *Overlay {
	return &Overlay{
		Kind:     OverlayInput,
		Title:    title,
		Prompt:   prompt,
		Input:    initial,
		OnAccept: onAccept,
	}
}

// HandleKey processes a key press. It reports whether the overlay should
// close, and whether the key was consumed.
//
// Consuming keys matters: while an overlay is open, "q" is a character to type
// or a rejected confirmation, never the quit command.
func (o *Overlay) HandleKey(key tui.Key) (closed, consumed bool) {
	switch o.Kind {
	case OverlayConfirm:
		switch {
		case key.IsRune('y'), key.IsRune('Y'):
			if o.OnAccept != nil {
				o.OnAccept(nil)
			}
			return true, true
		case key.IsRune('n'), key.IsRune('N'), key.Type == tui.KeyEscape, key.Type == tui.KeyCtrlC:
			return true, true
		case key.Type == tui.KeyEnter:
			// Enter confirms only for non-destructive prompts. For a
			// destructive one an explicit "y" is required, so a stray
			// Enter cannot delete anything.
			if !o.Danger {
				if o.OnAccept != nil {
					o.OnAccept(nil)
				}
				return true, true
			}
			return false, true
		}
		return false, true

	case OverlayMenu:
		switch {
		case key.Type == tui.KeyUp, key.IsRune('k'):
			o.Selected = clamp(o.Selected-1, len(o.Items))
		case key.Type == tui.KeyDown, key.IsRune('j'):
			o.Selected = clamp(o.Selected+1, len(o.Items))
		case key.Type == tui.KeyEnter:
			if o.OnAccept != nil && o.Selected < len(o.Items) {
				o.OnAccept(o.Items[o.Selected].Value)
			}
			return true, true
		case key.Type == tui.KeyEscape, key.Type == tui.KeyCtrlC, key.IsRune('q'):
			return true, true
		}
		return false, true

	case OverlayInput:
		switch {
		case key.Type == tui.KeyEnter:
			if o.OnAccept != nil {
				o.OnAccept(o.Input)
			}
			return true, true
		case key.Type == tui.KeyEscape, key.Type == tui.KeyCtrlC:
			return true, true
		case key.Type == tui.KeyBackspace:
			if o.Input != "" {
				runes := []rune(o.Input)
				o.Input = string(runes[:len(runes)-1])
			}
		case key.Type == tui.KeyCtrlU:
			o.Input = ""
		case key.Type == tui.KeyRune:
			o.Input += string(key.Rune)
		}
		return false, true
	}
	return false, false
}

// Render draws the overlay centred in a screen of the given size, returning
// the lines and the cursor position for input overlays (-1, -1 when the cursor
// should stay hidden).
func (o *Overlay) Render(screen *tui.Screen, width, height int) (lines []string, cursorCol, cursorRow int) {
	style := screen.Style

	body := o.bodyLines(style)
	boxWidth := o.width(body, width)
	boxHeight := len(body) + 4 // borders, title, padding

	top := (height - boxHeight) / 2
	if top < 0 {
		top = 0
	}
	left := (width - boxWidth) / 2
	if left < 0 {
		left = 0
	}
	indent := strings.Repeat(" ", left)

	border := styleAccent
	if o.Danger {
		border = styleError
	}

	lines = make([]string, 0, boxHeight)
	lines = append(lines, indent+style(border, "┌"+strings.Repeat("─", boxWidth-2)+"┐"))
	lines = append(lines, indent+style(border, "│")+" "+
		tui.Pad(style(tui.StyleBold, o.Title), boxWidth-4)+" "+style(border, "│"))
	lines = append(lines, indent+style(border, "├"+strings.Repeat("─", boxWidth-2)+"┤"))

	for _, line := range body {
		lines = append(lines, indent+style(border, "│")+" "+
			tui.Pad(line, boxWidth-4)+" "+style(border, "│"))
	}
	lines = append(lines, indent+style(border, "└"+strings.Repeat("─", boxWidth-2)+"┘"))

	if o.Kind == OverlayInput {
		// Place the caret after the typed text, on the body's first row.
		cursorCol = left + 2 + tui.VisibleWidth(o.Prompt) + 1 + tui.VisibleWidth(o.Input)
		cursorRow = top + 3
		return o.pad(lines, top), cursorCol, cursorRow
	}
	return o.pad(lines, top), -1, -1
}

// pad prepends blank lines so the box lands at the right vertical offset.
func (o *Overlay) pad(lines []string, top int) []string {
	if top <= 0 {
		return lines
	}
	padded := make([]string, 0, top+len(lines))
	for range top {
		padded = append(padded, "")
	}
	return append(padded, lines...)
}

// bodyLines renders the overlay's content, excluding the frame.
func (o *Overlay) bodyLines(style func(tui.Style, string) string) []string {
	switch o.Kind {
	case OverlayConfirm:
		hint := "y = yes    n = no"
		if o.Danger {
			hint = style(styleWarning, "y = yes") + "    n = no    (Enter will not confirm)"
		}
		// Split, because a prompt can carry a diagnostic that another
		// command wrote across several lines. Measuring it as one line
		// would size the box to the whole thing.
		return append(strings.Split(o.Prompt, "\n"), "", hint)

	case OverlayMenu:
		lines := make([]string, 0, len(o.Items)+2)
		for i, item := range o.Items {
			marker := "  "
			label := item.Label
			if i == o.Selected {
				marker = style(styleAccent, "▸ ")
				label = style(tui.StyleBold, label)
			}
			line := marker + label
			if item.Detail != "" {
				line += "  " + style(styleMuted, item.Detail)
			}
			lines = append(lines, line)
		}
		return append(lines, "", style(styleMuted, "Enter = run    Esc = cancel"))

	case OverlayInput:
		return []string{
			o.Prompt + " " + o.Input,
			"",
			style(styleMuted, "Enter = apply    Esc = cancel    Ctrl-U = clear"),
		}
	}
	return nil
}

// width computes the box width needed for the body, bounded by the screen.
func (o *Overlay) width(body []string, screenWidth int) int {
	widest := tui.VisibleWidth(o.Title)
	for _, line := range body {
		if w := tui.VisibleWidth(line); w > widest {
			widest = w
		}
	}
	boxWidth := widest + 4
	if maximum := screenWidth - 4; boxWidth > maximum {
		boxWidth = maximum
	}
	if boxWidth < 24 {
		boxWidth = 24
	}
	if boxWidth > screenWidth {
		boxWidth = screenWidth
	}
	return boxWidth
}
