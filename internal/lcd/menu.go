package lcd

import (
	"image"
	"sort"
	"strings"

	"github.com/n0xa/czconsole/internal/tool"
)

// MenuItem is one row; New builds the screen to push when chosen.
type MenuItem struct {
	Label string
	New   func() Screen
}

// MenuScreen is a navigable list — used for both the root (groups) and the
// per-group submenus. quitOnBack distinguishes the root (Esc exits to APPLaunch)
// from a submenu (Esc pops back up).
type MenuScreen struct {
	title      string
	items      []MenuItem
	focus      int
	quitOnBack bool
}

// menuEntry is a tool/special before grouping.
type menuEntry struct {
	label string
	group string
	new   func() Screen
}

// NewMenu builds the root from the runtime JSON tool specs (grouped by their
// "group") plus the built-in special screens (Wardrive). Adding a tool is a spec
// drop-in — no Go change here.
func NewMenu() *MenuScreen {
	var entries []menuEntry
	specs, _ := tool.Load("") // /etc/czconsole/tools.d
	for _, sp := range specs {
		sp := sp
		g := sp.Group
		if g == "" {
			g = "Tools"
		}
		entries = append(entries, menuEntry{sp.Name, g, func() Screen { return NewToolScreen(sp) }})
	}
	// Built-in special screens that aren't (yet) specs.
	entries = append(entries, menuEntry{"Wardrive", "Wireless", func() Screen { return NewWardrive() }})

	return buildGroupMenu("KALI TOOLS", entries, true)
}

// buildGroupMenu groups entries and returns a menu of groups, each opening a
// submenu of its tools.
func buildGroupMenu(title string, entries []menuEntry, root bool) *MenuScreen {
	byGroup := map[string][]menuEntry{}
	var groups []string
	for _, e := range entries {
		if _, ok := byGroup[e.group]; !ok {
			groups = append(groups, e.group)
		}
		byGroup[e.group] = append(byGroup[e.group], e)
	}
	orderGroups(groups)

	var items []MenuItem
	for _, g := range groups {
		g, ges := g, byGroup[g]
		sort.Slice(ges, func(i, j int) bool { return ges[i].label < ges[j].label })
		items = append(items, MenuItem{Label: g, New: func() Screen { return newSubMenu(g, ges) }})
	}
	return &MenuScreen{title: title, items: items, quitOnBack: root}
}

func newSubMenu(title string, entries []menuEntry) *MenuScreen {
	items := make([]MenuItem, 0, len(entries))
	for _, e := range entries {
		items = append(items, MenuItem{Label: e.label, New: e.new})
	}
	return &MenuScreen{title: strings.ToUpper(title), items: items}
}

// orderGroups sorts in place: preferred groups first, then alphabetical.
func orderGroups(gs []string) {
	pref := map[string]int{"Wireless": 0, "Net Recon": 1}
	sort.Slice(gs, func(i, j int) bool {
		pi, oki := pref[gs[i]]
		pj, okj := pref[gs[j]]
		if oki && okj {
			return pi < pj
		}
		if oki != okj {
			return oki
		}
		return gs[i] < gs[j]
	})
}

func (m *MenuScreen) Draw(c *Canvas) {
	content := drawChrome(c, m.title, "f/x:move  ent:open  esc:back")
	const rowH = 20
	for i, it := range m.items {
		top := content.Min.Y + 4 + i*rowH
		r := image.Rect(content.Min.X+4, top, content.Max.X-4, top+rowH-2)
		if i == m.focus {
			c.FillRect(r, colAccent)
			c.Text(r.Min.X+6, r.Min.Y+1, it.Label, c.Faces().Body, colFocusTx)
		} else {
			c.Text(r.Min.X+6, r.Min.Y+1, it.Label, c.Faces().Body, colText)
		}
	}
}

func (m *MenuScreen) Key(ev Event) (Action, Screen) {
	switch ev.Key {
	case KeyUp:
		if m.focus > 0 {
			m.focus--
		}
	case KeyDown:
		if m.focus < len(m.items)-1 {
			m.focus++
		}
	case KeyEnter:
		if len(m.items) == 0 {
			break
		}
		it := m.items[m.focus]
		if it.New != nil {
			return ActPush, it.New()
		}
		return ActPush, NewPlaceholder(it.Label)
	case KeyBack:
		if m.quitOnBack {
			return ActQuit, nil // root: exit to APPLaunch
		}
		return ActPop, nil // submenu: back up
	}
	return ActNone, nil
}
