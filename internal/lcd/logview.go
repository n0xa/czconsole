package lcd

import (
	"image"
	"image/color"
)

// Line is one row of a LogView: its text and colour.
type Line struct {
	Text string
	Col  color.Color
}

// LogView is a read-only scrolling viewport for tool output. Up/Down (f/x) scroll
// lines vertically; Left/Right (z/c) PAN horizontally to read lines wider than
// the screen (instead of paginating). Immediate mode: Draw records the geometry
// that Key scrolls against.
type LogView struct {
	top     int // first visible line
	left    int // horizontal pan offset, in characters
	visible int // rows shown (from last Draw)
	total   int // total lines (from last Draw)
	cols    int // visible columns (from last Draw)
	maxLen  int // longest line in runes (from last Draw)
}

func (lv *LogView) Draw(c *Canvas, r image.Rectangle, lines []Line) {
	face := c.Faces().Small
	m := face.Metrics()
	lineH := m.Ascent.Round() + m.Descent.Round() + 1
	if lineH < 1 {
		lineH = 12
	}
	visible := r.Dy() / lineH
	if visible < 1 {
		visible = 1
	}
	lv.visible = visible
	lv.total = len(lines)

	charW := c.TextWidth(face, "M")
	if charW < 1 {
		charW = 6
	}
	overflow := len(lines) > visible
	avail := r.Dx() - 6
	if overflow {
		avail -= 4 // scrollbar gutter
	}
	cols := avail / charW
	if cols < 1 {
		cols = 1
	}
	lv.cols = cols

	maxLen := 0
	for i := range lines {
		if n := len([]rune(lines[i].Text)); n > maxLen {
			maxLen = n
		}
	}
	lv.maxLen = maxLen
	lv.clamp()

	for i := 0; i < visible; i++ {
		idx := lv.top + i
		if idx >= len(lines) {
			break
		}
		ln := lines[idx]
		rs := []rune(ln.Text)
		seg := ""
		if lv.left < len(rs) {
			end := lv.left + cols
			if end > len(rs) {
				end = len(rs)
			}
			seg = string(rs[lv.left:end])
		}
		col := ln.Col
		if col == nil {
			col = colText
		}
		c.Text(r.Min.X+4, r.Min.Y+i*lineH, seg, face, col)
	}

	if overflow {
		trackX := r.Max.X - 3
		track := image.Rect(trackX, r.Min.Y, trackX+2, r.Min.Y+visible*lineH)
		c.FillRect(track, colBorder)
		thumbH := track.Dy() * visible / len(lines)
		if thumbH < 4 {
			thumbH = 4
		}
		var pos int
		if maxTop := len(lines) - visible; maxTop > 0 {
			pos = (track.Dy() - thumbH) * lv.top / maxTop
		}
		c.FillRect(image.Rect(trackX, track.Min.Y+pos, trackX+2, track.Min.Y+pos+thumbH), colAccent)
	}
}

// hPanStep is the horizontal pan per keypress — small, since holding Left/Right
// auto-repeats (see input.go) for smooth panning.
const hPanStep = 2

// Key scrolls: Up/Down a line, Left/Right pan horizontally. Returns true if it
// consumed the key.
func (lv *LogView) Key(k Key) bool {
	switch k {
	case KeyDown:
		lv.top++
	case KeyUp:
		lv.top--
	case KeyRight:
		lv.left += hPanStep
	case KeyLeft:
		lv.left -= hPanStep
	default:
		return false
	}
	lv.clamp()
	return true
}

func (lv *LogView) clamp() {
	if maxTop := lv.total - lv.visible; lv.top > maxTop {
		lv.top = maxTop
	}
	if lv.top < 0 {
		lv.top = 0
	}
	maxLeft := lv.maxLen - lv.cols
	if maxLeft < 0 {
		maxLeft = 0
	}
	if lv.left > maxLeft {
		lv.left = maxLeft
	}
	if lv.left < 0 {
		lv.left = 0
	}
}

// truncate fits s into max characters, ellipsizing with '…' when too long (used
// by the running-view subject, not the LogView body, which pans instead).
func truncate(s string, max int) string {
	if max <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	if max == 1 {
		return "…"
	}
	return string(r[:max-1]) + "…"
}
