package ui

import (
	"testing"

	"github.com/RchrdHndrcks/muelle/internal/tui"
)

func runeKey(r rune) tui.Key        { return tui.Key{Type: tui.KeyRune, Rune: r} }
func typeKey(t tui.KeyType) tui.Key { return tui.Key{Type: t} }

func TestConfirmAcceptsOnYes(t *testing.T) {
	accepted := false
	overlay := NewConfirm("Remove", "Really?", true, func(any) { accepted = true })

	closed, consumed := overlay.HandleKey(runeKey('y'))

	if !accepted || !closed || !consumed {
		t.Errorf("accepted=%v closed=%v consumed=%v, want all true", accepted, closed, consumed)
	}
}

func TestConfirmRejectsOnNo(t *testing.T) {
	accepted := false
	overlay := NewConfirm("Remove", "Really?", true, func(any) { accepted = true })

	closed, _ := overlay.HandleKey(runeKey('n'))

	if accepted {
		t.Error("n should not run the action")
	}
	if !closed {
		t.Error("n should close the overlay")
	}
}

func TestConfirmClosesOnEscape(t *testing.T) {
	accepted := false
	overlay := NewConfirm("Remove", "Really?", true, func(any) { accepted = true })

	closed, _ := overlay.HandleKey(typeKey(tui.KeyEscape))

	if accepted || !closed {
		t.Errorf("accepted=%v closed=%v, want cancelled", accepted, closed)
	}
}

// A stray Enter must never delete anything; destructive prompts demand an
// explicit "y".
func TestDangerousConfirmIgnoresEnter(t *testing.T) {
	accepted := false
	overlay := NewConfirm("Remove", "Really?", true, func(any) { accepted = true })

	closed, consumed := overlay.HandleKey(typeKey(tui.KeyEnter))

	if accepted {
		t.Error("Enter must not confirm a destructive action")
	}
	if closed {
		t.Error("the prompt should stay open")
	}
	if !consumed {
		t.Error("the key should still be consumed rather than reaching the list")
	}
}

func TestHarmlessConfirmAcceptsEnter(t *testing.T) {
	accepted := false
	overlay := NewConfirm("Reload", "Reload config?", false, func(any) { accepted = true })

	closed, _ := overlay.HandleKey(typeKey(tui.KeyEnter))

	if !accepted || !closed {
		t.Errorf("accepted=%v closed=%v, want Enter to confirm a safe action", accepted, closed)
	}
}

// While a prompt is open, q is an answer or a character — never the quit
// command.
func TestConfirmConsumesEveryKey(t *testing.T) {
	overlay := NewConfirm("Remove", "Really?", true, func(any) {})

	for _, key := range []tui.Key{runeKey('q'), runeKey('j'), runeKey('x'), typeKey(tui.KeyTab)} {
		if _, consumed := overlay.HandleKey(key); !consumed {
			t.Errorf("key %+v leaked through to the list underneath", key)
		}
	}
}

func TestMenuNavigatesAndSelects(t *testing.T) {
	var chosen any
	overlay := NewMenu("Exec", []MenuItem{
		{Label: "mysql", Value: "mysql"},
		{Label: "bash", Value: "bash"},
	}, func(value any) { chosen = value })

	overlay.HandleKey(runeKey('j'))
	closed, _ := overlay.HandleKey(typeKey(tui.KeyEnter))

	if chosen != "bash" {
		t.Errorf("got %v, want the second item", chosen)
	}
	if !closed {
		t.Error("selecting should close the menu")
	}
}

func TestMenuSelectionStopsAtTheEnds(t *testing.T) {
	overlay := NewMenu("Exec", []MenuItem{{Label: "one"}, {Label: "two"}}, func(any) {})

	overlay.HandleKey(runeKey('k'))
	if overlay.Selected != 0 {
		t.Errorf("got %d, want to stay at the top", overlay.Selected)
	}

	overlay.HandleKey(runeKey('j'))
	overlay.HandleKey(runeKey('j'))
	overlay.HandleKey(runeKey('j'))
	if overlay.Selected != 1 {
		t.Errorf("got %d, want to stay at the last item", overlay.Selected)
	}
}

func TestMenuCancelsWithoutChoosing(t *testing.T) {
	chosen := false
	overlay := NewMenu("Exec", []MenuItem{{Label: "one"}}, func(any) { chosen = true })

	closed, _ := overlay.HandleKey(typeKey(tui.KeyEscape))

	if chosen || !closed {
		t.Errorf("chosen=%v closed=%v, want cancelled", chosen, closed)
	}
}

func TestInputCollectsTypedText(t *testing.T) {
	var submitted any
	overlay := NewInput("Filter", "match:", "", func(value any) { submitted = value })

	for _, r := range "web" {
		overlay.HandleKey(runeKey(r))
	}
	closed, _ := overlay.HandleKey(typeKey(tui.KeyEnter))

	if submitted != "web" {
		t.Errorf("got %v, want the typed text", submitted)
	}
	if !closed {
		t.Error("Enter should close the input")
	}
}

func TestInputBackspaceDeletesOneRune(t *testing.T) {
	overlay := NewInput("Filter", "match:", "añb", nil)

	overlay.HandleKey(typeKey(tui.KeyBackspace))

	// Deleting by byte rather than rune would corrupt the multi-byte "ñ".
	if overlay.Input != "añ" {
		t.Errorf("got %q, want %q", overlay.Input, "añ")
	}
}

func TestInputBackspaceOnEmptyIsHarmless(t *testing.T) {
	overlay := NewInput("Filter", "match:", "", nil)

	overlay.HandleKey(typeKey(tui.KeyBackspace))

	if overlay.Input != "" {
		t.Errorf("got %q, want it still empty", overlay.Input)
	}
}

func TestInputCtrlUClearsEverything(t *testing.T) {
	overlay := NewInput("Filter", "match:", "some text", nil)

	overlay.HandleKey(typeKey(tui.KeyCtrlU))

	if overlay.Input != "" {
		t.Errorf("got %q, want it cleared", overlay.Input)
	}
}

// Typing "q" into a filter must not quit the app.
func TestInputTreatsLettersAsText(t *testing.T) {
	overlay := NewInput("Filter", "match:", "", nil)

	closed, consumed := overlay.HandleKey(runeKey('q'))

	if closed {
		t.Error("q should be typed, not treated as quit")
	}
	if !consumed {
		t.Error("the key should be consumed")
	}
	if overlay.Input != "q" {
		t.Errorf("got %q, want the letter appended", overlay.Input)
	}
}

func TestInputEscapeCancelsWithoutSubmitting(t *testing.T) {
	submitted := false
	overlay := NewInput("Filter", "match:", "text", func(any) { submitted = true })

	closed, _ := overlay.HandleKey(typeKey(tui.KeyEscape))

	if submitted || !closed {
		t.Errorf("submitted=%v closed=%v, want cancelled", submitted, closed)
	}
}
