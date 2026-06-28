package lcd

import (
	"fmt"
	"image"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/n0xa/czconsole/internal/gobuster"
)

type gobMode int

const (
	gobResults gobMode = iota
	gobConfig
)

// GobusterScreen renders gobuster dir scans from the shared gobuster.Core. Same
// PoLA flow as nmap (latest results on entry; SCANNING with Cancel/Background
// while live), but the config form is three fields — target URL, wordlist
// (prefilled), options (prefilled) — and results are the discovered paths.
type GobusterScreen struct {
	core *gobuster.Core

	mu      sync.Mutex
	running bool
	res     *gobuster.Result
	err     string
	subject string // the live scan's target (for the running view)
	inited  bool

	lv   LogView
	mode gobMode
	stop chan struct{}

	// config form (UI-goroutine only)
	url, wordlist, opts *textField
	focus               int // 0 = url, 1 = wordlist, 2 = options
	runFocus            int // running view: 0 = Cancel, 1 = Background
}

func NewGobuster() *GobusterScreen {
	s := &GobusterScreen{
		core:     gobuster.New(),
		url:      newTextField(""),
		wordlist: newTextField(gobuster.DefaultWordlist),
		opts:     newTextField(gobuster.DefaultOptions),
		runFocus: 1,
		stop:     make(chan struct{}),
	}
	s.refresh()
	go s.poll()
	return s
}

func (s *GobusterScreen) poll() {
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

func (s *GobusterScreen) refresh() {
	running := s.core.Running()
	res, _ := s.core.LatestResult()
	var subj string
	if running {
		subj = s.core.RunningTarget()
	}
	s.mu.Lock()
	s.running, s.res, s.subject = running, res, subj
	if !s.inited {
		s.inited = true
		if !running && res == nil {
			s.mode = gobConfig
		}
	}
	s.mu.Unlock()
}

func (s *GobusterScreen) snapshot() (bool, *gobuster.Result, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running, s.res, s.err
}

func (s *GobusterScreen) setErr(e string) {
	s.mu.Lock()
	s.err = e
	s.mu.Unlock()
}

// Close stops the poller when the screen is popped.
func (s *GobusterScreen) Close() { close(s.stop) }

func (s *GobusterScreen) Draw(c *Canvas) {
	running, res, _ := s.snapshot()
	if running {
		s.mu.Lock()
		subj := s.subject
		s.mu.Unlock()
		drawRunningView(c, "GOBUSTER", "RUNNING…", subj, s.runFocus)
		return
	}
	if s.mode == gobConfig {
		s.drawConfig(c)
		return
	}
	footer := "esc:config"
	if res != nil {
		footer = "f/x:scroll  z/c:page  esc:config"
	}
	content := drawChrome(c, "GOBUSTER", footer)
	if res != nil {
		s.lv.Draw(c, content, gobLines(res))
	} else {
		c.TextCenteredIn(content, "no scans yet", c.Faces().Body, colDim)
	}
}

func (s *GobusterScreen) Key(ev Event) (Action, Screen) {
	if running, _, _ := s.snapshot(); running {
		return s.keyRunning(ev)
	}
	if s.mode == gobConfig {
		return s.keyConfig(ev)
	}
	if ev.Key == KeyBack {
		s.mode = gobConfig
		return ActNone, nil
	}
	if _, res, _ := s.snapshot(); res != nil {
		s.lv.Key(ev.Key)
	}
	return ActNone, nil
}

func (s *GobusterScreen) keyRunning(ev Event) (Action, Screen) {
	switch {
	case ev.Key == KeyBack:
		return ActPop, nil // background
	case ev.Key == KeyTab, ev.Key == KeyLeft, ev.Key == KeyRight:
		s.runFocus ^= 1
	case ev.Key == KeyEnter:
		if s.runFocus == 0 {
			go s.cancel()
			s.mode = gobConfig
		} else {
			return ActPop, nil
		}
	}
	return ActNone, nil
}

// keyConfig drives the three-field form. Tab / real Up-Down cycle the fields;
// everything else routes to the focused field (a non-zero Rune is a character,
// so typed f/z/x/c insert rather than navigate — see textField.edit).
func (s *GobusterScreen) keyConfig(ev Event) (Action, Screen) {
	switch {
	case ev.Key == KeyBack:
		return ActPop, nil
	case ev.Key == KeyTab, ev.Rune == 0 && ev.Key == KeyDown:
		s.focus = (s.focus + 1) % 3
	case ev.Rune == 0 && ev.Key == KeyUp:
		s.focus = (s.focus + 2) % 3
	case ev.Key == KeyEnter:
		if strings.TrimSpace(s.url.String()) == "" {
			s.setErr("target URL required")
			break
		}
		s.setErr("")
		s.mode = gobResults
		s.runFocus = 1
		go s.start(s.url.String(), s.wordlist.String(), s.opts.String())
	default:
		s.fieldFor(s.focus).edit(ev)
	}
	return ActNone, nil
}

func (s *GobusterScreen) fieldFor(i int) *textField {
	switch i {
	case 0:
		return s.url
	case 1:
		return s.wordlist
	default:
		return s.opts
	}
}

func (s *GobusterScreen) start(url, wordlist, opts string) {
	if err := s.core.Start(url, wordlist, opts); err != nil {
		s.mu.Lock()
		s.err, s.mode = err.Error(), gobConfig
		s.mu.Unlock()
		return
	}
	time.Sleep(300 * time.Millisecond)
	s.refresh()
}

func (s *GobusterScreen) cancel() {
	_ = s.core.Stop()
	time.Sleep(300 * time.Millisecond)
	s.refresh()
}

func (s *GobusterScreen) drawConfig(c *Canvas) {
	content := drawChrome(c, "GOBUSTER", "tab:next  ent:scan  esc:back")
	x0 := content.Min.X + 6
	small := c.Faces().Small
	y := content.Min.Y + 2
	for i, f := range []struct {
		label string
		tf    *textField
	}{
		{"TARGET URL", s.url},
		{"WORDLIST", s.wordlist},
		{"OPTIONS", s.opts},
	} {
		c.Text(x0, y, f.label, small, colDim)
		f.tf.draw(c, image.Rect(x0, y+12, content.Max.X-6, y+30), s.focus == i)
		y += 36
	}
	if _, _, e := s.snapshot(); e != "" {
		c.Text(x0, y+2, "! "+e, small, colAccent)
	}
}

// gobLines flattens a gobuster result into LogView rows: a target/wordlist/time
// header, a "N found" summary, then one row per discovered path coloured by
// status (2xx accent, 4xx/5xx dim).
func gobLines(res *gobuster.Result) []Line {
	target := res.Target
	if target == "" {
		target = "(no target)"
	}
	lines := []Line{{target, colText}}

	meta := ""
	if !res.When.IsZero() {
		meta = res.When.Format("2006-01-02 15:04:05")
	}
	if res.Wordlist != "" {
		if meta != "" {
			meta += "  "
		}
		meta += filepath.Base(res.Wordlist)
	}
	if meta != "" {
		lines = append(lines, Line{meta, colDim})
	}

	if len(res.Findings) == 0 {
		lines = append(lines, Line{"no paths found", colDim})
		return lines
	}
	lines = append(lines, Line{fmt.Sprintf("%d found", len(res.Findings)), colTitle})
	for _, f := range res.Findings {
		col := colText
		switch {
		case f.Status >= 200 && f.Status < 300:
			col = colAccent
		case f.Status >= 400:
			col = colDim
		}
		lines = append(lines, Line{fmt.Sprintf("%-4d %s", f.Status, f.Path), col})
	}
	return lines
}
