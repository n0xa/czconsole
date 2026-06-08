// Package fb mirrors the onboard LCD by reading /dev/fb0 and encoding it as
// JPEG. The Cardputer Zero's st7789v is a real Linux framebuffer (RGB565,
// 320x170) into which APPLaunch's LVGL fbdev driver software-renders, so the
// mmap'd buffer is a pixel-perfect copy of whatever is on screen.
//
// Pure Go: the FBIOGET_*SCREENINFO ioctls go through golang.org/x/sys-free
// syscall wrappers so the binary still cross-compiles without cgo.
package fb

import (
	"bytes"
	"fmt"
	"image"
	"image/jpeg"
	"os"
	"strings"
	"unsafe"

	"syscall"
)

// FindLCD returns the device path of the onboard st7789 LCD framebuffer. The fb
// index is NOT stable across images: the LCD is /dev/fb0 on the Kali graft but
// /dev/fb1 on the stock M5 image (where fb0 is the vc4/HDMI KMS framebuffer, so
// mirroring fb0 there shows a blank HDMI). Match by the driver's registered name
// ("st7789") instead of assuming an index. Falls back to /dev/fb0.
func FindLCD() string {
	entries, err := os.ReadDir("/sys/class/graphics")
	if err != nil {
		return "/dev/fb0"
	}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, "fb") || name == "fbcon" {
			continue
		}
		n, err := os.ReadFile("/sys/class/graphics/" + name + "/name")
		if err == nil && strings.Contains(strings.ToLower(string(n)), "st7789") {
			return "/dev/" + name
		}
	}
	return "/dev/fb0"
}

// Linux fb ioctls.
const (
	fbiogetVScreenInfo = 0x4600
	fbiogetFScreenInfo = 0x4602
)

type bitfield struct {
	Offset, Length, MsbRight uint32
}

type varScreenInfo struct {
	Xres, Yres                 uint32
	XresVirtual, YresVirtual   uint32
	Xoffset, Yoffset           uint32
	BitsPerPixel, Grayscale    uint32
	Red, Green, Blue, Transp   bitfield
	Nonstd, Activate           uint32
	Height, Width              uint32
	AccelFlags                 uint32
	Pixclock                   uint32
	LeftMargin, RightMargin    uint32
	UpperMargin, LowerMargin   uint32
	HsyncLen, VsyncLen         uint32
	Sync, Vmode, Rotate        uint32
	Colorspace                 uint32
	Reserved                   [4]uint32
}

type fixScreenInfo struct {
	ID           [16]byte
	SmemStart    uint64
	SmemLen      uint32
	Type         uint32
	TypeAux      uint32
	Visual       uint32
	Xpanstep     uint16
	Ypanstep     uint16
	Ywrapstep    uint16
	_            uint16
	LineLength   uint32
	MmioStart    uint64
	MmioLen      uint32
	Accel        uint32
	Capabilities uint16
	Reserved     [2]uint16
}

// SnapshotJPEG reads the framebuffer at dev and returns a JPEG at the given
// quality (1-100). Currently handles the common RGB565 16bpp case (the CZ's
// st7789v); other depths return an error rather than a wrong-colored image.
func SnapshotJPEG(dev string, quality int) ([]byte, error) {
	f, err := os.OpenFile(dev, os.O_RDONLY, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var vinfo varScreenInfo
	if err := ioctl(f.Fd(), fbiogetVScreenInfo, unsafe.Pointer(&vinfo)); err != nil {
		return nil, fmt.Errorf("VSCREENINFO: %w", err)
	}
	var finfo fixScreenInfo
	if err := ioctl(f.Fd(), fbiogetFScreenInfo, unsafe.Pointer(&finfo)); err != nil {
		return nil, fmt.Errorf("FSCREENINFO: %w", err)
	}

	if vinfo.BitsPerPixel != 16 {
		return nil, fmt.Errorf("unsupported depth %d bpp (only RGB565 handled)", vinfo.BitsPerPixel)
	}

	raw, err := syscall.Mmap(int(f.Fd()), 0, int(finfo.SmemLen),
		syscall.PROT_READ, syscall.MAP_SHARED)
	if err != nil {
		return nil, fmt.Errorf("mmap: %w", err)
	}
	defer syscall.Munmap(raw)

	w, h := int(vinfo.Xres), int(vinfo.Yres)
	stride := int(finfo.LineLength)
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		row := raw[y*stride:]
		for x := 0; x < w; x++ {
			px := uint16(row[x*2]) | uint16(row[x*2+1])<<8
			r := uint8((px >> 11) & 0x1f)
			g := uint8((px >> 5) & 0x3f)
			b := uint8(px & 0x1f)
			// 5/6/5 → 8/8/8 with bit replication.
			o := img.PixOffset(x, y)
			img.Pix[o+0] = r<<3 | r>>2
			img.Pix[o+1] = g<<2 | g>>4
			img.Pix[o+2] = b<<3 | b>>2
			img.Pix[o+3] = 0xff
		}
	}

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: quality}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func ioctl(fd uintptr, req uint, arg unsafe.Pointer) error {
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, uintptr(req), uintptr(arg))
	if errno != 0 {
		return errno
	}
	return nil
}
