// Package img streams large PNGs into a flat, seekable RGB565 file (.r565) so the
// LCD can pan around images far too big to decode into RAM (e.g. multi-MB
// rtl_power heatmaps — a 7887x5796 PNG is 183 MB as RGBA, but only 91 MB as a
// flat .r565 on disk, and the viewer reads just the on-screen window). Conversion
// is bounded to ~two scanlines of memory regardless of image size.
//
// The .r565 layout: a 16-byte header (magic "R565", u32 version, u32 width, u32
// height, all little-endian) followed by width*height little-endian uint16
// RGB565 pixels, row-major. Pixel (x,y) is at HeaderSize + (y*width+x)*2.
package img

import (
	"bufio"
	"compress/zlib"
	"encoding/binary"
	"fmt"
	"io"
	"os"
)

const (
	Magic      = "R565"
	HeaderSize = 16 // magic(4) + version(4) + width(4) + height(4)
)

// idatReader presents the concatenated IDAT payloads of a PNG as one stream for
// zlib, skipping every other chunk. The underlying reader must be positioned at
// the first chunk after IHDR.
type idatReader struct {
	r      *bufio.Reader
	remain int // bytes left in the current IDAT chunk's data
	done   bool
}

func (d *idatReader) Read(p []byte) (int, error) {
	for d.remain == 0 {
		if d.done {
			return 0, io.EOF
		}
		var length uint32
		if err := binary.Read(d.r, binary.BigEndian, &length); err != nil {
			return 0, err
		}
		var typ [4]byte
		if _, err := io.ReadFull(d.r, typ[:]); err != nil {
			return 0, err
		}
		switch string(typ[:]) {
		case "IDAT":
			d.remain = int(length)
			if d.remain == 0 { // empty IDAT: skip its CRC and continue
				if _, err := io.CopyN(io.Discard, d.r, 4); err != nil {
					return 0, err
				}
			}
		case "IEND":
			d.done = true
			return 0, io.EOF
		default:
			if _, err := io.CopyN(io.Discard, d.r, int64(length)+4); err != nil {
				return 0, err
			}
		}
	}
	n := len(p)
	if n > d.remain {
		n = d.remain
	}
	m, err := d.r.Read(p[:n])
	d.remain -= m
	if d.remain == 0 && err == nil {
		_, err = io.CopyN(io.Discard, d.r, 4) // chunk CRC
	}
	return m, err
}

// PNGToR565 streams pngPath → outPath. Handles 8-bit RGB/RGBA, non-interlaced
// (the rfheatmap output). progress (nil ok) is called with 0..1 as rows finish.
// Memory is bounded to a few scanlines whatever the image size.
func PNGToR565(pngPath, outPath string, progress func(float64)) (width, height int, err error) {
	in, err := os.Open(pngPath)
	if err != nil {
		return 0, 0, err
	}
	defer in.Close()
	br := bufio.NewReaderSize(in, 1<<16)

	var sig [8]byte
	if _, err = io.ReadFull(br, sig[:]); err != nil {
		return 0, 0, err
	}
	if string(sig[:]) != "\x89PNG\r\n\x1a\n" {
		return 0, 0, fmt.Errorf("not a PNG")
	}

	// IHDR must be the first chunk.
	var ihdrLen uint32
	if err = binary.Read(br, binary.BigEndian, &ihdrLen); err != nil {
		return 0, 0, err
	}
	var typ [4]byte
	if _, err = io.ReadFull(br, typ[:]); err != nil {
		return 0, 0, err
	}
	if string(typ[:]) != "IHDR" {
		return 0, 0, fmt.Errorf("missing IHDR")
	}
	ihdr := make([]byte, ihdrLen)
	if _, err = io.ReadFull(br, ihdr); err != nil {
		return 0, 0, err
	}
	io.CopyN(io.Discard, br, 4) // IHDR CRC

	width = int(binary.BigEndian.Uint32(ihdr[0:4]))
	height = int(binary.BigEndian.Uint32(ihdr[4:8]))
	depth, ctype, interlace := ihdr[8], ihdr[9], ihdr[12]
	var channels int
	switch {
	case depth == 8 && ctype == 2:
		channels = 3
	case depth == 8 && ctype == 6:
		channels = 4
	default:
		return 0, 0, fmt.Errorf("unsupported PNG (depth=%d colortype=%d); want 8-bit RGB/RGBA", depth, ctype)
	}
	if interlace != 0 {
		return 0, 0, fmt.Errorf("interlaced PNG not supported")
	}
	if width <= 0 || height <= 0 {
		return 0, 0, fmt.Errorf("bad dimensions %dx%d", width, height)
	}

	zr, err := zlib.NewReader(&idatReader{r: br})
	if err != nil {
		return 0, 0, err
	}
	defer zr.Close()

	out, err := os.Create(outPath)
	if err != nil {
		return 0, 0, err
	}
	defer out.Close()
	bw := bufio.NewWriterSize(out, 1<<16)

	bw.WriteString(Magic)
	binary.Write(bw, binary.LittleEndian, uint32(1))
	binary.Write(bw, binary.LittleEndian, uint32(width))
	binary.Write(bw, binary.LittleEndian, uint32(height))

	bpp := channels
	rowBytes := width * channels
	cur := make([]byte, rowBytes)
	prev := make([]byte, rowBytes) // zeroed: the row above row 0 is all zeros
	filt := make([]byte, 1)
	row565 := make([]byte, width*2)

	for y := 0; y < height; y++ {
		if _, err = io.ReadFull(zr, filt); err != nil {
			return 0, 0, fmt.Errorf("row %d filter: %w", y, err)
		}
		if _, err = io.ReadFull(zr, cur); err != nil {
			return 0, 0, fmt.Errorf("row %d data: %w", y, err)
		}
		unfilter(filt[0], cur, prev, bpp)
		for x := 0; x < width; x++ {
			r, g, b := cur[x*channels], cur[x*channels+1], cur[x*channels+2]
			v := uint16(r>>3)<<11 | uint16(g>>2)<<5 | uint16(b>>3)
			binary.LittleEndian.PutUint16(row565[x*2:], v)
		}
		if _, err = bw.Write(row565); err != nil {
			return 0, 0, err
		}
		cur, prev = prev, cur // this row becomes the predecessor of the next
		if progress != nil && y&255 == 0 {
			progress(float64(y) / float64(height))
		}
	}
	if err = bw.Flush(); err != nil {
		return 0, 0, err
	}
	if progress != nil {
		progress(1)
	}
	return width, height, nil
}

// unfilter reverses a PNG scanline filter in place. bpp = bytes per pixel.
func unfilter(ftype byte, cur, prev []byte, bpp int) {
	switch ftype {
	case 0: // None
	case 1: // Sub
		for i := bpp; i < len(cur); i++ {
			cur[i] += cur[i-bpp]
		}
	case 2: // Up
		for i := range cur {
			cur[i] += prev[i]
		}
	case 3: // Average
		for i := range cur {
			a := 0
			if i >= bpp {
				a = int(cur[i-bpp])
			}
			cur[i] += byte((a + int(prev[i])) / 2)
		}
	case 4: // Paeth
		for i := range cur {
			a, c := 0, 0
			if i >= bpp {
				a = int(cur[i-bpp])
				c = int(prev[i-bpp])
			}
			cur[i] += byte(paeth(a, int(prev[i]), c))
		}
	}
}

func paeth(a, b, c int) int {
	p := a + b - c
	pa, pb, pc := abs(p-a), abs(p-b), abs(p-c)
	switch {
	case pa <= pb && pa <= pc:
		return a
	case pb <= pc:
		return b
	default:
		return c
	}
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
