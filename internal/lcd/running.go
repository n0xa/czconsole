package lcd

import "image"

// drawRunningView is the shared live-run view: a centred status word, the subject
// (target/command), a note, and one or more buttons. Callers pass the footer
// hint, the buttons ([Cancel, Background] for one-shots; [Stop] for continuous),
// and the focused index.
func drawRunningView(c *Canvas, title, footer, status, subject, note string, buttons []string, focus int) {
	content := drawChrome(c, title, footer)
	small := c.Faces().Small

	y := content.Min.Y + 14
	c.TextCenteredIn(image.Rect(content.Min.X, y, content.Max.X, y+16), status, c.Faces().Body, colAccent)
	if subject != "" {
		cw := c.TextWidth(small, "M")
		if cw < 1 {
			cw = 6
		}
		c.TextCenteredIn(image.Rect(content.Min.X, y+22, content.Max.X, y+36),
			truncate(subject, (content.Dx()-12)/cw), small, colText)
	}
	c.TextCenteredIn(image.Rect(content.Min.X, y+40, content.Max.X, y+54), note, small, colDim)

	n := len(buttons)
	if n == 0 {
		return
	}
	const gap = 8
	bw := (content.Dx() - 12 - gap*(n-1)) / n
	by := content.Max.Y - 24
	for i, lab := range buttons {
		x := content.Min.X + 6 + i*(bw+gap)
		r := image.Rect(x, by, x+bw, by+18)
		if i == focus {
			c.FillRect(r, colAccent)
			c.TextCenteredIn(r, lab, small, colFocusTx)
		} else {
			c.Border(r, colBorder)
			c.TextCenteredIn(r, lab, small, colText)
		}
	}
}
