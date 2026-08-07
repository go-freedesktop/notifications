// Copyright (c) 2026 the go-freedesktop/notifications authors. All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

// Package toast bridges a decoded freedesktop
// [github.com/go-freedesktop/notifications.Notification] onto a
// [github.com/go-widgets/toolkit.Toast] widget, so a go-widgets desktop can
// render notification bubbles and act as the notification daemon.
//
// [ToToast] is a pure function (no build tags, no I/O): it maps the
// notification's fields onto a Toast per the specification, wiring each action
// button to an emit callback. [Daemon] layers a small, platform-neutral stack
// manager and expiry tick loop on top, driving the two spec signals through an
// [Emitter]; the Linux D-Bus server
// ([github.com/go-freedesktop/notifications.Server]) satisfies Emitter.
package toast

import (
	"image"
	"strings"

	"github.com/go-freedesktop/notifications"
	"github.com/go-widgets/toolkit"
)

// Timing constants translating a notification timeout (in milliseconds) into a
// Toast Life budget (a count of [toolkit.Toast.Tick] calls). A daemon calls
// Tick every TickMS milliseconds, so Life = round(timeout / TickMS).
const (
	// TickMS is the assumed interval between Tick calls, in milliseconds.
	TickMS = 100
	// DefaultExpireMS is the lifetime used when the client asks for the
	// server default (a timeout of -1).
	DefaultExpireMS = 5000
)

// IconLookup resolves an icon name or file path to raster pixels for a Toast
// icon. It returns the tightly packed RGBA buffer (w*h*4 bytes) with its
// dimensions, and ok == false when the name cannot be resolved (the Toast then
// renders without an icon). It is the seam a daemon plugs an icon-theme lookup
// (github.com/go-freedesktop/icontheme + [notifications.LoadImagePath]) into; a
// nil IconLookup resolves nothing.
type IconLookup func(nameOrPath string) (pix []byte, w, h int, ok bool)

// ActionFunc receives the (notification id, action key) pair when a Toast
// action button is clicked. ToToast wires it to every rendered button's
// Callback so a click drives an ActionInvoked emission. A nil ActionFunc makes
// the buttons inert (they still dismiss the toast).
type ActionFunc func(id uint32, key string)

// LifeFor maps a notification's requested timeout onto a Toast Life budget: 0
// (the sticky sentinel) when the notification should persist until dismissed,
// otherwise the number of Tick calls covering the timeout (at least 1).
func LifeFor(n *notifications.Notification) int {
	if n.Sticky() {
		return 0
	}
	ms := n.ExpireMS
	if ms < 0 {
		ms = DefaultExpireMS
	}
	life := int(ms) / TickMS
	if life < 1 {
		life = 1
	}
	return life
}

// KindFor maps a notification's urgency onto a Toast kind: Critical urgency is
// an error pill, everything else an info pill (matching the advertised
// capability set, which does not distinguish success/warning).
func KindFor(n *notifications.Notification) toolkit.ToastKind {
	if n.Urgency == notifications.UrgencyCritical {
		return toolkit.ToastError
	}
	return toolkit.ToastInfo
}

// ToToast builds a Toast from a decoded notification. Summary and body become
// the Toast's stacked Lines (body markup stripped to plain text, honouring the
// advertised "body-markup" capability); urgency selects the Kind; the timeout
// (or resident/critical stickiness) sets Life; each non-default action becomes
// a right-edge button whose Callback emits ActionInvoked through onAction; and
// an inline image, image-path or app_icon (resolved through icons, in that
// order of preference) becomes the leading icon. The theme is accepted for API
// symmetry with the toolkit draw path and future themed tinting.
func ToToast(n *notifications.Notification, theme *toolkit.Theme, icons IconLookup, onAction ActionFunc) *toolkit.Toast {
	t := &toolkit.Toast{
		Kind:    KindFor(n),
		Life:    LifeFor(n),
		Visible: true,
		Lines:   linesFor(n),
	}
	for _, a := range n.Actions {
		if a.IsDefault() {
			continue
		}
		key, id := a.Key, n.ID
		t.Actions = append(t.Actions, toolkit.ToastAction{
			Label: a.Label,
			Callback: func() {
				if onAction != nil {
					onAction(id, key)
				}
			},
		})
	}
	if pix, w, h, ok := resolveIcon(n, icons); ok {
		t.Pixels, t.IW, t.IH = pix, w, h
	}
	return t
}

// linesFor returns the Toast rows: the (markup-stripped) summary first, then
// each line of the (markup-stripped) body.
func linesFor(n *notifications.Notification) []string {
	lines := []string{stripMarkup(n.Summary)}
	if body := stripMarkup(n.Body); body != "" {
		lines = append(lines, strings.Split(body, "\n")...)
	}
	return lines
}

// resolveIcon picks the notification's image in preference order -- inline
// image data, then image-path, then app_icon -- returning the first that
// yields pixels.
func resolveIcon(n *notifications.Notification, icons IconLookup) (pix []byte, w, h int, ok bool) {
	if n.Image != nil {
		p, iw, ih := rgbaPixels(n.Image)
		return p, iw, ih, true
	}
	if icons == nil {
		return nil, 0, 0, false
	}
	if n.ImagePath != "" {
		if p, iw, ih, found := icons(n.ImagePath); found {
			return p, iw, ih, true
		}
	}
	if n.AppIcon != "" {
		if p, iw, ih, found := icons(n.AppIcon); found {
			return p, iw, ih, true
		}
	}
	return nil, 0, 0, false
}

// rgbaPixels repacks an *image.RGBA into a tightly packed (stride == 4*w) RGBA
// byte buffer with its dimensions, regardless of the source stride or origin.
func rgbaPixels(img *image.RGBA) (pix []byte, w, h int) {
	b := img.Bounds()
	w, h = b.Dx(), b.Dy()
	pix = make([]byte, w*h*4)
	for y := 0; y < h; y++ {
		src := img.PixOffset(b.Min.X, b.Min.Y+y)
		copy(pix[y*w*4:(y+1)*w*4], img.Pix[src:src+w*4])
	}
	return pix, w, h
}

// stripMarkup removes the notification body's hypertext-subset markup tags
// (<b>, <i>, <a href>, <img>, ...) and decodes the five named XML entities,
// yielding plain text. It is intentionally forgiving: an unterminated '<' runs
// to end of string.
func stripMarkup(s string) string {
	if !strings.ContainsAny(s, "<&") {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	inTag := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '<':
			inTag = true
		case c == '>':
			inTag = false
		case inTag:
			// drop tag content
		case c == '&':
			ent, rest, ok := readEntity(s[i:])
			if ok {
				b.WriteString(ent)
				i += rest - 1
			} else {
				b.WriteByte(c)
			}
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

// readEntity decodes a leading XML entity in s (which begins with '&'),
// returning its replacement, the number of bytes consumed, and whether s
// started with a recognised entity.
func readEntity(s string) (string, int, bool) {
	for name, repl := range map[string]string{
		"&amp;":  "&",
		"&lt;":   "<",
		"&gt;":   ">",
		"&quot;": "\"",
		"&apos;": "'",
	} {
		if strings.HasPrefix(s, name) {
			return repl, len(name), true
		}
	}
	return "", 0, false
}
