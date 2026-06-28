package lcd

import (
	"fmt"
	"image"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/n0xa/czconsole/internal/nmap"
)

type nmapMode int

const (
	modeResults nmapMode = iota // scrolling parsed results / SCANNING
	modeConfig                  // the options form
)

// NmapScreen renders nmap from the shared nmap.Core. Principle of least
// astonishment: on entry it shows the most recent scan's results (you kicked off
// a scan, walked away, came back) — a scrolling read-only panel. While a scan is
// live it shows SCANNING; when it finishes the poller swaps in the results. With
// no scans yet (or on Esc from the results) it shows the config form: a free-form
// nmap-options text field + a log-errors checkbox + syntax hints.
//
// A background goroutine polls the core (systemctl/cgroup + a file parse can
// block) so the draw/key thread never stalls. The form state (opts/cursor/focus)
// is touched only on the UI goroutine; running/res/err are shared with the poller.
type NmapScreen struct {
	core *nmap.Core

	mu      sync.Mutex
	running bool
	res     *nmap.Result
	err     string
	subject string // what the live scan is scanning (for the running view)
	inited  bool

	lv   LogView
	mode nmapMode
	stop chan struct{}

	// config form (UI-goroutine only)
	opts     []rune
	cursor   int
	logErr   bool
	focus    int // 0 = options field, 1 = log-errors checkbox
	runFocus int // running view: 0 = Cancel, 1 = Background
}

func NewNmap() *NmapScreen {
	s := &NmapScreen{core: nmap.New(), runFocus: 1, stop: make(chan struct{})}
	s.refresh() // synchronous first poll so the initial mode is correct
	go s.poll()
	return s
}

func (s *NmapScreen) poll() {
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-s.stop:
			return
		case <-t.C:
			s.refresh()
		}
	}
}

// Close stops the poller when the screen is popped.
func (s *NmapScreen) Close() { close(s.stop) }

func (s *NmapScreen) refresh() {
	running := s.core.Running()
	res, _ := s.core.LatestResult()
	var subj string
	if running {
		subj = s.core.RunningOpts()
	}
	s.mu.Lock()
	s.running, s.res, s.subject = running, res, subj
	if !s.inited { // first poll decides where we land
		s.inited = true
		if !running && res == nil {
			s.mode = modeConfig
		}
	}
	s.mu.Unlock()
}

func (s *NmapScreen) snapshot() (bool, *nmap.Result, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running, s.res, s.err
}

func (s *NmapScreen) setErr(e string) {
	s.mu.Lock()
	s.err = e
	s.mu.Unlock()
}

func (s *NmapScreen) Draw(c *Canvas) {
	running, res, _ := s.snapshot()
	if running { // a live scan takes over the screen, whatever mode we were in
		s.drawRunning(c)
		return
	}
	if s.mode == modeConfig {
		s.drawConfig(c)
		return
	}
	footer := "esc:config"
	if res != nil {
		footer = "f/x:scroll  z/c:page  esc:config"
	}
	content := drawChrome(c, "NMAP", footer)
	if res != nil {
		s.lv.Draw(c, content, resultLines(res))
	} else {
		c.TextCenteredIn(content, "no scans yet", c.Faces().Body, colDim)
	}
}

func (s *NmapScreen) drawRunning(c *Canvas) {
	s.mu.Lock()
	subj := s.subject
	s.mu.Unlock()
	drawRunningView(c, "NMAP", "SCANNING…", subj, s.runFocus)
}

func (s *NmapScreen) Key(ev Event) (Action, Screen) {
	if running, _, _ := s.snapshot(); running {
		return s.keyRunning(ev)
	}
	if s.mode == modeConfig {
		return s.keyConfig(ev)
	}
	// results
	if ev.Key == KeyBack {
		s.mode = modeConfig // exit results → the config form (not straight to menu)
		return ActNone, nil
	}
	if _, res, _ := s.snapshot(); res != nil {
		s.lv.Key(ev.Key)
	}
	return ActNone, nil
}

// keyRunning handles the Cancel/Background choice over a live scan.
func (s *NmapScreen) keyRunning(ev Event) (Action, Screen) {
	switch {
	case ev.Key == KeyBack:
		return ActPop, nil // background: pop to the menu; the scan keeps running
	case ev.Key == KeyTab, ev.Key == KeyLeft, ev.Key == KeyRight:
		s.runFocus ^= 1
	case ev.Key == KeyEnter:
		if s.runFocus == 0 { // Cancel
			go s.cancel()
			s.mode = modeConfig
		} else { // Background
			return ActPop, nil
		}
	}
	return ActNone, nil
}

func (s *NmapScreen) cancel() {
	_ = s.core.Stop()
	time.Sleep(300 * time.Millisecond)
	s.refresh()
}

// keyConfig handles the options form. A non-zero Rune is a typed character; a
// zero Rune with a nav Key is pure navigation — that's how a typed 'f'/'z' (which
// also map to the d-pad) is told apart from a real arrow.
func (s *NmapScreen) keyConfig(ev Event) (Action, Screen) {
	switch {
	case ev.Key == KeyBack:
		return ActPop, nil // config is the root of nmap → Esc exits to the menu

	case ev.Key == KeyTab, ev.Rune == 0 && (ev.Key == KeyUp || ev.Key == KeyDown):
		s.focus ^= 1 // toggle between the field and the checkbox

	case ev.Key == KeyEnter:
		opts := strings.TrimSpace(string(s.opts))
		if opts == "" {
			s.setErr("enter scan options first")
			break
		}
		s.setErr("")
		s.mode = modeResults
		s.runFocus = 1 // default to Background
		go s.start(opts, s.logErr)

	case s.focus == 1: // checkbox focused
		if ev.Rune == ' ' || ev.Key == KeyRight || ev.Key == KeyLeft {
			s.logErr = !s.logErr
		}

	case ev.Rune != 0: // options field: type a character
		s.opts = append(s.opts[:s.cursor], append([]rune{ev.Rune}, s.opts[s.cursor:]...)...)
		s.cursor++

	case ev.Key == KeyBackspace:
		if s.cursor > 0 {
			s.opts = append(s.opts[:s.cursor-1], s.opts[s.cursor:]...)
			s.cursor--
		}
	case ev.Key == KeyLeft:
		if s.cursor > 0 {
			s.cursor--
		}
	case ev.Key == KeyRight:
		if s.cursor < len(s.opts) {
			s.cursor++
		}
	}
	return ActNone, nil
}

// start runs the scan; on failure it drops back to the form with the error.
func (s *NmapScreen) start(opts string, logErr bool) {
	if err := s.core.Start(opts, logErr); err != nil {
		s.mu.Lock()
		s.err, s.mode = err.Error(), modeConfig
		s.mu.Unlock()
		return
	}
	time.Sleep(300 * time.Millisecond)
	s.refresh()
}

func (s *NmapScreen) drawConfig(c *Canvas) {
	content := drawChrome(c, "NMAP", "tab:field  ent:scan  esc:back")
	x0 := content.Min.X + 6
	small := c.Faces().Small

	// Options text field.
	c.Text(x0, content.Min.Y+4, "OPTIONS", small, colDim)
	fieldR := image.Rect(x0, content.Min.Y+16, content.Max.X-6, content.Min.Y+34)
	fieldBorder := colBorder
	if s.focus == 0 {
		fieldBorder = colAccent
	}
	c.FillRect(fieldR, colCardBg)
	c.Border(fieldR, fieldBorder)
	s.drawField(c, fieldR)

	// Log-errors checkbox.
	mark := "[ ] log errors"
	if s.logErr {
		mark = "[x] log errors"
	}
	cbY := fieldR.Max.Y + 8
	if s.focus == 1 {
		w := c.TextWidth(small, mark) + 8
		c.FillRect(image.Rect(x0-2, cbY-1, x0+w, cbY+13), colAccent)
		c.Text(x0+2, cbY, mark, small, colFocusTx)
	} else {
		c.Text(x0+2, cbY, mark, small, colText)
	}

	// Error (if any) or syntax hints in the free space.
	hy := cbY + 20
	if _, _, e := s.snapshot(); e != "" {
		c.Text(x0, hy, "! "+e, small, colAccent)
	} else {
		for _, h := range []string{
			"-sS/-sT/-sA  SYN / Connect / ACK",
			"-sn          ping sweep",
			"-Pn -O -p 1-1000,22   10.0.0.0/24",
		} {
			c.Text(x0, hy, h, small, colDim)
			hy += 13
		}
	}
}

// drawField renders the options text inside r with a caret, horizontally
// windowed so the cursor stays visible when the text overflows.
func (s *NmapScreen) drawField(c *Canvas, r image.Rectangle) {
	face := c.Faces().Small
	charW := c.TextWidth(face, "M")
	if charW < 1 {
		charW = 6
	}
	innerW := r.Dx() - 8
	maxChars := innerW / charW
	if maxChars < 1 {
		maxChars = 1
	}
	start := 0
	if s.cursor > maxChars-1 {
		start = s.cursor - (maxChars - 1)
	}
	end := start + maxChars
	if end > len(s.opts) {
		end = len(s.opts)
	}
	c.Text(r.Min.X+4, r.Min.Y+3, string(s.opts[start:end]), face, colText)
	if s.focus == 0 { // caret
		cx := r.Min.X + 4 + (s.cursor-start)*charW
		c.FillRect(image.Rect(cx, r.Min.Y+3, cx+1, r.Max.Y-3), colAccent)
	}
}

// resultLines renders a parsed scan for the LogView: a context header (the scan
// options and timestamp, so a panel you return to is self-describing) followed
// by the per-host body.
func resultLines(res *nmap.Result) []Line {
	return append(headerLines(res), bodyLines(res)...)
}

// headerLines shows WHAT was scanned and WHEN.
func headerLines(res *nmap.Result) []Line {
	var lines []Line
	if a := cleanArgs(res.Args); a != "" {
		lines = append(lines, Line{a, colText})
	}
	if !res.When.IsZero() {
		lines = append(lines, Line{res.When.Format("2006-01-02 15:04:05"), colDim})
	}
	return lines
}

// cleanArgs strips the wrapper plumbing (the program name, the --privileged flag
// it adds, and the -oA output prefix) from nmap's recorded command line, leaving
// the operator's own options.
func cleanArgs(args string) string {
	f := strings.Fields(args)
	var out []string
	for i := 0; i < len(f); i++ {
		switch {
		case i == 0: // program name (nmap / /usr/lib/nmap/nmap)
		case f[i] == "--privileged", f[i] == "--unprivileged":
		case f[i] == "-oA":
			i++ // also skip its path argument
		default:
			out = append(out, f[i])
		}
	}
	return strings.Join(out, " ")
}

// bodyLines flattens the per-host results into compact, pre-fit rows. Two shapes,
// picked from the data:
//   - ONE up host WITH ports → the detailed PORT/STATE/SERVICE table
//     (open=accent, filtered=dim), closed collapsed into the "N closed" header.
//   - many hosts, or a host with no ports (e.g. a -sn ping sweep) → a host list,
//     each line "addr  <open ports>" so discovery scans read usefully.
func bodyLines(res *nmap.Result) []Line {
	var up []nmap.Host
	for _, h := range res.Hosts {
		if h.Up {
			up = append(up, h)
		}
	}
	if len(up) == 0 {
		return []Line{{"no hosts up", colDim}}
	}

	if len(up) == 1 && len(up[0].Ports) > 0 {
		h := up[0]
		head := h.Addr + "  up"
		if h.Closed > 0 {
			head += fmt.Sprintf("  %d closed", h.Closed)
		}
		lines := []Line{{head, colTitle}}
		lines = append(lines, Line{fmt.Sprintf("%-9s %-9s %s", "PORT", "STATE", "SERVICE"), colDim})
		for _, p := range h.Ports {
			var col = colText
			switch {
			case p.State == "open":
				col = colAccent
			case strings.Contains(p.State, "filtered"):
				col = colDim
			}
			label := fmt.Sprintf("%d/%s", p.Num, p.Proto)
			lines = append(lines, Line{fmt.Sprintf("%-9s %-9s %s", label, p.State, p.Service), col})
		}
		return lines
	}

	hdr := fmt.Sprintf("%d hosts up", len(up))
	if len(up) == 1 {
		hdr = "1 host up"
	}
	lines := []Line{{hdr, colTitle}}
	for _, h := range up {
		var open []string
		for _, p := range h.Ports {
			if p.State == "open" {
				open = append(open, strconv.Itoa(p.Num))
			}
		}
		line := h.Addr
		if len(open) > 0 {
			line = fmt.Sprintf("%-15s %s", h.Addr, strings.Join(open, ","))
		}
		lines = append(lines, Line{line, colAccent})
	}
	return lines
}
