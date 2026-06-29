package lcd

import (
	"image"
	"strings"
	"sync"
	"time"

	"github.com/n0xa/czconsole/internal/tool"
)

type toolMode int

const (
	toolResults toolMode = iota
	toolConfig
)

// ToolScreen is the generic, spec-driven tool screen — one implementation that
// replaces the hand-written per-tool screens. It renders the spec's inputs as a
// (scrolling) config form, runs the tool via tool.Runner, shows the shared
// running view (Cancel/Background or Stop per the spec), and renders the captured
// output (text with strip/colorize, or just the file path). Adding a tool needs
// no Go — only a JSON spec.
type ToolScreen struct {
	spec   tool.Spec
	runner *tool.Runner
	colors []colorRule

	mu        sync.Mutex
	running   bool
	latest    string
	when      time.Time
	subject   string
	lines     []Line // cached parsed results
	imagePath string // kind=image: the sibling image to view (Enter), "" if none
	err       string
	inited    bool

	lv     LogView
	mode   toolMode
	stop   chan struct{}
	fields []*toolField
	focus  int
	scroll int
	runDix int // running view button focus (0=Cancel,1=Background)
}

type toolField struct {
	in tool.Input
	tf *textField // non-nil for text inputs
	on bool       // checkbox state
}

func NewToolScreen(spec tool.Spec) *ToolScreen {
	s := &ToolScreen{spec: spec, runner: tool.NewRunner(spec), runDix: 1, stop: make(chan struct{})}
	for _, in := range spec.Inputs {
		f := &toolField{in: in}
		switch in.Type {
		case "note":
			// static text, not a field — no widget
		case "checkbox":
			f.on = in.Default == "1"
		default:
			f.tf = newTextField(in.Default)
		}
		s.fields = append(s.fields, f)
	}
	for i, f := range s.fields { // start focus on the first real field, not a note
		if !f.isNote() {
			s.focus = i
			break
		}
	}
	s.colors = compileColors(spec)
	s.refresh()
	go s.poll()
	return s
}

func (f *toolField) isNote() bool { return f.in.Type == "note" }

func (s *ToolScreen) poll() {
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

func (s *ToolScreen) refresh() {
	running := s.runner.Running()
	latest, when := s.runner.Latest()
	var subj string
	if running || latest != "" {
		subj = s.runner.Subject()
	}
	var imagePath string
	if latest != "" {
		imagePath = imageSibling(s.spec, latest)
	}
	var lines []Line
	if !running && latest != "" {
		lines = resultLines(s.spec, latest, when, subj, imagePath, s.colors)
	}
	s.mu.Lock()
	s.running, s.latest, s.when, s.subject, s.lines, s.imagePath = running, latest, when, subj, lines, imagePath
	if !s.inited {
		s.inited = true
		if !running && latest == "" {
			s.mode = toolConfig
		}
	}
	s.mu.Unlock()
}

func (s *ToolScreen) snapshot() (bool, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running, s.err
}

func (s *ToolScreen) Close() { close(s.stop) }

func (s *ToolScreen) title() string { return strings.ToUpper(s.spec.Name) }

// ── draw ─────────────────────────────────────────────────────────────────────

func (s *ToolScreen) Draw(c *Canvas) {
	running, _ := s.snapshot()
	if running {
		s.drawRunning(c)
		return
	}
	if s.mode == toolConfig {
		s.drawConfig(c)
		return
	}
	s.mu.Lock()
	lines := s.lines
	hasImage := s.imagePath != ""
	s.mu.Unlock()
	footer := "tab:logs  esc:config"
	if hasImage {
		footer = "ent:view  tab:logs  esc:config"
	} else if len(lines) > 0 {
		footer = "f/x:scroll  tab:logs  esc:config"
	}
	content := drawChrome(c, s.title(), footer)
	if len(lines) > 0 {
		s.lv.Draw(c, content, lines)
	} else {
		c.TextCenteredIn(content, "no runs yet", c.Faces().Body, colDim)
	}
}

func (s *ToolScreen) drawRunning(c *Canvas) {
	s.mu.Lock()
	subj := s.subject
	s.mu.Unlock()
	if s.spec.Running.Controls == tool.ControlsStop {
		drawRunningView(c, s.title(), "tab:logs  ent:stop  esc:bg", s.spec.Running.Label, subj,
			"esc leaves it running", []string{"Stop"}, 0)
		return
	}
	drawRunningView(c, s.title(), "tab:logs  z/c:move  ent:ok  esc:bg", s.spec.Running.Label, subj,
		"results will show when complete", []string{"Cancel", "Background"}, s.runDix)
}

// config-form metrics: inputs are a fixed row; a note is its line count.
const (
	cfgRowH    = 34
	cfgNoteLn  = 13
	cfgNotePad = 6
)

func (s *ToolScreen) fieldHeight(f *toolField) int {
	if f.isNote() {
		return (strings.Count(f.in.Text, "\n")+1)*cfgNoteLn + cfgNotePad
	}
	return cfgRowH
}

func (s *ToolScreen) drawConfig(c *Canvas) {
	footer := "tab:next  ent:run  esc:back"
	if _, e := s.snapshot(); e != "" {
		footer = "! " + e
	}
	content := drawChrome(c, s.title(), footer)
	x0 := content.Min.X + 6
	small := c.Faces().Small

	// Variable-height layout (notes can be multi-line): scroll by field index so
	// the focused field — always a real input, never a note — stays fully visible.
	if s.focus < s.scroll {
		s.scroll = s.focus
	}
	for s.scroll < s.focus {
		h, fits := 0, true
		for i := s.scroll; i <= s.focus; i++ {
			if h += s.fieldHeight(s.fields[i]); h > content.Dy() {
				fits = false
				break
			}
		}
		if fits {
			break
		}
		s.scroll++
	}

	y := content.Min.Y
	for i := s.scroll; i < len(s.fields); i++ {
		f := s.fields[i]
		h := s.fieldHeight(f)
		if y+h > content.Max.Y && y > content.Min.Y {
			break
		}
		switch {
		case f.isNote():
			ly := y + 2
			for _, line := range strings.Split(f.in.Text, "\n") {
				c.Text(x0, ly, line, small, colDim)
				ly += cfgNoteLn
			}
		case f.tf != nil:
			c.Text(x0, y+2, f.in.Label, small, colDim)
			f.tf.draw(c, image.Rect(x0, y+14, content.Max.X-6, y+32), s.focus == i)
		default:
			mark := "[ ] " + f.in.Label
			if f.on {
				mark = "[x] " + f.in.Label
			}
			if s.focus == i {
				w := c.TextWidth(small, mark) + 8
				c.FillRect(image.Rect(x0-2, y+9, x0+w, y+23), colAccent)
				c.Text(x0+2, y+10, mark, small, colFocusTx)
			} else {
				c.Text(x0+2, y+10, mark, small, colText)
			}
		}
		y += h
	}
}

// ── keys ─────────────────────────────────────────────────────────────────────

func (s *ToolScreen) Key(ev Event) (Action, Screen) {
	if running, _ := s.snapshot(); running {
		return s.keyRunning(ev)
	}
	if s.mode == toolConfig {
		return s.keyConfig(ev)
	}
	if ev.Key == KeyBack {
		s.mode = toolConfig
		return ActNone, nil
	}
	if ev.Key == KeyTab {
		return ActPush, NewHistory(s.spec)
	}
	if ev.Key == KeyEnter {
		s.mu.Lock()
		ip := s.imagePath
		s.mu.Unlock()
		if ip != "" {
			return ActPush, NewImageView(ip)
		}
	}
	s.lv.Key(ev.Key)
	return ActNone, nil
}

func (s *ToolScreen) keyRunning(ev Event) (Action, Screen) {
	// Tab leaves the job running and opens its logs — same as Tab on the results
	// page, and consistent with esc's "leave it running" backgrounding.
	if ev.Key == KeyTab {
		return ActPush, NewHistory(s.spec)
	}
	if s.spec.Running.Controls == tool.ControlsStop {
		switch ev.Key {
		case KeyEnter:
			go s.cancel()
			s.mode = toolResults
		case KeyBack:
			return ActPop, nil // leave it running
		}
		return ActNone, nil
	}
	switch {
	case ev.Key == KeyBack:
		return ActPop, nil // background
	case ev.Key == KeyLeft, ev.Key == KeyRight:
		s.runDix ^= 1
	case ev.Key == KeyEnter:
		if s.runDix == 0 {
			go s.cancel()
			s.mode = toolConfig
		} else {
			return ActPop, nil
		}
	}
	return ActNone, nil
}

func (s *ToolScreen) keyConfig(ev Event) (Action, Screen) {
	switch {
	case ev.Key == KeyBack:
		return ActPop, nil
	case ev.Key == KeyTab, ev.Rune == 0 && ev.Key == KeyDown:
		s.moveFocus(1)
	case ev.Rune == 0 && ev.Key == KeyUp:
		s.moveFocus(-1)
	case ev.Key == KeyEnter:
		s.submit()
	default:
		if len(s.fields) == 0 {
			break
		}
		f := s.fields[s.focus]
		if f.tf != nil {
			f.tf.edit(ev)
		} else if ev.Rune == ' ' || ev.Key == KeyLeft || ev.Key == KeyRight {
			f.on = !f.on
		}
	}
	return ActNone, nil
}

// moveFocus advances focus by delta, skipping notes (which aren't focusable).
func (s *ToolScreen) moveFocus(delta int) {
	n := len(s.fields)
	if n == 0 {
		return
	}
	for k := 0; k < n; k++ {
		s.focus = (s.focus + delta + n) % n
		if !s.fields[s.focus].isNote() {
			return
		}
	}
}

func (s *ToolScreen) submit() {
	vals := map[string]string{}
	for _, f := range s.fields {
		if f.isNote() {
			continue
		}
		if f.tf != nil {
			v := f.tf.String()
			if f.in.Required && strings.TrimSpace(v) == "" {
				s.setErr(f.in.Label + " required")
				return
			}
			vals[f.in.ID] = v
		} else if f.on {
			vals[f.in.ID] = "1"
		} else {
			vals[f.in.ID] = "0"
		}
	}
	s.setErr("")
	s.mode = toolResults
	s.runDix = 1
	go s.start(vals)
}

func (s *ToolScreen) start(vals map[string]string) {
	if err := s.runner.Start(vals); err != nil {
		s.mu.Lock()
		s.err, s.mode = err.Error(), toolConfig
		s.mu.Unlock()
		return
	}
	time.Sleep(300 * time.Millisecond)
	s.refresh()
}

func (s *ToolScreen) cancel() {
	_ = s.runner.Stop()
	time.Sleep(300 * time.Millisecond)
	s.refresh()
}

func (s *ToolScreen) setErr(e string) {
	s.mu.Lock()
	s.err = e
	s.mu.Unlock()
}
