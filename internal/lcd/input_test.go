package lcd

import "testing"

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

// TestTextFieldEdit covers the reusable editor: typing (including the d-pad
// letters f/z/x/c, which arrive WITH a nav Key but a non-zero Rune and must
// insert), real-arrow cursor movement (Rune==0), and backspace.
func TestTextFieldEdit(t *testing.T) {
	tf := newTextField("")
	for _, r := range "-sT " {
		tf.edit(Event{Rune: r})
	}
	// 'f' is a d-pad letter: {Key:KeyUp, Rune:'f'} → must type, not move.
	tf.edit(Event{Key: KeyUp, Rune: 'f'})
	if tf.String() != "-sT f" {
		t.Fatalf("after typing incl. d-pad letter: %q, want %q", tf.String(), "-sT f")
	}
	// A REAL Left arrow (Rune==0) moves the cursor (before 'f'); then insert.
	tf.edit(Event{Key: KeyLeft})
	tf.edit(Event{Rune: 'X'})
	if tf.String() != "-sT Xf" {
		t.Fatalf("mid-string insert: %q, want %q", tf.String(), "-sT Xf")
	}
	// Backspace removes the char before the cursor (the X).
	tf.edit(Event{Key: KeyBackspace})
	if tf.String() != "-sT f" {
		t.Fatalf("after backspace: %q, want %q", tf.String(), "-sT f")
	}
}
