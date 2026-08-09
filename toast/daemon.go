// Copyright (c) 2026 the go-freedesktop/notifications authors. All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

package toast

import (
	"sync"

	"github.com/go-freedesktop/notifications"
	"github.com/go-widgets/toolkit"
)

// DefaultCap is the default upper bound on the number of live notifications a
// Daemon keeps at once. When a new notification would exceed the cap the oldest
// live one is retired (closed, reason expired), so a flood of notifications can
// never grow the in-memory store without bound.
const DefaultCap = 100

// Emitter is the subset of the notification server the Daemon drives: emitting
// the two spec signals. github.com/go-freedesktop/notifications.Server
// satisfies it on Linux (and its non-Linux stub satisfies it too), so a Daemon
// stays platform-neutral and unit-testable with a fake Emitter.
type Emitter interface {
	EmitClosed(id uint32, reason notifications.CloseReason) error
	EmitActionInvoked(id uint32, key string) error
}

// entry is one live notification: the decoded value plus its rendered Toast.
type entry struct {
	n *notifications.Notification
	t *toolkit.Toast
}

// Daemon is a platform-neutral notification-surface manager: it implements
// [notifications.Handler], keeping a corner-anchored stack of Toasts, ticking
// their lifetimes and emitting NotificationClosed with the right reason --
// expiry (1), dismissal (2) or a CloseNotification call (3, emitted by the
// server itself). It is safe for concurrent use.
//
// A go-widgets desktop wires a Daemon to a notifications.Server as its Handler,
// calls Tick from its animation loop, renders Toasts, and routes pointer clicks
// into each Toast (whose action buttons call back into the Daemon to emit
// ActionInvoked). Host and Corner control where the stack anchors.
type Daemon struct {
	// Host is the rectangle the toast stack anchors inside; Corner is the
	// corner it docks to. Both may be set before use (defaults: a 400x600
	// region docked BottomRight).
	Host   toolkit.Rect
	Corner toolkit.Corner

	// Cap bounds the number of live notifications kept at once (default
	// DefaultCap). A value <= 0 means unbounded. When a new notification
	// would exceed Cap the oldest live one is retired.
	Cap int

	emit  Emitter
	theme *toolkit.Theme
	icons IconLookup

	mu      sync.Mutex
	entries []*entry
}

// NewDaemon returns a Daemon that emits signals through emit, renders Toasts
// with theme, and resolves icons through icons (which may be nil).
func NewDaemon(emit Emitter, theme *toolkit.Theme, icons IconLookup) *Daemon {
	return &Daemon{
		Host:   toolkit.Rect{X: 0, Y: 0, W: 400, H: 600},
		Corner: toolkit.BottomRight,
		Cap:    DefaultCap,
		emit:   emit,
		theme:  theme,
		icons:  icons,
	}
}

// SetEmitter installs (or replaces) the Emitter the Daemon drives. It exists
// to break the construction cycle between a Daemon (which needs a server to
// emit through) and a server (which needs the Daemon as its Handler): build the
// Daemon with a nil Emitter, hand it to the server, then call SetEmitter with
// the server.
func (d *Daemon) SetEmitter(e Emitter) {
	d.mu.Lock()
	d.emit = e
	d.mu.Unlock()
}

// OnNotify renders n into a Toast and adds it to the stack (replacing an
// existing entry with the same id), then returns n.ID. If adding it would push
// the live count past Cap, the oldest notifications are retired to make room
// (each closed with reason expired). It implements notifications.Handler.
func (d *Daemon) OnNotify(n *notifications.Notification) uint32 {
	t := ToToast(n, d.theme, d.icons, d.onAction)
	d.mu.Lock()
	if i := d.indexOf(n.ID); i >= 0 {
		d.entries[i] = &entry{n: n, t: t}
	} else {
		d.entries = append(d.entries, &entry{n: n, t: t})
	}
	var evicted []uint32
	for d.Cap > 0 && len(d.entries) > d.Cap {
		evicted = append(evicted, d.entries[0].n.ID)
		d.entries = d.entries[1:]
	}
	d.restack()
	d.mu.Unlock()
	for _, id := range evicted {
		_ = d.emit.EmitClosed(id, notifications.ReasonExpired)
	}
	return n.ID
}

// OnClose removes the notification's surface. The reason is ignored here
// because the only path that reaches OnClose -- a CloseNotification call --
// has the server emit NotificationClosed(closed) itself; expiry and dismissal
// go through Tick and Dismiss, which emit their own reasons. It implements
// notifications.Handler.
func (d *Daemon) OnClose(id uint32, reason notifications.CloseReason) {
	d.mu.Lock()
	d.remove(id)
	d.restack()
	d.mu.Unlock()
}

// Tick advances every toast's lifetime by one step, retiring those whose Life
// budget has run out and emitting NotificationClosed(expired) for each. A
// daemon calls it once per TickMS milliseconds.
func (d *Daemon) Tick() {
	d.mu.Lock()
	var expired []uint32
	for _, e := range d.entries {
		e.t.Tick()
		if !e.t.Visible {
			expired = append(expired, e.n.ID)
		}
	}
	for _, id := range expired {
		d.remove(id)
	}
	if len(expired) > 0 {
		d.restack()
	}
	d.mu.Unlock()
	for _, id := range expired {
		_ = d.emit.EmitClosed(id, notifications.ReasonExpired)
	}
}

// Dismiss retires the notification with id as a user dismissal, emitting
// NotificationClosed(dismissed). It is a no-op (no signal) if no such
// notification is live.
func (d *Daemon) Dismiss(id uint32) {
	d.mu.Lock()
	found := d.indexOf(id) >= 0
	d.remove(id)
	d.restack()
	d.mu.Unlock()
	if found {
		_ = d.emit.EmitClosed(id, notifications.ReasonDismissed)
	}
}

// Toasts returns the current stack of visible Toasts in anchor order, for the
// host to render.
func (d *Daemon) Toasts() []*toolkit.Toast {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]*toolkit.Toast, len(d.entries))
	for i, e := range d.entries {
		out[i] = e.t
	}
	return out
}

// onAction emits ActionInvoked for a clicked action button and then, unless the
// notification is resident, dismisses it (emitting NotificationClosed(dismissed)).
func (d *Daemon) onAction(id uint32, key string) {
	_ = d.emit.EmitActionInvoked(id, key)
	d.mu.Lock()
	i := d.indexOf(id)
	resident := i >= 0 && d.entries[i].n.Resident
	d.mu.Unlock()
	if i >= 0 && !resident {
		d.Dismiss(id)
	}
}

// indexOf returns the index of the entry for id, or -1. Callers hold d.mu.
func (d *Daemon) indexOf(id uint32) int {
	for i, e := range d.entries {
		if e.n.ID == id {
			return i
		}
	}
	return -1
}

// remove deletes the entry for id if present. Callers hold d.mu.
func (d *Daemon) remove(id uint32) {
	if i := d.indexOf(id); i >= 0 {
		d.entries = append(d.entries[:i], d.entries[i+1:]...)
	}
}

// restack re-anchors every toast to its row in the stack. Callers hold d.mu.
func (d *Daemon) restack() {
	for i, e := range d.entries {
		e.t.AnchorIn(d.Host, d.Corner, i)
	}
}
