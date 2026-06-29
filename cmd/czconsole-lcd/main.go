// Command czconsole-lcd is the native on-screen frontend for the CardputerZero.
//
// It is a separate, single-purpose binary from the web worker: launched as an
// APPLaunch tile, it owns the st7789 framebuffer and the integrated keypad while
// it's the foreground program (APPLaunch cedes the framebuffer to it and resumes
// on exit). It shares czconsole's internal packages (wardrive, sysinfo, fb) so
// the native display and the web companion are one experience, two frontends.
//
// Build (pure Go, no cgo — cross-compiles from any host):
//
//	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -o czconsole-lcd ./cmd/czconsole-lcd
package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/n0xa/czconsole/internal/lcd"
	"github.com/n0xa/czconsole/internal/tool"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Debug: jump straight to a screen (e.g. CZLCD_START=wardrive) for testing.
	var root lcd.Screen = lcd.NewMenu()
	// Debug: jump straight to a screen. CZLCD_START=wardrive, or any tool id
	// (loads its spec into the generic ToolScreen).
	switch v := os.Getenv("CZLCD_START"); v {
	case "":
	case "wardrive":
		root = lcd.NewWardrive()
	default:
		if p, ok := strings.CutPrefix(v, "image:"); ok {
			root = lcd.NewImageView(p)
		} else if p, ok := strings.CutPrefix(v, "history:"); ok {
			if sp, err := tool.LoadByID(p); err == nil {
				root = lcd.NewHistory(sp)
			}
		} else if sp, err := tool.LoadByID(v); err == nil {
			root = lcd.NewToolScreen(sp)
		}
	}

	app, err := lcd.NewApp(root)
	if err != nil {
		log.Fatalf("czconsole-lcd: %v", err)
	}
	if err := app.Run(ctx); err != nil && err != context.Canceled {
		log.Fatalf("czconsole-lcd: %v", err)
	}
}
