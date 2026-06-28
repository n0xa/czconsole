package fb

// Display is the write side of the onboard LCD: it mmaps the st7789 framebuffer
// read-write and blits an RGBA image into it as RGB565. APPLaunch cedes the
// framebuffer to whatever foreground program is running, so the native LCD
// binary owns the screen while it's up (and APPLaunch resumes on exit).
//
// Shares FindLCD, the screeninfo structs, and ioctl with the mirror reader
// (snapshot side) in this package — pure Go, no cgo.

import (
	"fmt"
	"image"
	"os"
	"syscall"
	"unsafe"
)

type Display struct {
	f      *os.File
	mem    []byte
	w, h   int
	stride int
}

// OpenDisplay finds the st7789 LCD and maps it for writing. Errors if the panel
// isn't the expected RGB565 16bpp.
func OpenDisplay() (*Display, error) {
	dev := FindLCD()
	f, err := os.OpenFile(dev, os.O_RDWR, 0)
	if err != nil {
		return nil, err
	}

	var vinfo varScreenInfo
	if err := ioctl(f.Fd(), fbiogetVScreenInfo, unsafe.Pointer(&vinfo)); err != nil {
		f.Close()
		return nil, fmt.Errorf("VSCREENINFO: %w", err)
	}
	var finfo fixScreenInfo
	if err := ioctl(f.Fd(), fbiogetFScreenInfo, unsafe.Pointer(&finfo)); err != nil {
		f.Close()
		return nil, fmt.Errorf("FSCREENINFO: %w", err)
	}
	if vinfo.BitsPerPixel != 16 {
		f.Close()
		return nil, fmt.Errorf("unsupported depth %d bpp (only RGB565 handled)", vinfo.BitsPerPixel)
	}

	mem, err := syscall.Mmap(int(f.Fd()), 0, int(finfo.SmemLen),
		syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_SHARED)
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("mmap: %w", err)
	}

	return &Display{
		f:      f,
		mem:    mem,
		w:      int(vinfo.Xres),
		h:      int(vinfo.Yres),
		stride: int(finfo.LineLength),
	}, nil
}

// Bounds is the panel's pixel rectangle (e.g. 320x170).
func (d *Display) Bounds() image.Rectangle { return image.Rect(0, 0, d.w, d.h) }

// Present blits an RGBA image into the framebuffer, converting to RGB565
// (little-endian, matching the mirror reader).
func (d *Display) Present(img *image.RGBA) {
	for y := 0; y < d.h; y++ {
		row := d.mem[y*d.stride:]
		base := img.PixOffset(0, y)
		for x := 0; x < d.w; x++ {
			o := base + x*4
			px := uint16(img.Pix[o+0]>>3)<<11 |
				uint16(img.Pix[o+1]>>2)<<5 |
				uint16(img.Pix[o+2]>>3)
			row[x*2] = byte(px)
			row[x*2+1] = byte(px >> 8)
		}
	}
}

func (d *Display) Close() error {
	if d.mem != nil {
		syscall.Munmap(d.mem)
		d.mem = nil
	}
	return d.f.Close()
}
