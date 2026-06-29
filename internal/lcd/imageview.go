package lcd

import (
	"encoding/binary"
	"fmt"
	"image"
	"io"
	"os"
	"strings"
	"sync"

	"github.com/n0xa/czconsole/internal/img"
)

const (
	ivConverting = iota
	ivReady
	ivError
)

// panStep is the pan distance per keypress; held keys auto-repeat (input.go) so
// this stays small enough for fine positioning yet flies when held.
const panStep = 32

// ImageView pans around an image far too large to hold in memory (e.g. an
// rtl_power heatmap). On open it ensures a flat, seekable .r565 exists next to
// the source PNG (converting it once, streamed, if the cache is stale), then each
// frame reads only the on-screen window via pread — bounded memory at any size.
type ImageView struct {
	pngPath  string
	r565Path string

	mu       sync.Mutex
	state    int
	progress float64
	errMsg   string
	f        *os.File
	imgW     int
	imgH     int

	// pan offset — UI-goroutine only
	panX, panY int
}

func NewImageView(pngPath string) *ImageView {
	iv := &ImageView{pngPath: pngPath, r565Path: strings.TrimSuffix(pngPath, ".png") + ".r565"}
	if iv.cacheValid() && iv.open() == nil {
		iv.state = ivReady
	} else {
		iv.state = ivConverting
		go iv.convert()
	}
	return iv
}

func (iv *ImageView) cacheValid() bool {
	ri, err := os.Stat(iv.r565Path)
	if err != nil {
		return false
	}
	pi, err := os.Stat(iv.pngPath)
	if err != nil {
		return false
	}
	return !ri.ModTime().Before(pi.ModTime()) // cache at least as new as the PNG
}

func (iv *ImageView) open() error {
	f, err := os.Open(iv.r565Path)
	if err != nil {
		return err
	}
	var hdr [img.HeaderSize]byte
	if _, err := io.ReadFull(f, hdr[:]); err != nil || string(hdr[0:4]) != img.Magic {
		f.Close()
		return fmt.Errorf("bad .r565 cache")
	}
	iv.f = f
	iv.imgW = int(binary.LittleEndian.Uint32(hdr[8:12]))
	iv.imgH = int(binary.LittleEndian.Uint32(hdr[12:16]))
	return nil
}

func (iv *ImageView) convert() {
	_, _, err := img.PNGToR565(iv.pngPath, iv.r565Path, func(f float64) {
		iv.mu.Lock()
		iv.progress = f
		iv.mu.Unlock()
	})
	iv.mu.Lock()
	switch {
	case err != nil:
		iv.state, iv.errMsg = ivError, err.Error()
	case iv.open() != nil:
		iv.state, iv.errMsg = ivError, "open cache failed"
	default:
		iv.state = ivReady
	}
	iv.mu.Unlock()
}

func (iv *ImageView) Close() {
	iv.mu.Lock()
	if iv.f != nil {
		iv.f.Close()
		iv.f = nil
	}
	iv.mu.Unlock()
}

func (iv *ImageView) Draw(c *Canvas) {
	iv.mu.Lock()
	state, prog, errMsg, f, w, h := iv.state, iv.progress, iv.errMsg, iv.f, iv.imgW, iv.imgH
	iv.mu.Unlock()

	switch state {
	case ivConverting:
		content := drawChrome(c, "HEATMAP", "preparing…  esc:back")
		c.TextCenteredIn(image.Rect(content.Min.X, content.Min.Y, content.Max.X, content.Min.Y+content.Dy()/2),
			fmt.Sprintf("preparing image…  %.0f%%", prog*100), c.Faces().Body, colAccent)
		bar := image.Rect(content.Min.X+20, content.Min.Y+content.Dy()/2+4, content.Max.X-20, content.Min.Y+content.Dy()/2+14)
		c.Border(bar, colBorder)
		fw := int(float64(bar.Dx()-2) * prog)
		c.FillRect(image.Rect(bar.Min.X+1, bar.Min.Y+1, bar.Min.X+1+fw, bar.Max.Y-1), colAccent)
	case ivError:
		content := drawChrome(c, "HEATMAP", "esc:back")
		c.TextCenteredIn(content, "! "+errMsg, c.Faces().Small, colAccent)
	case ivReady:
		iv.drawImage(c, f, w, h)
	}
}

func (iv *ImageView) drawImage(c *Canvas, f *os.File, imgW, imgH int) {
	b := c.Bounds()
	const footerH = 12
	view := image.Rect(b.Min.X, b.Min.Y, b.Max.X, b.Max.Y-footerH)
	vw, vh := view.Dx(), view.Dy()

	// clamp pan so the window stays on the image
	maxX, maxY := max0(imgW-vw), max0(imgH-vh)
	iv.panX = clampi(iv.panX, 0, maxX)
	iv.panY = clampi(iv.panY, 0, maxY)

	c.FillRect(view, colBg)
	rowBuf := make([]byte, vw*2)
	for ry := 0; ry < vh; ry++ {
		sy := iv.panY + ry
		if sy >= imgH {
			break
		}
		n := vw
		if iv.panX+n > imgW {
			n = imgW - iv.panX
		}
		off := int64(img.HeaderSize + (sy*imgW+iv.panX)*2)
		if _, err := f.ReadAt(rowBuf[:n*2], off); err != nil && err != io.EOF {
			continue
		}
		blitR565Row(c.img, view.Min.X, view.Min.Y+ry, rowBuf[:n*2])
	}

	footer := image.Rect(b.Min.X, b.Max.Y-footerH, b.Max.X, b.Max.Y)
	c.FillRect(footer, colBar)
	c.Text(footer.Min.X+4, footer.Min.Y+1,
		fmt.Sprintf("%d,%d  %dx%d  f/z/x/c:pan  esc:back", iv.panX, iv.panY, imgW, imgH),
		c.Faces().Small, colDim)
}

func (iv *ImageView) Key(ev Event) (Action, Screen) {
	if ev.Key == KeyBack {
		return ActPop, nil
	}
	iv.mu.Lock()
	ready := iv.state == ivReady
	iv.mu.Unlock()
	if !ready {
		return ActNone, nil
	}
	switch ev.Key {
	case KeyLeft:
		iv.panX -= panStep
	case KeyRight:
		iv.panX += panStep
	case KeyUp:
		iv.panY -= panStep
	case KeyDown:
		iv.panY += panStep
	}
	return ActNone, nil
}

// blitR565Row writes a row of little-endian RGB565 pixels as RGBA into dst at
// (x,y) — straight into the canvas's pixel buffer.
func blitR565Row(dst *image.RGBA, x, y int, src []byte) {
	o := dst.PixOffset(x, y)
	for i := 0; i+1 < len(src); i += 2 {
		v := uint16(src[i]) | uint16(src[i+1])<<8
		r := byte(v>>11) & 0x1f
		g := byte(v>>5) & 0x3f
		bl := byte(v) & 0x1f
		dst.Pix[o] = r<<3 | r>>2
		dst.Pix[o+1] = g<<2 | g>>4
		dst.Pix[o+2] = bl<<3 | bl>>2
		dst.Pix[o+3] = 0xff
		o += 4
	}
}

func max0(x int) int {
	if x < 0 {
		return 0
	}
	return x
}
func clampi(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
