// Copyright (c) 2026 the go-freedesktop/notifications authors. All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

//go:build linux

// This end-to-end test drives the notification service against a REAL
// dbus-daemon and is therefore gated: it runs only when
// NOTIFICATIONS_INTEGRATION=1 (set it under `dbus-run-session`). It proves the
// migration onto the pure-Go github.com/godbus/dbus/v5 works on the
// live session bus exactly as libnotify / notify-send would exercise it:
// export the service and claim the name, then from a second connection call
// GetServerInformation, GetCapabilities, Notify and CloseNotification and
// receive the ActionInvoked and NotificationClosed signals.
package notifications_test

import (
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/go-freedesktop/notifications"
	"github.com/godbus/dbus/v5"
)

// intHandler records what the server forwards and hands each notification the
// id the server assigned.
type intHandler struct {
	lastNotify *notifications.Notification
	closedID   uint32
	closedWhy  notifications.CloseReason
}

func (h *intHandler) OnNotify(n *notifications.Notification) uint32 {
	h.lastNotify = n
	return n.ID
}

func (h *intHandler) OnClose(id uint32, reason notifications.CloseReason) {
	h.closedID = id
	h.closedWhy = reason
}

func TestIntegrationRealBus(t *testing.T) {
	if os.Getenv("NOTIFICATIONS_INTEGRATION") != "1" {
		t.Skip("set NOTIFICATIONS_INTEGRATION=1 (under dbus-run-session) to run the real-bus test")
	}

	// --- server side: export the service on the real session bus ---
	srvConn, err := dbus.ConnectSessionBus()
	if err != nil {
		t.Fatalf("ConnectSessionBus (server): %v", err)
	}
	defer srvConn.Close()

	h := &intHandler{}
	server := notifications.NewServer(srvConn, h)
	if err := server.Export(); err != nil {
		t.Fatalf("server.Export: %v", err)
	}
	t.Logf("server owns %s as %v", notifications.BusName, srvConn.Names())

	// --- client side: a second connection acting as a notifying app ---
	cli, err := dbus.ConnectSessionBus()
	if err != nil {
		t.Fatalf("ConnectSessionBus (client): %v", err)
	}
	defer cli.Close()
	obj := cli.Object(notifications.BusName, dbus.ObjectPath(notifications.ObjectPath))

	// Subscribe to the two spec signals before triggering anything.
	if err := cli.AddMatchSignal(dbus.WithMatchInterface(notifications.Interface)); err != nil {
		t.Fatalf("AddMatchSignal: %v", err)
	}
	sigCh := make(chan *dbus.Signal, 8)
	cli.Signal(sigCh)

	// --- GetServerInformation ---
	var name, vendor, version, spec string
	if err := obj.Call(notifications.Interface+".GetServerInformation", 0).
		Store(&name, &vendor, &version, &spec); err != nil {
		t.Fatalf("GetServerInformation: %v", err)
	}
	if name != notifications.ServerName || vendor != notifications.VendorName ||
		version != notifications.Version || spec != notifications.SpecVersion {
		t.Fatalf("GetServerInformation = %q/%q/%q/%q", name, vendor, version, spec)
	}
	t.Logf("GetServerInformation: %s %s %s (spec %s)", name, vendor, version, spec)

	// --- GetCapabilities ---
	var caps []string
	if err := obj.Call(notifications.Interface+".GetCapabilities", 0).Store(&caps); err != nil {
		t.Fatalf("GetCapabilities: %v", err)
	}
	if !reflect.DeepEqual(caps, notifications.Capabilities()) {
		t.Fatalf("GetCapabilities = %v, want %v", caps, notifications.Capabilities())
	}
	t.Logf("GetCapabilities: %v", caps)

	// --- Notify (full susssasa{sv}i signature) ---
	hints := map[string]dbus.Variant{
		"urgency":  dbus.MakeVariant(byte(notifications.UrgencyCritical)),
		"category": dbus.MakeVariant("email.arrived"),
	}
	var id uint32
	if err := obj.Call(notifications.Interface+".Notify", 0,
		"IntegrationApp",            // app_name
		uint32(0),                   // replaces_id
		"mail-unread",               // app_icon
		"New message",               // summary
		"Hello from the real bus",   // body
		[]string{"default", "Open"}, // actions
		hints,                       // hints
		int32(-1),                   // expire_timeout
	).Store(&id); err != nil {
		t.Fatalf("Notify: %v", err)
	}
	if id == 0 {
		t.Fatal("Notify returned id 0")
	}
	if h.lastNotify == nil || h.lastNotify.Summary != "New message" ||
		h.lastNotify.Urgency != notifications.UrgencyCritical || len(h.lastNotify.Actions) != 1 {
		t.Fatalf("handler received wrong notification: %+v", h.lastNotify)
	}
	t.Logf("Notify assigned id %d, handler decoded summary=%q urgency=%v",
		id, h.lastNotify.Summary, h.lastNotify.Urgency)

	// --- ActionInvoked signal round-trips server -> bus -> client ---
	if err := server.EmitActionInvoked(id, "default"); err != nil {
		t.Fatalf("EmitActionInvoked: %v", err)
	}
	waitSignal(t, sigCh, notifications.Interface+"."+notifications.SignalActionInvoked,
		func(body []interface{}) bool {
			return len(body) == 2 && body[0].(uint32) == id && body[1].(string) == "default"
		})
	t.Logf("received ActionInvoked(%d, default)", id)

	// --- CloseNotification drives OnClose and emits NotificationClosed ---
	if err := obj.Call(notifications.Interface+".CloseNotification", 0, id).Err; err != nil {
		t.Fatalf("CloseNotification: %v", err)
	}
	if h.closedID != id || h.closedWhy != notifications.ReasonClosed {
		t.Fatalf("handler.OnClose = (%d,%v), want (%d,%v)",
			h.closedID, h.closedWhy, id, notifications.ReasonClosed)
	}
	waitSignal(t, sigCh, notifications.Interface+"."+notifications.SignalNotificationClosed,
		func(body []interface{}) bool {
			return len(body) == 2 && body[0].(uint32) == id &&
				body[1].(uint32) == uint32(notifications.ReasonClosed)
		})
	t.Logf("received NotificationClosed(%d, closed)", id)
}

// waitSignal blocks until a signal named want whose body satisfies ok arrives,
// or the test's short deadline elapses. It ignores unrelated bus traffic (e.g.
// NameAcquired). It never blocks indefinitely.
func waitSignal(t *testing.T, ch <-chan *dbus.Signal, want string, ok func([]interface{}) bool) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case sig := <-ch:
			if sig.Name != want {
				continue
			}
			if !ok(sig.Body) {
				t.Fatalf("signal %s body mismatch: %#v", want, sig.Body)
			}
			return
		case <-deadline:
			t.Fatalf("timed out waiting for signal %s", want)
		}
	}
}
