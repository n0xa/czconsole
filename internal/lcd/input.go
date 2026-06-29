// Package lcd is the native on-screen (LCD) frontend for the CardputerZero: it
// owns the framebuffer and the keypad while it's the foreground program, drawing
// an immediate-mode UI over czconsole's existing modules.
package lcd

import (
	"context"
	"encoding/binary"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Key is a logical navigation key, decoded from evdev.
type Key int

const (
	KeyNone Key = iota
	KeyUp
	KeyDown
	KeyLeft
	KeyRight
	KeyEnter
	KeyBack      // ESC
	KeyTab       // field navigation in forms
	KeyBackspace // text editing
	KeyShowPass  // 'p' — reveal secrets (e.g. wardrive REST password)
)

// Event is one key press. It carries BOTH interpretations so each screen picks
// what it needs without the input layer having to know the current mode:
//
//   - Key  — the navigation meaning (d-pad, Enter, Esc, Tab, Backspace…).
//   - Rune — the printable character, or 0 if the key isn't printable.
//
// The d-pad letters (f/z/x/c) and 'p' carry BOTH (e.g. f → {KeyUp, 'f'}). A text
// field therefore distinguishes a typed letter from a real arrow by Rune!=0: a
// non-zero Rune is a character to insert; a zero Rune with a nav Key is pure
// navigation (cursor/submit). Nav screens just read Key and ignore Rune.
type Event struct {
	Key  Key
	Rune rune
}

// Linux evdev constants we care about.
const (
	evKey = 0x01

	keyEsc       = 1
	keyBackspace = 14
	keyTab       = 15
	keyEnter     = 28
	keyP         = 25
	keyC         = 46
	keyF         = 33
	keyX         = 45
	keyZ         = 44
	keySpace     = 57
	keyLShift    = 42
	keyRShift    = 54
	keyUp        = 103
	keyLeft      = 105
	keyRight     = 106
	keyDown      = 108
)

// letterCodes maps evdev key codes to their base (lowercase) letter.
var letterCodes = map[uint16]rune{
	16: 'q', 17: 'w', 18: 'e', 19: 'r', 20: 't', 21: 'y', 22: 'u', 23: 'i', 24: 'o', 25: 'p',
	30: 'a', 31: 's', 32: 'd', 33: 'f', 34: 'g', 35: 'h', 36: 'j', 37: 'k', 38: 'l',
	44: 'z', 45: 'x', 46: 'c', 47: 'v', 48: 'b', 49: 'n', 50: 'm',
}

// digitCodes maps the number-row codes (2..11) to their digit.
var digitCodes = map[uint16]rune{
	2: '1', 3: '2', 4: '3', 5: '4', 6: '5', 7: '6', 8: '7', 9: '8', 10: '9', 11: '0',
}

// symCodes is the CardputerZero Sym-layer table, ported verbatim from APPLaunch's
// keyboard_input.c (tca8418_keymap[]). The TCA8418 firmware emits these custom
// codes for Sym+<key> chords — they are NOT the standard symbol keycodes — so we
// translate them ourselves exactly as APPLaunch does. (Verified against a live
// capture: Sym+R→196→'-', Sym+T→197→'/', etc.)
var symCodes = map[uint16]rune{
	183: '!', 184: '@', 185: '#', 186: '$', 187: '%', 188: '^', 189: '&', 190: '*',
	191: '(', 192: ')', 193: '~', 194: '`', 195: '+', 196: '-', 197: '/', 198: '\\',
	199: '{', 200: '}', 201: '[', 202: ']', 209: '=', 210: ':', 211: ';', 212: '_',
	213: '?', 214: '<', 215: '>', 216: '\'', 217: '"', 231: ',', 232: '.', 233: '|',
}

// mapKey returns the navigation meaning of a key code (KeyNone if it isn't a nav
// key — e.g. a plain letter, which is carried as Event.Rune instead).
func mapKey(code uint16) Key {
	switch code {
	case keyUp, keyF:
		return KeyUp
	case keyDown, keyX:
		return KeyDown
	case keyLeft, keyZ:
		return KeyLeft
	case keyRight, keyC:
		return KeyRight
	case keyEnter:
		return KeyEnter
	case keyEsc:
		return KeyBack
	case keyTab:
		return KeyTab
	case keyBackspace:
		return KeyBackspace
	case keyP:
		return KeyShowPass
	default:
		return KeyNone
	}
}

// printableRune returns the character a key code produces (0 if non-printable).
// Letters honour Shift for case; symbols come pre-resolved from the Sym layer
// (the device banks them into distinct codes, so Shift doesn't apply there).
func printableRune(code uint16, shift bool) rune {
	if r, ok := symCodes[code]; ok {
		return r
	}
	if r, ok := letterCodes[code]; ok {
		if shift {
			return r - ('a' - 'A')
		}
		return r
	}
	if r, ok := digitCodes[code]; ok {
		return r
	}
	if code == keySpace {
		return ' '
	}
	return 0
}

// inputEvent is struct input_event on 64-bit Linux: timeval(16) + type/code/value.
type inputEvent struct {
	Sec   int64
	Usec  int64
	Type  uint16
	Code  uint16
	Value int32
}

// findKeypad returns the evdev node for the integrated keyboard. Prefers the
// tca8418c matrix controller by name; falls back to any device whose key
// capability bitmap advertises the d-pad keys we use.
func findKeypad() string {
	entries, _ := filepath.Glob("/dev/input/event*")
	// Pass 1: match by device name.
	for _, dev := range entries {
		base := filepath.Base(dev)
		name, _ := os.ReadFile("/sys/class/input/" + base + "/device/name")
		if strings.Contains(strings.ToLower(string(name)), "tca8418") {
			return dev
		}
	}
	// Pass 2: capability sniff (KEY_F + KEY_ESC live in the low 64-bit word).
	for _, dev := range entries {
		base := filepath.Base(dev)
		raw, err := os.ReadFile("/sys/class/input/" + base + "/device/capabilities/key")
		if err != nil {
			continue
		}
		fields := strings.Fields(strings.TrimSpace(string(raw)))
		if len(fields) == 0 {
			continue
		}
		low, err := strconv.ParseUint(fields[len(fields)-1], 16, 64)
		if err != nil {
			continue
		}
		if low&(1<<keyF) != 0 && low&(1<<keyEsc) != 0 {
			return dev
		}
	}
	return "/dev/input/event0"
}

// software-autorepeat timing (the tca8418 has no EV_REP, so we generate it).
const (
	repeatDelay  = 320 * time.Millisecond // hold this long before repeating
	repeatPeriod = 45 * time.Millisecond  // then re-emit this often
)

// repeatable reports whether a held key should auto-repeat. The commit keys are
// excluded so a hold can't double-submit / over-pop / skip form fields.
func repeatable(code uint16) bool {
	switch code {
	case keyEnter, keyEsc, keyTab:
		return false
	}
	return true
}

type rawKey struct {
	code  uint16
	value int32 // 1=press, 0=release (the device never sends 2=repeat)
}

// ReadKeys opens the keypad and streams Events until ctx is cancelled. Shift is
// tracked across press/release for casing; held keys auto-repeat in software
// (the controller below) so scrolling/panning can be held down.
func ReadKeys(ctx context.Context) (<-chan Event, error) {
	dev := findKeypad()
	f, err := os.Open(dev)
	if err != nil {
		return nil, err
	}

	raw := make(chan rawKey, 32)
	out := make(chan Event, 16)

	// evdev reader → raw (code,value) press/release events.
	go func() {
		defer close(raw)
		defer f.Close()
		go func() { <-ctx.Done(); f.Close() }() // unblock the blocking Read

		var ev inputEvent
		buf := make([]byte, binary.Size(ev))
		for {
			if _, err := readFull(f, buf); err != nil {
				return
			}
			ev.Type = binary.LittleEndian.Uint16(buf[16:18])
			ev.Code = binary.LittleEndian.Uint16(buf[18:20])
			ev.Value = int32(binary.LittleEndian.Uint32(buf[20:24]))
			if ev.Type != evKey {
				continue
			}
			select {
			case raw <- rawKey{ev.Code, ev.Value}:
			case <-ctx.Done():
				return
			}
		}
	}()

	// controller: shift state + software auto-repeat of the held key.
	go func() {
		defer close(out)
		ticker := time.NewTicker(repeatPeriod)
		defer ticker.Stop()

		var shift bool
		var heldCode uint16
		var heldEvent Event
		var heldSince time.Time

		emit := func(e Event) {
			select {
			case out <- e:
			case <-ctx.Done():
			}
		}
		for {
			select {
			case <-ctx.Done():
				return
			case rk, ok := <-raw:
				if !ok {
					return
				}
				if rk.code == keyLShift || rk.code == keyRShift {
					shift = rk.value != 0
					continue
				}
				switch rk.value {
				case 1: // press
					e := Event{Key: mapKey(rk.code), Rune: printableRune(rk.code, shift)}
					if e.Key == KeyNone && e.Rune == 0 {
						heldCode = 0
						continue
					}
					emit(e)
					if repeatable(rk.code) {
						heldCode, heldEvent, heldSince = rk.code, e, time.Now()
					} else {
						heldCode = 0
					}
				case 0: // release
					if rk.code == heldCode {
						heldCode = 0
					}
				}
			case now := <-ticker.C:
				if heldCode != 0 && now.Sub(heldSince) >= repeatDelay {
					emit(heldEvent)
				}
			}
		}
	}()
	return out, nil
}

func readFull(f *os.File, buf []byte) (int, error) {
	got := 0
	for got < len(buf) {
		n, err := f.Read(buf[got:])
		if n > 0 {
			got += n
		}
		if err != nil {
			return got, err
		}
	}
	return got, nil
}
