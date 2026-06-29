package lcd

import (
	"image"
	"image/color"
	"image/draw"
	"log"

	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gomono"
	"golang.org/x/image/font/gofont/gomonobold"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
)

// Faces holds the mono faces the UI draws with. Go Mono is embedded (no font
// files on disk) and cross-compiles cleanly.
type Faces struct {
	Small font.Face // ~11px — labels, footer
	Body  font.Face // ~14px — menu rows, titles
	Big   font.Face // ~20px — stat numbers
}

func mustFace(ttf []byte, px float64) font.Face {
	f, err := opentype.Parse(ttf)
	if err != nil {
		log.Fatalf("lcd: parse font: %v", err)
	}
	face, err := opentype.NewFace(f, &opentype.FaceOptions{Size: px, DPI: 72, Hinting: font.HintingFull})
	if err != nil {
		log.Fatalf("lcd: new face: %v", err)
	}
	return face
}

func loadFaces() Faces {
	return Faces{
		Small: mustFace(gomono.TTF, 11),
		Body:  mustFace(gomonobold.TTF, 14),
		Big:   mustFace(gomonobold.TTF, 20),
	}
}

// Canvas is an off-screen RGBA buffer the screens draw into; the app blits it to
// the framebuffer each frame.
type Canvas struct {
	img   *image.RGBA
	faces Faces
}

func newCanvas(bounds image.Rectangle, faces Faces) *Canvas {
	return &Canvas{img: image.NewRGBA(bounds), faces: faces}
}

func (c *Canvas) Bounds() image.Rectangle { return c.img.Bounds() }
func (c *Canvas) Faces() Faces            { return c.faces }

// Fill paints the whole canvas one color.
func (c *Canvas) Fill(col color.Color) {
	draw.Draw(c.img, c.img.Bounds(), image.NewUniform(col), image.Point{}, draw.Src)
}

// FillRect fills a rectangle.
func (c *Canvas) FillRect(r image.Rectangle, col color.Color) {
	draw.Draw(c.img, r.Intersect(c.img.Bounds()), image.NewUniform(col), image.Point{}, draw.Src)
}

// Border draws a 1px outline inside r.
func (c *Canvas) Border(r image.Rectangle, col color.Color) {
	c.FillRect(image.Rect(r.Min.X, r.Min.Y, r.Max.X, r.Min.Y+1), col)
	c.FillRect(image.Rect(r.Min.X, r.Max.Y-1, r.Max.X, r.Max.Y), col)
	c.FillRect(image.Rect(r.Min.X, r.Min.Y, r.Min.X+1, r.Max.Y), col)
	c.FillRect(image.Rect(r.Max.X-1, r.Min.Y, r.Max.X, r.Max.Y), col)
}

// TextWidth measures a string in the given face.
func (c *Canvas) TextWidth(face font.Face, s string) int {
	return font.MeasureString(face, s).Round()
}

// Text draws s with its top-left at (x, y).
func (c *Canvas) Text(x, y int, s string, face font.Face, col color.Color) {
	m := face.Metrics()
	d := &font.Drawer{
		Dst:  c.img,
		Src:  image.NewUniform(col),
		Face: face,
		Dot:  fixed.P(x, y+m.Ascent.Round()),
	}
	d.DrawString(s)
}

// TextCenteredIn draws s centered within r (both axes).
func (c *Canvas) TextCenteredIn(r image.Rectangle, s string, face font.Face, col color.Color) {
	m := face.Metrics()
	tw := c.TextWidth(face, s)
	th := m.Ascent.Round() + m.Descent.Round()
	x := r.Min.X + (r.Dx()-tw)/2
	y := r.Min.Y + (r.Dy()-th)/2
	c.Text(x, y, s, face, col)
}
