package lcd

import (
	"fmt"
	"image"
	"sync"
	"time"

	"github.com/n0xa/czconsole/internal/wardrive"
)

// WardriveScreen renders the wardrive HUD from the shared wardrive.Core. A
// background goroutine polls the core (kismet REST can block up to the client
// timeout) so the draw/key thread never stalls; Draw reads the latest snapshot.
//
// Interface selection (z/c) and the reveal-password toggle (p) are handled on
// the UI goroutine, so those fields need no locking; only `st` is shared with
// the poller.
type WardriveScreen struct {
	core *wardrive.Core

	mu sync.Mutex
	st wardrive.Status

	ifaces   []string
	ifaceIdx int
	showPass bool
	pass     string

	stop chan struct{}
}

func NewWardrive() *WardriveScreen {
	w := &WardriveScreen{core: wardrive.New(), stop: make(chan struct{})}
	w.ifaces = wardrive.Interfaces()
	for i, n := range w.ifaces { // prefer wlan1 (the usual monitor NIC)
		if n == "wlan1" {
			w.ifaceIdx = i
			break
		}
	}
	go w.poll()
	return w
}

func (w *WardriveScreen) poll() {
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	w.refresh()
	for {
		select {
		case <-w.stop:
			return
		case <-t.C:
			w.refresh()
		}
	}
}

func (w *WardriveScreen) refresh() {
	s := w.core.Status()
	w.mu.Lock()
	w.st = s
	w.mu.Unlock()
}

func (w *WardriveScreen) snapshot() wardrive.Status {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.st
}

// Close stops the poller when the screen is popped.
func (w *WardriveScreen) Close() { close(w.stop) }

func (w *WardriveScreen) selectedIface() string {
	if len(w.ifaces) == 0 {
		return ""
	}
	return w.ifaces[w.ifaceIdx]
}

func (w *WardriveScreen) Draw(c *Canvas) {
	s := w.snapshot()

	footer := "esc:back  ent:start  z/c:iface  p:pass"
	if s.Running {
		footer = "esc:back  ent:stop  p:pass"
	}
	content := drawChrome(c, "WARDRIVE", footer)

	// status row: badge (left) + GPS pill (right)
	rowY := content.Min.Y + 4
	status := "IDLE"
	if s.Running {
		status = "RUNNING"
	}
	pill(c, content.Min.X+6, rowY, status, colDim)

	gpsText := "GPS -"
	if s.GPSFix {
		gpsText = "GPS 3D"
	}
	gw := c.TextWidth(c.Faces().Small, gpsText) + 12
	pill(c, content.Max.X-6-gw, rowY, gpsText, colText)

	// "-" until we have live stats (running AND kismet REST answered)
	val := func(n int) string {
		if s.Running && s.StatsOK {
			return fmt.Sprintf("%d", n)
		}
		return "-"
	}

	// stat cards
	cardsTop := rowY + 20
	cardsBot := content.Max.Y - 18
	const gap = 6
	cw := (content.Dx() - 12 - 2*gap) / 3
	x := content.Min.X + 6
	cards := []struct {
		value, label string
		accent       bool
	}{
		{val(s.APs), "APs", false},
		{val(s.Clients), "CLIENTS", false},
		{val(s.NewPerMin), "NEW/MIN", true},
	}
	for _, cd := range cards {
		r := image.Rect(x, cardsTop, x+cw, cardsBot)
		statCard(c, r, cd.value, cd.label, cd.accent)
		x += cw + gap
	}

	// bottom line: revealed password > running iface+uptime > selectable iface
	bottomY := content.Max.Y - 14
	switch {
	case w.showPass:
		c.Text(content.Min.X+6, bottomY, "pass: "+w.pass, c.Faces().Small, colAccent)
	case s.Running:
		c.Text(content.Min.X+6, bottomY,
			fmt.Sprintf("wlan: %s   up: %s", s.Iface, fmtDuration(s.UptimeSec)),
			c.Faces().Small, colDim)
	default:
		iface := w.selectedIface()
		if iface == "" {
			iface = "none"
		}
		c.Text(content.Min.X+6, bottomY, "iface: "+iface+"  (z/c)", c.Faces().Small, colDim)
	}
}

func (w *WardriveScreen) Key(ev Event) (Action, Screen) {
	running := w.snapshot().Running
	switch ev.Key {
	case KeyBack:
		return ActPop, nil
	case KeyEnter:
		go w.toggle(running, w.selectedIface()) // capture values; systemctl blocks
	case KeyLeft:
		if !running && len(w.ifaces) > 0 {
			w.ifaceIdx = (w.ifaceIdx - 1 + len(w.ifaces)) % len(w.ifaces)
		}
	case KeyRight:
		if !running && len(w.ifaces) > 0 {
			w.ifaceIdx = (w.ifaceIdx + 1) % len(w.ifaces)
		}
	case KeyShowPass:
		w.showPass = !w.showPass
		if w.showPass {
			w.pass = w.core.Password() // read once on reveal
		}
	}
	return ActNone, nil
}

func (w *WardriveScreen) toggle(running bool, iface string) {
	if running {
		_ = w.core.Stop()
	} else if iface != "" {
		_ = w.core.Start(iface)
	}
	time.Sleep(300 * time.Millisecond)
	w.refresh()
}

func fmtDuration(sec int) string {
	if sec <= 0 {
		return "0s"
	}
	h, m, s := sec/3600, (sec%3600)/60, sec%60
	if h > 0 {
		return fmt.Sprintf("%dh%02dm", h, m)
	}
	if m > 0 {
		return fmt.Sprintf("%dm%02ds", m, s)
	}
	return fmt.Sprintf("%ds", s)
}
