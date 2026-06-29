package lcd

import (
	"image"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/n0xa/czconsole/internal/opdir"
	"github.com/n0xa/czconsole/internal/tool"
)

// histRun is one past run: its primary output file + timestamp.
type histRun struct {
	path  string
	when  time.Time
	label string // the <ts> portion, for the list
}

// HistoryScreen lists a tool's past runs newest-first (read from ~/<tool>/),
// reached with Tab from the results page. Enter opens one into a RunView, which
// reuses the same renderers (text/path, or the heatmap viewer for old PNGs).
type HistoryScreen struct {
	spec   tool.Spec
	colors []colorRule
	runs   []histRun
	focus  int
	scroll int
}

func NewHistory(spec tool.Spec) *HistoryScreen {
	h := &HistoryScreen{spec: spec, colors: compileColors(spec)}
	h.load()
	return h
}

func (h *HistoryScreen) load() {
	dir := opdir.Tool(h.spec.ID)
	entries, _ := os.ReadDir(dir)
	suffix := h.spec.Results.File
	for _, e := range entries {
		if e.IsDir() || (suffix != "" && !strings.HasSuffix(e.Name(), suffix)) {
			continue
		}
		fi, err := e.Info()
		if err != nil {
			continue
		}
		label := strings.TrimPrefix(strings.TrimSuffix(e.Name(), suffix), h.spec.ID+"-")
		h.runs = append(h.runs, histRun{filepath.Join(dir, e.Name()), fi.ModTime(), label})
	}
	// Newest first by the run timestamp embedded in the name (zero-padded
	// YYYY-MM-DD-HH-MM-SS sorts lexically = chronologically), so it's immune to
	// later mtime changes (copies, touches).
	sort.Slice(h.runs, func(i, j int) bool { return h.runs[i].label > h.runs[j].label })
}

func (h *HistoryScreen) Draw(c *Canvas) {
	content := drawChrome(c, strings.ToUpper(h.spec.Name)+" LOGS", "f/x:move  ent:open  esc:back")
	if len(h.runs) == 0 {
		c.TextCenteredIn(content, "no past runs", c.Faces().Body, colDim)
		return
	}
	const rowH = 16
	visible := content.Dy() / rowH
	if visible < 1 {
		visible = 1
	}
	if h.focus < h.scroll {
		h.scroll = h.focus
	}
	if h.focus >= h.scroll+visible {
		h.scroll = h.focus - visible + 1
	}
	for i := h.scroll; i < len(h.runs) && i < h.scroll+visible; i++ {
		y := content.Min.Y + (i-h.scroll)*rowH
		r := image.Rect(content.Min.X+4, y, content.Max.X-4, y+rowH-2)
		if i == h.focus {
			c.FillRect(r, colAccent)
			c.Text(r.Min.X+4, r.Min.Y, h.runs[i].label, c.Faces().Small, colFocusTx)
		} else {
			c.Text(r.Min.X+4, r.Min.Y, h.runs[i].label, c.Faces().Small, colText)
		}
	}
}

func (h *HistoryScreen) Key(ev Event) (Action, Screen) {
	switch ev.Key {
	case KeyUp:
		if h.focus > 0 {
			h.focus--
		}
	case KeyDown:
		if h.focus < len(h.runs)-1 {
			h.focus++
		}
	case KeyEnter:
		if len(h.runs) > 0 {
			return ActPush, NewRunView(h.spec, h.runs[h.focus], h.colors)
		}
	case KeyBack:
		return ActPop, nil
	}
	return ActNone, nil
}

// RunView renders one historical run's output — the same renderers as the live
// results page, for a fixed run (no polling, no subject since the inputs aren't
// recorded per-run). For a heatmap run, Enter opens the pan viewer.
type RunView struct {
	spec      tool.Spec
	lv        LogView
	lines     []Line
	imagePath string
}

func NewRunView(spec tool.Spec, run histRun, colors []colorRule) *RunView {
	rv := &RunView{spec: spec}
	rv.imagePath = imageSibling(spec, run.path)
	rv.lines = resultLines(spec, run.path, run.when, "", rv.imagePath, colors)
	return rv
}

func (rv *RunView) Draw(c *Canvas) {
	footer := "f/x:scroll  z/c:pan  esc:back"
	if rv.imagePath != "" {
		footer = "ent:view  esc:back"
	}
	content := drawChrome(c, strings.ToUpper(rv.spec.Name), footer)
	rv.lv.Draw(c, content, rv.lines)
}

func (rv *RunView) Key(ev Event) (Action, Screen) {
	if ev.Key == KeyBack {
		return ActPop, nil
	}
	if ev.Key == KeyEnter && rv.imagePath != "" {
		return ActPush, NewImageView(rv.imagePath)
	}
	rv.lv.Key(ev.Key)
	return ActNone, nil
}
