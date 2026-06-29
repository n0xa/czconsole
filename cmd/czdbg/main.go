// Command czdbg is a throwaway dev/test harness (NOT shipped; remove before the
// final commit). Validates the wardrive core, snapshots the LCD framebuffer, and
// dumps raw evdev key events (keyboard research spike).
//
//	czdbg status | start | stop | fb <path> | keys [dev]
package main

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/n0xa/czconsole/internal/fb"
	"github.com/n0xa/czconsole/internal/img"
	"github.com/n0xa/czconsole/internal/wardrive"
)

// r565convert converts a PNG to a flat .r565 and reports timing + peak RSS, to
// validate the streaming converter stays bounded on big images.
func r565convert(pngPath, outPath string) {
	start := time.Now()
	w, h, err := img.PNGToR565(pngPath, outPath, func(f float64) {
		fmt.Printf("\r  converting %3.0f%%", f*100)
	})
	fmt.Println()
	if err != nil {
		fmt.Println("error:", err)
		os.Exit(1)
	}
	fi, _ := os.Stat(outPath)
	fmt.Printf("converted %dx%d in %v → %.0f MB .r565, peak RSS %s\n",
		w, h, time.Since(start).Round(time.Millisecond), float64(fi.Size())/1e6, vmHWM())
}

// vmHWM reads the process peak resident set size from /proc/self/status.
func vmHWM() string {
	b, _ := os.ReadFile("/proc/self/status")
	for _, line := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(line, "VmHWM:") {
			return strings.TrimSpace(strings.TrimPrefix(line, "VmHWM:"))
		}
	}
	return "?"
}

// keyName maps the common evdev codes so the capture is readable; unknown codes
// (e.g. a Fn/Sym modifier) print as code_NNN so we can identify them.
func keyName(code uint16) string {
	n := map[uint16]string{
		1: "ESC", 14: "BACKSPACE", 15: "TAB", 28: "ENTER", 57: "SPACE",
		42: "LSHIFT", 54: "RSHIFT", 29: "LCTRL", 97: "RCTRL", 56: "LALT", 100: "RALT",
		125: "LMETA", 126: "RMETA", 127: "COMPOSE", 464: "FN", 0x1d1: "FN_ESC",
		103: "UP", 108: "DOWN", 105: "LEFT", 106: "RIGHT",
		12: "MINUS", 13: "EQUAL", 26: "LBRACE", 27: "RBRACE", 43: "BACKSLASH",
		39: "SEMICOLON", 40: "APOSTROPHE", 41: "GRAVE", 51: "COMMA", 52: "DOT", 53: "SLASH",
	}
	if s, ok := n[code]; ok {
		return s
	}
	// letters / digits
	rows := "1234567890"
	if code >= 2 && code <= 11 {
		return "DIGIT_" + string(rows[code-2])
	}
	letters := map[uint16]string{16: "Q", 17: "W", 18: "E", 19: "R", 20: "T", 21: "Y",
		22: "U", 23: "I", 24: "O", 25: "P", 30: "A", 31: "S", 32: "D", 33: "F", 34: "G",
		35: "H", 36: "J", 37: "K", 38: "L", 44: "Z", 45: "X", 46: "C", 47: "V", 48: "B",
		49: "N", 50: "M"}
	if s, ok := letters[code]; ok {
		return s
	}
	return fmt.Sprintf("code_%d", code)
}

func dumpKeys(dev string) {
	f, err := os.Open(dev)
	if err != nil {
		fmt.Println("open:", err)
		os.Exit(1)
	}
	defer f.Close()
	fmt.Printf("reading %s — press keys; Ctrl-C to stop\n", dev)
	buf := make([]byte, 24) // input_event on arm64
	for {
		if _, err := io.ReadFull(f, buf); err != nil {
			return
		}
		typ := binary.LittleEndian.Uint16(buf[16:18])
		code := binary.LittleEndian.Uint16(buf[18:20])
		val := int32(binary.LittleEndian.Uint32(buf[20:24]))
		if typ != 0x01 { // EV_KEY only
			continue
		}
		st := map[int32]string{0: "up", 1: "DOWN", 2: "repeat"}[val]
		fmt.Printf("code=%-4d %-12s %s\n", code, keyName(code), st)
	}
}

func main() {
	if len(os.Args) < 2 {
		fmt.Println("usage: czdbg status | start | stop | fb <path>")
		os.Exit(2)
	}
	switch os.Args[1] {
	case "status":
		c := wardrive.New()
		fmt.Printf("interfaces: %v\npassword: %q\nstatus: %+v\n",
			wardrive.Interfaces(), c.Password(), c.Status())
	case "start":
		ifaces := wardrive.Interfaces()
		if len(ifaces) == 0 {
			fmt.Println("no monitor interface")
			os.Exit(1)
		}
		fmt.Println("Start ->", wardrive.New().Start(ifaces[0]))
	case "stop":
		fmt.Println("Stop ->", wardrive.New().Stop())
	case "keys":
		dev := "/dev/input/event3"
		if len(os.Args) >= 3 {
			dev = os.Args[2]
		}
		dumpKeys(dev)
	case "r565":
		if len(os.Args) < 4 {
			fmt.Println("usage: czdbg r565 <png> <out.r565>")
			os.Exit(1)
		}
		r565convert(os.Args[2], os.Args[3])
	case "fb":
		if len(os.Args) < 3 {
			fmt.Println("usage: czdbg fb <path>")
			os.Exit(2)
		}
		b, err := fb.SnapshotJPEG(fb.FindLCD(), 85)
		if err != nil {
			fmt.Println("snapshot:", err)
			os.Exit(1)
		}
		if err := os.WriteFile(os.Args[2], b, 0o644); err != nil {
			fmt.Println("write:", err)
			os.Exit(1)
		}
		fmt.Printf("wrote %d bytes to %s\n", len(b), os.Args[2])
	default:
		fmt.Println("unknown:", os.Args[1])
		os.Exit(2)
	}
}
