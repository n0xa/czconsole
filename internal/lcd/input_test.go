package lcd

import (
	"testing"

	"github.com/n0xa/czconsole/internal/nmap"
)

// TestPrintableRune covers the keyboard decode: Sym-layer symbols, letter case
// via Shift, digits/space, and that real nav keys produce no character.
func TestPrintableRune(t *testing.T) {
	cases := []struct {
		code  uint16
		shift bool
		want  rune
	}{
		{196, false, '-'}, // Sym+R  (from the live capture)
		{197, false, '/'}, // Sym+T
		{231, false, ','}, // Sym+G
		{232, false, '.'}, // Sym+H
		{30, false, 'a'},  // KEY_A
		{30, true, 'A'},   // Shift+A
		{19, false, 'r'},  // KEY_R base letter (vs Sym+R above)
		{2, false, '1'},   // number row
		{57, false, ' '},  // space
		{103, false, 0},   // real Up arrow → not printable
		{28, false, 0},    // Enter → not printable
		{1, false, 0},     // Esc → not printable
	}
	for _, c := range cases {
		if got := printableRune(c.code, c.shift); got != c.want {
			t.Errorf("printableRune(%d, shift=%v) = %q, want %q", c.code, c.shift, got, c.want)
		}
	}
}

// TestConfigEditing exercises the options-form editor: typing (including the
// d-pad letters f/z/x/c, which arrive WITH a nav Key but a non-zero Rune), the
// real-arrow vs typed-letter distinction, focus toggle, backspace, and cursor
// insert. No core/poller involved (struct built directly).
func TestConfigEditing(t *testing.T) {
	s := &NmapScreen{core: nmap.New(), mode: modeConfig}

	typeStr := func(str string) {
		for _, r := range str {
			s.keyConfig(Event{Rune: r})
		}
	}
	typeStr("-sT ")
	// 'f' is a d-pad letter: arrives as {Key:KeyUp, Rune:'f'} and must type, not move.
	s.keyConfig(Event{Key: KeyUp, Rune: 'f'})
	if got := string(s.opts); got != "-sT f" {
		t.Fatalf("after typing incl. d-pad letter: opts=%q, want %q", got, "-sT f")
	}

	// A REAL Up arrow (Rune==0) must toggle focus to the checkbox, not type.
	s.keyConfig(Event{Key: KeyUp})
	if s.focus != 1 {
		t.Fatalf("real Up should focus checkbox, focus=%d", s.focus)
	}
	// Space on the checkbox toggles it (does not type into opts).
	s.keyConfig(Event{Rune: ' '})
	if !s.logErr || string(s.opts) != "-sT f" {
		t.Fatalf("space on checkbox: logErr=%v opts=%q", s.logErr, string(s.opts))
	}
	// Tab back to the field.
	s.keyConfig(Event{Key: KeyTab})
	if s.focus != 0 {
		t.Fatalf("tab should return to field, focus=%d", s.focus)
	}

	// Backspace removes the trailing 'f'.
	s.keyConfig(Event{Key: KeyBackspace})
	if string(s.opts) != "-sT " {
		t.Fatalf("after backspace: opts=%q", string(s.opts))
	}
	// Cursor left twice, then insert — must land mid-string.
	s.keyConfig(Event{Key: KeyLeft})
	s.keyConfig(Event{Key: KeyLeft})
	s.keyConfig(Event{Rune: 'X'})
	if string(s.opts) != "-sXT " {
		t.Fatalf("mid-string insert: opts=%q, want %q", string(s.opts), "-sXT ")
	}
}
