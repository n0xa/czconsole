package lcd

import (
	"image"
	"image/color"
)

// czconsole green-on-black field-console palette (matches the web/LCD design).
var (
	colBg      = color.RGBA{0x07, 0x0b, 0x09, 0xff}
	colBar     = color.RGBA{0x04, 0x25, 0x0f, 0xff}
	colTitle   = color.RGBA{0xbf, 0xff, 0xd6, 0xff}
	colText    = color.RGBA{0xdc, 0xf5, 0xe6, 0xff}
	colDim     = color.RGBA{0x4f, 0x9e, 0x6f, 0xff}
	colAccent  = color.RGBA{0x3d, 0xff, 0x6a, 0xff}
	colCardBg  = color.RGBA{0x0d, 0x17, 0x12, 0xff}
	colBorder  = color.RGBA{0x16, 0x34, 0x1f, 0xff}
	colFocusTx = color.RGBA{0x00, 0x12, 0x06, 0xff}
)

const (
	titleH  = 18
	footerH = 14
)

// drawChrome paints the background, title bar, and footer, returning the content
// rectangle between them.
func drawChrome(c *Canvas, title, footer string) image.Rectangle {
	b := c.Bounds()
	c.Fill(colBg)

	tb := image.Rect(b.Min.X, b.Min.Y, b.Max.X, b.Min.Y+titleH)
	c.FillRect(tb, colBar)
	c.Text(tb.Min.X+6, tb.Min.Y+1, title, c.Faces().Body, colTitle)

	fb := image.Rect(b.Min.X, b.Max.Y-footerH, b.Max.X, b.Max.Y)
	c.FillRect(fb, colBar)
	if footer != "" {
		c.Text(fb.Min.X+6, fb.Min.Y+1, footer, c.Faces().Small, colDim)
	}

	return image.Rect(b.Min.X, tb.Max.Y, b.Max.X, fb.Min.Y)
}

// pill draws a small bordered label box at (x,y) and returns its width, so the
// caller can lay the next one out.
func pill(c *Canvas, x, y int, text string, txt color.Color) int {
	w := c.TextWidth(c.Faces().Small, text) + 12
	r := image.Rect(x, y, x+w, y+14)
	c.Border(r, colBorder)
	c.TextCenteredIn(r, text, c.Faces().Small, txt)
	return w
}

// statCard draws one wardrive stat: big value over a dim label in a bordered box.
func statCard(c *Canvas, r image.Rectangle, value, label string, accent bool) {
	c.FillRect(r, colCardBg)
	c.Border(r, colBorder)

	valCol := color.Color(colText)
	if accent {
		valCol = colAccent
	}
	vw := c.TextWidth(c.Faces().Big, value)
	c.Text(r.Min.X+(r.Dx()-vw)/2, r.Min.Y+r.Dy()/2-16, value, c.Faces().Big, valCol)

	lw := c.TextWidth(c.Faces().Small, label)
	c.Text(r.Min.X+(r.Dx()-lw)/2, r.Max.Y-15, label, c.Faces().Small, colDim)
}
