package img

import (
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

// TestPNGToR565 round-trips a small RGB image: PNG-encode it (Go picks adaptive
// per-row filters, so this exercises Sub/Up/Average/Paeth), convert, and verify
// every pixel's RGB565 matches the source. PNG is lossless, so the only loss is
// the deliberate 565 quantization, which we reproduce in `want`.
func TestPNGToR565(t *testing.T) {
	const w, h = 29, 17
	src := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			src.SetRGBA(x, y, color.RGBA{uint8(x * 9), uint8(y * 13), uint8((x + y) * 5), 255})
		}
	}

	dir := t.TempDir()
	pngPath := filepath.Join(dir, "t.png")
	f, err := os.Create(pngPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(f, src); err != nil {
		t.Fatal(err)
	}
	f.Close()

	outPath := filepath.Join(dir, "t.r565")
	gw, gh, err := PNGToR565(pngPath, outPath, nil)
	if err != nil {
		t.Fatalf("PNGToR565: %v", err)
	}
	if gw != w || gh != h {
		t.Fatalf("dims = %dx%d, want %dx%d", gw, gh, w, h)
	}

	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data[:4]) != Magic {
		t.Fatalf("bad magic %q", data[:4])
	}
	if len(data) != HeaderSize+w*h*2 {
		t.Fatalf("size = %d, want %d", len(data), HeaderSize+w*h*2)
	}
	pix := data[HeaderSize:]
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			p := src.RGBAAt(x, y)
			want := uint16(p.R>>3)<<11 | uint16(p.G>>2)<<5 | uint16(p.B>>3)
			got := binary.LittleEndian.Uint16(pix[(y*w+x)*2:])
			if got != want {
				t.Fatalf("pixel (%d,%d) = %#04x, want %#04x", x, y, got, want)
			}
		}
	}
}
