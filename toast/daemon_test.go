// Copyright (c) 2026 the go-freedesktop/notifications authors. All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

package toast

import (
	"testing"

	"github.com/go-freedesktop/notifications"
	"github.com/go-widgets/toolkit"
)

type closedSig struct {
	id     uint32
	reason notifications.CloseReason
}
type actionSig struct {
	id  uint32
	key string
}

// fakeEmitter records the signals a Daemon emits.
type fakeEmitter struct {
	closed  []closedSig
	actions []actionSig
}

func (f *fakeEmitter) EmitClosed(id uint32, r notifications.CloseReason) error {
	f.closed = append(f.closed, closedSig{id, r})
	return nil
}
func (f *fakeEmitter) EmitActionInvoked(id uint32, key string) error {
	f.actions = append(f.actions, actionSig{id, key})
	return nil
}

func newTestDaemon() (*Daemon, *fakeEmitter) {
	f := &fakeEmitter{}
	d := NewDaemon(nil, toolkit.DefaultLight(), nil)
	d.SetEmitter(f)
	return d, f
}

// note builds a notification with the given id and lifetime.
func note(id uint32, expireMS int32) *notifications.Notification {
	return &notifications.Notification{ID: id, Summary: "S", Body: "B", ExpireMS: expireMS, Urgency: notifications.UrgencyNormal}
}

func TestDaemonDefaults(t *testing.T) {
	d := NewDaemon(nil, toolkit.DefaultLight(), nil)
	if d.Corner != toolkit.BottomRight || d.Host.W != 400 || d.Host.H != 600 {
		t.Fatalf("unexpected defaults: corner=%d host=%+v", d.Corner, d.Host)
	}
}

func TestDaemonOnNotifyAddReplaceAndOrder(t *testing.T) {
	d, _ := newTestDaemon()
	if got := d.OnNotify(note(1, 500)); got != 1 {
		t.Fatalf("OnNotify returned %d, want 1", got)
	}
	d.OnNotify(note(2, 500))
	if len(d.Toasts()) != 2 {
		t.Fatalf("stack size = %d, want 2", len(d.Toasts()))
	}
	// Re-notify id 1: replaces in place, no growth.
	d.OnNotify(note(1, 500))
	ts := d.Toasts()
	if len(ts) != 2 {
		t.Fatalf("after replace, stack size = %d, want 2", len(ts))
	}
	// The two toasts occupy distinct stacked rows.
	if ts[0].Bounds().Y == ts[1].Bounds().Y {
		t.Fatal("stacked toasts share a Y; restack did not offset rows")
	}
}

func TestDaemonTickExpiryEmitsClosed(t *testing.T) {
	d, f := newTestDaemon()
	d.OnNotify(note(1, 50))   // Life 1 -> expires next tick
	d.OnNotify(note(2, 5000)) // Life 50 -> survives

	d.Tick() // id 1 expires
	if len(d.Toasts()) != 1 {
		t.Fatalf("after tick, stack = %d, want 1", len(d.Toasts()))
	}
	if len(f.closed) != 1 || f.closed[0] != (closedSig{1, notifications.ReasonExpired}) {
		t.Fatalf("closed signals = %v, want [{1 expired}]", f.closed)
	}

	// A tick with nothing expiring emits nothing more (len(expired)==0 path).
	d.Tick()
	if len(f.closed) != 1 {
		t.Fatalf("second tick emitted extra closes: %v", f.closed)
	}
}

func TestDaemonStickyNeverExpires(t *testing.T) {
	d, f := newTestDaemon()
	d.OnNotify(note(9, 0)) // Life 0 = sticky
	for i := 0; i < 5; i++ {
		d.Tick()
	}
	if len(d.Toasts()) != 1 {
		t.Fatal("sticky toast was retired")
	}
	if len(f.closed) != 0 {
		t.Fatalf("sticky toast emitted a close: %v", f.closed)
	}
}

func TestDaemonDismiss(t *testing.T) {
	d, f := newTestDaemon()
	d.OnNotify(note(3, 5000))
	d.Dismiss(3)
	if len(d.Toasts()) != 0 {
		t.Fatal("dismiss did not remove the toast")
	}
	if len(f.closed) != 1 || f.closed[0] != (closedSig{3, notifications.ReasonDismissed}) {
		t.Fatalf("closed = %v, want [{3 dismissed}]", f.closed)
	}
	// Dismissing an unknown id is a silent no-op.
	d.Dismiss(999)
	if len(f.closed) != 1 {
		t.Fatalf("dismiss unknown emitted a signal: %v", f.closed)
	}
}

func TestDaemonOnCloseRemovesWithoutEmit(t *testing.T) {
	d, f := newTestDaemon()
	d.OnNotify(note(4, 5000))
	// CloseNotification path: the server already emitted; OnClose only prunes.
	d.OnClose(4, notifications.ReasonClosed)
	if len(d.Toasts()) != 0 {
		t.Fatal("OnClose did not remove the toast")
	}
	if len(f.closed) != 0 {
		t.Fatalf("OnClose should not emit (server did): %v", f.closed)
	}
	// OnClose on an unknown id (remove-absent path) is harmless.
	d.OnClose(4, notifications.ReasonClosed)
}

func TestDaemonActionClickDismissesNonResident(t *testing.T) {
	d, f := newTestDaemon()
	n := note(5, 5000)
	n.Actions = []notifications.Action{{Key: "open", Label: "Open"}}
	d.OnNotify(n)

	tt := d.Toasts()[0]
	r := tt.ButtonRects()[0]
	tt.OnEvent(toolkit.Event{Kind: toolkit.EventClick, X: r.X + r.W/2, Y: r.Y + r.H/2})

	if len(f.actions) != 1 || f.actions[0] != (actionSig{5, "open"}) {
		t.Fatalf("actions = %v, want [{5 open}]", f.actions)
	}
	if len(f.closed) != 1 || f.closed[0].reason != notifications.ReasonDismissed {
		t.Fatalf("non-resident action should dismiss: %v", f.closed)
	}
	if len(d.Toasts()) != 0 {
		t.Fatal("toast should be gone after a non-resident action")
	}
}

func TestDaemonResidentActionKeepsToast(t *testing.T) {
	d, f := newTestDaemon()
	n := note(6, 0) // sticky
	n.Resident = true
	n.Actions = []notifications.Action{{Key: "snooze", Label: "Snooze"}}
	d.OnNotify(n)

	tt := d.Toasts()[0]
	r := tt.ButtonRects()[0]
	tt.OnEvent(toolkit.Event{Kind: toolkit.EventClick, X: r.X + 1, Y: r.Y + 1})

	if len(f.actions) != 1 {
		t.Fatalf("action not emitted: %v", f.actions)
	}
	if len(f.closed) != 0 {
		t.Fatalf("resident notification must not close on action: %v", f.closed)
	}
}

// TestDaemonOnActionUnknownID covers onAction when the entry is already gone:
// the ActionInvoked signal still fires, but no dismissal is attempted.
func TestDaemonOnActionUnknownID(t *testing.T) {
	d, f := newTestDaemon()
	d.onAction(12345, "ghost")
	if len(f.actions) != 1 || f.actions[0] != (actionSig{12345, "ghost"}) {
		t.Fatalf("actions = %v, want [{12345 ghost}]", f.actions)
	}
	if len(f.closed) != 0 {
		t.Fatalf("unknown-id action should not close anything: %v", f.closed)
	}
}
