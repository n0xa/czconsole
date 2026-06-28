package lcd

import "image"

// MenuItem is a top-level tool entry. New builds the screen to push when chosen
// (nil → a "coming soon" placeholder).
type MenuItem struct {
	Label string
	New   func() Screen
}

// MenuScreen is the root tool list.
type MenuScreen struct {
	title string
	items []MenuItem
	focus int
}

// NewMenu builds the root menu: the tools czconsole already has, plus the recon
// tools we know we want in both the web and native frontends.
func NewMenu() *MenuScreen {
	return &MenuScreen{
		title: "KALI TOOLS",
		items: []MenuItem{
			{"Wardrive", func() Screen { return NewWardrive() }},
			{"rtl_433", nil},
			{"rtl_power", nil},
			{"Heatmap", nil},
			{"nmap", func() Screen { return NewNmap() }},
			{"gobuster", func() Screen { return NewGobuster() }},
		},
	}
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
		it := m.items[m.focus]
		if it.New != nil {
			return ActPush, it.New()
		}
		return ActPush, NewPlaceholder(it.Label)
	case KeyBack:
		return ActQuit, nil // root menu: back exits to APPLaunch
	}
	return ActNone, nil
}
