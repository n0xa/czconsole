package lcd

import "image"

// textField is a single-line editable text input: a rune buffer + a caret, with
// horizontal windowing so the caret stays visible when the text overflows the
// box. Screens compose one or more (the gobuster form has three) and route edit
// events to the focused one.
type textField struct {
	buf    []rune
	cursor int
}

func newTextField(initial string) *textField {
	r := []rune(initial)
	return &textField{buf: r, cursor: len(r)}
}

func (t *textField) String() string { return string(t.buf) }

// edit applies a printable rune (insert) or an editing key (backspace, cursor
// left/right). Returns true if it consumed the event. A non-zero Rune is always
// a character — that's how a typed d-pad letter (f/z/x/c, which also carry a nav
// Key) is told apart from a real arrow (Rune == 0 → cursor move).
func (t *textField) edit(ev Event) bool {
	switch {
	case ev.Rune != 0:
		t.buf = append(t.buf[:t.cursor], append([]rune{ev.Rune}, t.buf[t.cursor:]...)...)
		t.cursor++
	case ev.Key == KeyBackspace:
		if t.cursor > 0 {
			t.buf = append(t.buf[:t.cursor-1], t.buf[t.cursor:]...)
			t.cursor--
		}
	case ev.Key == KeyLeft:
		if t.cursor > 0 {
			t.cursor--
		}
	case ev.Key == KeyRight:
		if t.cursor < len(t.buf) {
			t.cursor++
		}
	default:
		return false
	}
	return true
}

// draw renders the field as a bordered box (accent border + caret when focused),
// windowing the text so the caret is on-screen.
func (t *textField) draw(c *Canvas, r image.Rectangle, focused bool) {
	border := colBorder
	if focused {
		border = colAccent
	}
	c.FillRect(r, colCardBg)
	c.Border(r, border)

	face := c.Faces().Small
	charW := c.TextWidth(face, "M")
	if charW < 1 {
		charW = 6
	}
	maxChars := (r.Dx() - 8) / charW
	if maxChars < 1 {
		maxChars = 1
	}
	start := 0
	if t.cursor > maxChars-1 {
		start = t.cursor - (maxChars - 1)
	}
	end := start + maxChars
	if end > len(t.buf) {
		end = len(t.buf)
	}
	c.Text(r.Min.X+4, r.Min.Y+3, string(t.buf[start:end]), face, colText)
	if focused {
		cx := r.Min.X + 4 + (t.cursor-start)*charW
		c.FillRect(image.Rect(cx, r.Min.Y+3, cx+1, r.Max.Y-3), colAccent)
	}
}
