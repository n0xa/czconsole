package lcd

import "image"

// drawRunningView is the shared live-run view for one-shot tools of
// indeterminate length (nmap, gobuster): a centred status word, the subject
// being scanned (target/command), a "results will show when complete" note, and
// a Cancel/Background choice. runFocus selects the highlighted button
// (0 = Cancel, 1 = Background).
func drawRunningView(c *Canvas, title, status, subject string, runFocus int) {
	content := drawChrome(c, title, "tab:switch  ent:select  esc:bg")
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
	c.TextCenteredIn(image.Rect(content.Min.X, y+40, content.Max.X, y+54),
		"results will show when complete", small, colDim)

	labels := []string{"Cancel", "Background"}
	const gap = 8
	bw := (content.Dx() - 12 - gap) / 2
	by := content.Max.Y - 24
	for i, lab := range labels {
		x := content.Min.X + 6 + i*(bw+gap)
		r := image.Rect(x, by, x+bw, by+18)
		if i == runFocus {
			c.FillRect(r, colAccent)
			c.TextCenteredIn(r, lab, c.Faces().Small, colFocusTx)
		} else {
			c.Border(r, colBorder)
			c.TextCenteredIn(r, lab, c.Faces().Small, colText)
		}
	}
}
