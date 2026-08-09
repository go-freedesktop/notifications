// Copyright (c) 2026 the go-freedesktop/notifications authors. All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

// Command notifyd is the reference go-widgets notification daemon: it owns the
// org.freedesktop.Notifications name on the session bus and renders each
// incoming notification as a go-widgets Toast, driven by the
// github.com/go-freedesktop/notifications server and its ./toast bridge.
//
// On a real Linux desktop it claims the name, decodes Notify calls, stacks
// Toasts, ticks their lifetimes and emits the NotificationClosed /
// ActionInvoked signals. Presenting the rendered pixel surface to the screen is
// the host compositor's job (go-widgets is a pure pixel-blitting toolkit); this
// binary renders each frame to an offscreen buffer so the draw path is
// exercised end to end.
//
// Lifecycle: it fails clearly if another notification daemon already owns the
// name (unless -replace is given, which asks the bus to hand the name over),
// and shuts down gracefully on SIGINT/SIGTERM -- releasing the name and closing
// the connection so a replacement daemon can start cleanly. A malformed or
// panicking notification is isolated and never brings the daemon down.
//
// Usage:
//
//	notifyd [-replace] [-timeout MS] [-verbose] [-version]
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"image"
	"io"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-freedesktop/dbus"
	"github.com/go-freedesktop/icontheme"
	"github.com/go-freedesktop/notifications"
	ntoast "github.com/go-freedesktop/notifications/toast"
	"github.com/go-widgets/painter"
	"github.com/go-widgets/toolkit"
)

// iconSize is the nominal pixel size requested from the icon theme.
const iconSize = 48

// surfaceW / surfaceH are the offscreen render surface dimensions.
const (
	surfaceW = 480
	surfaceH = 720
)

// Process exit codes.
const (
	exitOK        = 0 // clean startup + graceful shutdown
	exitError     = 1 // an unexpected failure (bus connect, export, ...)
	exitUsage     = 2 // bad command-line flags
	exitNameTaken = 3 // another notification daemon already owns the name
)

func main() {
	os.Exit(run(os.Args[1:], os.Stderr))
}

// config is the parsed command-line configuration.
type config struct {
	replace   bool // take the name over from a running daemon (-replace)
	timeoutMS int  // server-default expiry, applied when a client asks for -1
	verbose   bool // log every notification (-verbose)
	version   bool // print version and exit (-version)
}

// parseFlags parses argv into a config, writing usage/errors to out. It returns
// flag.ErrHelp when -h/-help was requested (the caller should exit 0).
func parseFlags(args []string, out io.Writer) (config, error) {
	var cfg config
	fs := flag.NewFlagSet("notifyd", flag.ContinueOnError)
	fs.SetOutput(out)
	fs.BoolVar(&cfg.replace, "replace", false,
		"take the org.freedesktop.Notifications name over from a running daemon")
	fs.IntVar(&cfg.timeoutMS, "timeout", ntoast.DefaultExpireMS,
		"default notification lifetime in ms, used when a client requests the server default")
	fs.BoolVar(&cfg.verbose, "verbose", false, "log every notification received")
	fs.BoolVar(&cfg.version, "version", false, "print version and exit")
	fs.Usage = func() {
		fmt.Fprintf(out, "Usage: notifyd [flags]\n\n"+
			"The reference go-freedesktop notification daemon: owns %s on the\n"+
			"session bus and renders each notification as a go-widgets Toast.\n\nFlags:\n",
			notifications.BusName)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return config{}, err
	}
	if cfg.timeoutMS < 1 {
		cfg.timeoutMS = ntoast.DefaultExpireMS
	}
	return cfg, nil
}

// run is the process body: it returns the exit code. out receives usage and
// diagnostics.
func run(args []string, out io.Writer) int {
	logger := log.New(out, "", log.LstdFlags)

	cfg, err := parseFlags(args, out)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return exitUsage
	}
	if cfg.version {
		fmt.Fprintf(out, "notifyd %s (spec %s)\n", notifications.Version, notifications.SpecVersion)
		return exitOK
	}

	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		logger.Printf("notifyd: connect session bus: %v", err)
		return exitError
	}
	defer conn.Close()

	theme := toolkit.DefaultDark()
	daemon := ntoast.NewDaemon(nil, theme, newIconLookup())
	handler := &safeHandler{inner: daemon, timeoutMS: cfg.timeoutMS, verbose: cfg.verbose, logf: logger.Printf}
	server := notifications.NewServer(conn, handler)
	daemon.SetEmitter(server) // break the daemon<->server construction cycle

	if code, ok := claim(server, cfg.replace, logger.Printf); !ok {
		return code
	}
	logger.Printf("notifyd: owning %s (spec %s, default timeout %dms)",
		notifications.BusName, notifications.SpecVersion, cfg.timeoutMS)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	return serve(ctx, conn, theme, daemon, logger.Printf)
}

// exporter is the subset of *notifications.Server that claims the well-known
// name; a fake stands in for it in tests.
type exporter interface {
	Export() error
	ExportReplace() error
}

// claim exports the service -- replacing a running daemon when replace is set --
// and maps the outcome to (exit code, ok). ok is true only when the name was
// claimed; otherwise it logs an informative message and returns the exit code
// the caller should exit with.
func claim(s exporter, replace bool, logf func(string, ...any)) (int, bool) {
	var err error
	if replace {
		err = s.ExportReplace()
	} else {
		err = s.Export()
	}
	switch {
	case err == nil:
		return exitOK, true
	case errors.Is(err, notifications.ErrNameTaken):
		logf("notifyd: %s is already owned by another notification daemon; "+
			"stop it first or re-run with -replace", notifications.BusName)
		return exitNameTaken, false
	default:
		logf("notifyd: export service: %v", err)
		return exitError, false
	}
}

// serve runs the tick/render loop until ctx is cancelled (SIGINT/SIGTERM), then
// shuts down gracefully: releasing the well-known name so a replacement daemon
// can claim it (conn.Close is the caller's deferred final step). It returns the
// process exit code.
func serve(ctx context.Context, conn *dbus.Conn, theme *toolkit.Theme,
	daemon *ntoast.Daemon, logf func(string, ...any)) int {
	surface := make([]byte, surfaceW*surfaceH*4)
	p := painter.NewPixelPainter(surface, surfaceW, surfaceH)

	ticker := time.NewTicker(ntoast.TickMS * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			daemon.Tick()
			render(p, theme, daemon)
		case <-ctx.Done():
			logf("notifyd: shutting down, releasing %s", notifications.BusName)
			if _, err := conn.ReleaseName(notifications.BusName); err != nil {
				logf("notifyd: release name: %v", err)
			}
			return exitOK
		}
	}
}

// safeHandler wraps a notifications.Handler with per-notification panic
// isolation (a malformed or panicking notification is logged and dropped rather
// than crashing the D-Bus reader goroutine) and applies the configured default
// timeout to notifications that request the server default.
type safeHandler struct {
	inner     notifications.Handler
	timeoutMS int
	verbose   bool
	logf      func(string, ...any)
}

// OnNotify forwards to the wrapped handler under a recover, first substituting
// the configured default timeout for a server-default (-1) request. On a panic
// it logs and returns the notification's already-assigned id, so the caller
// still gets a valid reply.
func (h *safeHandler) OnNotify(n *notifications.Notification) (id uint32) {
	if n.ExpireMS < 0 {
		n.ExpireMS = int32(h.timeoutMS)
	}
	if h.verbose {
		h.logf("notifyd: notify id=%d app=%q urgency=%s summary=%q",
			n.ID, n.AppName, n.Urgency, n.Summary)
	}
	defer func() {
		if r := recover(); r != nil {
			h.logf("notifyd: recovered from panic handling notification id=%d: %v", n.ID, r)
			id = n.ID
		}
	}()
	return h.inner.OnNotify(n)
}

// OnClose forwards to the wrapped handler under a recover.
func (h *safeHandler) OnClose(id uint32, reason notifications.CloseReason) {
	defer func() {
		if r := recover(); r != nil {
			h.logf("notifyd: recovered from panic closing notification id=%d: %v", id, r)
		}
	}()
	h.inner.OnClose(id, reason)
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
