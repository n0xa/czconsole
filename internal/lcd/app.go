package lcd

import (
	"context"
	"time"

	"github.com/n0xa/czconsole/internal/fb"
)

// Action is what a screen's key handler asks the app to do next.
type Action int

const (
	ActNone Action = iota // stay; just redraw
	ActPush               // push the returned Screen
	ActPop                // pop back to the previous screen (quit at root)
	ActQuit               // exit the app (APPLaunch resumes)
)

// Screen is one full-screen view. Immediate mode: Draw renders current state;
// Key handles a press and tells the app what to do next.
type Screen interface {
	Draw(c *Canvas)
	Key(ev Event) (Action, Screen)
}

// App owns the framebuffer, the keypad stream, and a screen stack.
type App struct {
	disp   *fb.Display
	canvas *Canvas
	stack  []Screen
}

// NewApp opens the LCD and seeds the stack with root.
func NewApp(root Screen) (*App, error) {
	disp, err := fb.OpenDisplay()
	if err != nil {
		return nil, err
	}
	return &App{
		disp:   disp,
		canvas: newCanvas(disp.Bounds(), loadFaces()),
		stack:  []Screen{root},
	}, nil
}

func (a *App) top() Screen { return a.stack[len(a.stack)-1] }

func (a *App) draw() {
	a.top().Draw(a.canvas)
	a.disp.Present(a.canvas.img)
}

// Run renders and processes input until the app quits (root pop / quit) or ctx
// is cancelled. ~5 Hz redraw keeps live screens (wardrive stats) current.
func (a *App) Run(ctx context.Context) error {
	defer a.disp.Close()

	keys, err := ReadKeys(ctx)
	if err != nil {
		return err
	}

	tick := time.NewTicker(200 * time.Millisecond)
	defer tick.Stop()

	a.draw()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()

		case <-tick.C:
			a.draw() // refresh live data

		case ev, ok := <-keys:
			if !ok {
				return nil
			}
			action, next := a.top().Key(ev)
			switch action {
			case ActPush:
				if next != nil {
					a.stack = append(a.stack, next)
				}
			case ActPop:
				if len(a.stack) > 1 {
					old := a.stack[len(a.stack)-1]
					a.stack = a.stack[:len(a.stack)-1]
					// Let a screen release resources (e.g. a poller goroutine).
					if cl, ok := old.(interface{ Close() }); ok {
						cl.Close()
					}
				} else {
					return nil // popped past root → exit
				}
			case ActQuit:
				return nil
			}
			a.draw()
		}
	}
}
