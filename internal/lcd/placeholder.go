package lcd

// PlaceholderScreen is shown for tools that aren't wired up yet.
type PlaceholderScreen struct{ name string }

func NewPlaceholder(name string) *PlaceholderScreen { return &PlaceholderScreen{name: name} }

func (p *PlaceholderScreen) Draw(c *Canvas) {
	content := drawChrome(c, p.name, "esc:back")
	c.TextCenteredIn(content, "coming soon", c.Faces().Body, colDim)
}

func (p *PlaceholderScreen) Key(ev Event) (Action, Screen) {
	if ev.Key == KeyBack {
		return ActPop, nil
	}
	return ActNone, nil
}
