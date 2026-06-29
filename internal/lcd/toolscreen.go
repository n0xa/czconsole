package lcd

import (
	"image"
	"image/color"
	"os"
	"regexp"
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

type colorRule struct {
	re  *regexp.Regexp
	col color.Color
}

func NewToolScreen(spec tool.Spec) *ToolScreen {
	s := &ToolScreen{spec: spec, runner: tool.NewRunner(spec), runDix: 1, stop: make(chan struct{})}
	for _, in := range spec.Inputs {
		f := &toolField{in: in}
		if in.Type == "checkbox" {
			f.on = in.Default == "1"
		} else {
			f.tf = newTextField(in.Default)
		}
		s.fields = append(s.fields, f)
	}
	for _, cr := range spec.Results.Colorize {
		if re, err := regexp.Compile(cr.Match); err == nil {
			s.colors = append(s.colors, colorRule{re, namedColor(cr.Color)})
		}
	}
	s.refresh()
	go s.poll()
	return s
}

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
	// kind=image: look for the sibling image (e.g. <run>.png next to <run>.csv).
	var imagePath string
	if s.spec.Results.Kind == "image" && latest != "" && s.spec.Results.Image != "" {
		ip := strings.TrimSuffix(latest, s.spec.Results.File) + s.spec.Results.Image
		if _, err := os.Stat(ip); err == nil {
			imagePath = ip
		}
	}
	var lines []Line
	if !running && latest != "" {
		lines = s.buildResultLines(latest, when, subj, imagePath)
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
	footer := "esc:config"
	if hasImage {
		footer = "ent:view  esc:config"
	} else if len(lines) > 0 {
		footer = "f/x:scroll  z/c:pan  esc:config"
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
		drawRunningView(c, s.title(), s.spec.Running.Label, subj, "esc leaves it running",
			[]string{"Stop"}, 0)
		return
	}
	drawRunningView(c, s.title(), s.spec.Running.Label, subj, "results will show when complete",
		[]string{"Cancel", "Background"}, s.runDix)
}

func (s *ToolScreen) drawConfig(c *Canvas) {
	footer := "tab:next  ent:run  esc:back"
	if _, e := s.snapshot(); e != "" {
		footer = "! " + e
	}
	content := drawChrome(c, s.title(), footer)
	x0 := content.Min.X + 6
	small := c.Faces().Small

	const rowH = 34
	visible := content.Dy() / rowH
	if visible < 1 {
		visible = 1
	}
	if s.focus < s.scroll {
		s.scroll = s.focus
	}
	if s.focus >= s.scroll+visible {
		s.scroll = s.focus - visible + 1
	}
	for i := s.scroll; i < len(s.fields) && i < s.scroll+visible; i++ {
		f := s.fields[i]
		y := content.Min.Y + (i-s.scroll)*rowH
		if f.tf != nil {
			c.Text(x0, y+2, f.in.Label, small, colDim)
			f.tf.draw(c, image.Rect(x0, y+14, content.Max.X-6, y+32), s.focus == i)
		} else {
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
	case ev.Key == KeyTab, ev.Key == KeyLeft, ev.Key == KeyRight:
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
		s.focus = (s.focus + 1) % len(s.fields)
	case ev.Rune == 0 && ev.Key == KeyUp:
		s.focus = (s.focus - 1 + len(s.fields)) % len(s.fields)
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

func (s *ToolScreen) submit() {
	vals := map[string]string{}
	for _, f := range s.fields {
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

// ── results rendering (text/path + strip + colorize) ─────────────────────────

func (s *ToolScreen) buildResultLines(path string, when time.Time, subj, imagePath string) []Line {
	var lines []Line
	if subj != "" {
		lines = append(lines, Line{subj, colText})
	}
	if !when.IsZero() {
		lines = append(lines, Line{when.Format("2006-01-02 15:04:05"), colDim})
	}
	if s.spec.Results.Kind == "image" {
		if imagePath != "" {
			return append(lines, Line{"heatmap ready — press ent to view", colAccent})
		}
		// no image this run → fall back to the primary output's path
		return append(lines, Line{"no heatmap for this run", colDim},
			Line{"saved to:", colDim}, Line{path, colDim})
	}
	if s.spec.Results.Kind == "path" {
		return append(lines, Line{"saved to:", colDim}, Line{path, colAccent})
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return append(lines, Line{"(output unavailable)", colDim})
	}
	sp := s.spec.Results.StripPrefix
	for _, raw := range strings.Split(string(b), "\n") {
		ln := strings.TrimRight(raw, "\r")
		if strings.TrimSpace(ln) == "" {
			continue
		}
		if sp != "" && strings.HasPrefix(strings.TrimSpace(ln), sp) {
			continue
		}
		lines = append(lines, Line{ln, s.colorFor(ln)})
	}
	return lines
}

func (s *ToolScreen) colorFor(line string) color.Color {
	for _, cr := range s.colors {
		if cr.re.MatchString(line) {
			return cr.col
		}
	}
	return colText
}

func namedColor(name string) color.Color {
	switch name {
	case "accent":
		return colAccent
	case "dim":
		return colDim
	case "title":
		return colTitle
	default:
		return colText
	}
}
