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

// LogView is a read-only, vertically-scrolling text viewport — the repeatable
// pattern for rendering tool output on the tiny screen (nmap results now;
// gobuster/tcpdump later). It owns no chrome: a screen hands it a content rect
// and the lines each frame and forwards scroll keys. Wide lines ellipsize (the
// content is pre-fit by the caller); a scrollbar shows when it overflows.
//
// Immediate mode: Draw records the visible/total counts for Key to scroll
// against, since the rect is only known at draw time.
type LogView struct {
	top     int
	visible int
	total   int
}

// Top reports the current scroll offset (first visible line index).
func (lv *LogView) Top() int { return lv.top }

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
	lv.clamp()

	charW := c.TextWidth(face, "M")
	if charW < 1 {
		charW = 6
	}
	overflow := len(lines) > visible
	avail := r.Dx() - 6
	if overflow {
		avail -= 4 // scrollbar gutter
	}
	maxChars := avail / charW
	if maxChars < 1 {
		maxChars = 1
	}

	for i := 0; i < visible; i++ {
		idx := lv.top + i
		if idx >= len(lines) {
			break
		}
		ln := lines[idx]
		col := ln.Col
		if col == nil {
			col = colText
		}
		c.Text(r.Min.X+4, r.Min.Y+i*lineH, truncate(ln.Text, maxChars), face, col)
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

// Key scrolls (line on Up/Down, page on Left/Right). Returns true if it consumed
// the key.
func (lv *LogView) Key(k Key) bool {
	switch k {
	case KeyDown:
		lv.top++
	case KeyUp:
		lv.top--
	case KeyRight:
		lv.top += lv.visible
	case KeyLeft:
		lv.top -= lv.visible
	default:
		return false
	}
	lv.clamp()
	return true
}

func (lv *LogView) clamp() {
	maxTop := lv.total - lv.visible
	if maxTop < 0 {
		maxTop = 0
	}
	if lv.top > maxTop {
		lv.top = maxTop
	}
	if lv.top < 0 {
		lv.top = 0
	}
}

// truncate fits s into max characters, ellipsizing with '…' when it's too long.
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
