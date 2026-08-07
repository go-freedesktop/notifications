// Copyright (c) 2026 the go-freedesktop/notifications authors. All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

// Command notifyd is the reference go-widgets notification daemon: it owns the
// org.freedesktop.Notifications name on the session bus and renders each
// incoming notification as a go-widgets Toast, driven by the
// github.com/go-freedesktop/notifications server and its ./toast bridge.
//
// It is a working skeleton: on a real Linux desktop it claims the name, decodes
// Notify calls, stacks Toasts, ticks their lifetimes and emits the
// NotificationClosed / ActionInvoked signals. Presenting the rendered pixel
// surface to the screen is the host compositor's job (go-widgets is a pure
// pixel-blitting toolkit); this binary renders each frame to an offscreen
// buffer so the draw path is exercised end to end.
package main

import (
	"image"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-freedesktop/icontheme"
	"github.com/go-freedesktop/notifications"
	ntoast "github.com/go-freedesktop/notifications/toast"
	"github.com/go-widgets/painter"
	"github.com/go-widgets/toolkit"
	"github.com/godbus/dbus/v5"
)

// iconSize is the nominal pixel size requested from the icon theme.
const iconSize = 48

// surfaceW / surfaceH are the offscreen render surface dimensions.
const (
	surfaceW = 480
	surfaceH = 720
)

func main() {
	os.Exit(run())
}

// run wires the daemon together and blocks until interrupted, returning the
// process exit code.
func run() int {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		log.Printf("notifyd: connect session bus: %v", err)
		return 1
	}
	defer conn.Close()

	theme := toolkit.DefaultDark()
	daemon := ntoast.NewDaemon(nil, theme, newIconLookup())
	server := notifications.NewServer(conn, daemon)
	daemon.SetEmitter(server) // break the daemon<->server construction cycle

	if err := server.Export(); err != nil {
		log.Printf("notifyd: export service: %v", err)
		return 1
	}
	log.Printf("notifyd: owning %s (spec %s)", notifications.BusName, notifications.SpecVersion)

	surface := make([]byte, surfaceW*surfaceH*4)
	p := painter.NewPixelPainter(surface, surfaceW, surfaceH)

	ticker := time.NewTicker(ntoast.TickMS * time.Millisecond)
	defer ticker.Stop()
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)

	for {
		select {
		case <-ticker.C:
			daemon.Tick()
			render(p, theme, daemon)
		case <-sig:
			log.Printf("notifyd: shutting down")
			return 0
		}
	}
}

// render paints the current toast stack onto the offscreen surface.
func render(p painter.Painter, theme *toolkit.Theme, daemon *ntoast.Daemon) {
	for _, t := range daemon.Toasts() {
		t.Draw(p, theme)
	}
}

// newIconLookup builds an IconLookup that resolves a file path or an icon-theme
// name to raster pixels, using the user's configured icon theme (falling back
// to hicolor).
func newIconLookup() ntoast.IconLookup {
	themeName := os.Getenv("ICON_THEME")
	if themeName == "" {
		themeName = icontheme.HicolorTheme
	}
	th := icontheme.New(themeName)
	return func(nameOrPath string) ([]byte, int, int, bool) {
		if img, err := notifications.LoadImagePath(nameOrPath); err == nil {
			return tighten(img)
		}
		path, err := th.FindIcon([]string{nameOrPath}, iconSize, 1)
		if err != nil {
			return nil, 0, 0, false
		}
		img, err := notifications.LoadImagePath(path)
		if err != nil {
			return nil, 0, 0, false
		}
		return tighten(img)
	}
}

// tighten repacks an *image.RGBA into a tightly packed RGBA buffer with its
// dimensions.
func tighten(img *image.RGBA) ([]byte, int, int, bool) {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	pix := make([]byte, w*h*4)
	for y := 0; y < h; y++ {
		src := img.PixOffset(b.Min.X, b.Min.Y+y)
		copy(pix[y*w*4:(y+1)*w*4], img.Pix[src:src+w*4])
	}
	return pix, w, h, true
}
