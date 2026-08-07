// Copyright (c) 2026 the go-freedesktop/notifications authors. All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

//go:build linux

package notifications

import (
	"errors"
	"math"
	"net"
	"testing"

	"github.com/go-freedesktop/dbus"
)

// The server's method handlers are unit-tested as plain Go calls with injected
// fakes for the three fallible D-Bus operations (export, request-name, emit).
// This gives full coverage of the server logic with no bus, no auth handshake
// and no connection I/O -- so a test can never hang.

// recHandler records the calls a Server forwards to it.
type recHandler struct {
	last   *Notification
	closes []struct {
		id     uint32
		reason CloseReason
	}
}

func (h *recHandler) OnNotify(n *Notification) uint32 { h.last = n; return n.ID }
func (h *recHandler) OnClose(id uint32, reason CloseReason) {
	h.closes = append(h.closes, struct {
		id     uint32
		reason CloseReason
	}{id, reason})
}

// emitRec captures the signals a Server emits through its emitFn seam.
type emitRec struct {
	path dbus.ObjectPath
	name string
	args []interface{}
}

func recordEmitter() (func(dbus.ObjectPath, string, ...interface{}) error, *[]emitRec) {
	var recs []emitRec
	fn := func(p dbus.ObjectPath, name string, args ...interface{}) error {
		recs = append(recs, emitRec{p, name, args})
		return nil
	}
	return fn, &recs
}

// newPipeConn returns a *dbus.Conn wrapping one end of an in-process pipe. It
// runs no auth and starts no I/O workers; only conn.Export (a pure in-memory
// object registration) is ever exercised on it, so nothing blocks on the wire.
func newPipeConn(t *testing.T) *dbus.Conn {
	t.Helper()
	a, b := net.Pipe()
	conn := dbus.NewConn(a)
	t.Cleanup(func() { _ = conn.Close(); _ = b.Close() })
	return conn
}

func TestNewServerWiresSeams(t *testing.T) {
	h := &recHandler{}
	s := NewServer(newPipeConn(t), h)
	if s.handler != h || s.exportFn == nil || s.requestNameFn == nil || s.emitFn == nil {
		t.Fatalf("NewServer left a seam unset: %+v", s)
	}
}

func TestNotifyDecodesAssignsAndAllocates(t *testing.T) {
	h := &recHandler{}
	s := NewServer(newPipeConn(t), h)
	x := &notifyExport{s: s}

	img := imageDataVariant(1, 1, 3, false, 8, 3, []byte{7, 8, 9})
	id, derr := x.Notify("Mail", 0, "mail", "Subject", "Body <b>x</b>",
		[]string{"default", "Open", "reply", "Reply"},
		map[string]dbus.Variant{
			"urgency":    dbus.MakeVariant(byte(UrgencyCritical)),
			"image-data": img,
		}, -1)
	if derr != nil {
		t.Fatalf("Notify returned error: %v", derr)
	}
	if id != 1 {
		t.Fatalf("first id = %d, want 1", id)
	}
	if h.last == nil || h.last.Summary != "Subject" || h.last.Urgency != UrgencyCritical ||
		h.last.Image == nil || len(h.last.Actions) != 2 {
		t.Fatalf("handler received wrong notification: %+v", h.last)
	}

	// The next fresh notification gets the next monotonic id.
	id2, _ := x.Notify("", 0, "", "s2", "", nil, nil, 1000)
	if id2 != 2 {
		t.Fatalf("second id = %d, want 2", id2)
	}
	// replaces_id is reused verbatim and does not advance the counter.
	id3, _ := x.Notify("", 5, "", "s3", "", nil, nil, 1000)
	if id3 != 5 {
		t.Fatalf("replaces id = %d, want 5", id3)
	}
	id4, _ := x.Notify("", 0, "", "s4", "", nil, nil, 1000)
	if id4 != 3 {
		t.Fatalf("id after replace = %d, want 3", id4)
	}
}

func TestAssignWrapsPastMaxUint32(t *testing.T) {
	s := &Server{handler: &recHandler{}}
	s.nextID = math.MaxUint32
	n := &Notification{}
	if got := s.assign(n); got != 1 || n.ID != 1 {
		t.Fatalf("wrap-around id = %d (n.ID=%d), want 1", got, n.ID)
	}
}

func TestCloseNotificationEmitsClosedReason(t *testing.T) {
	h := &recHandler{}
	emit, recs := recordEmitter()
	s := &Server{handler: h, emitFn: emit}
	x := &notifyExport{s: s}

	if derr := x.CloseNotification(7); derr != nil {
		t.Fatalf("CloseNotification error: %v", derr)
	}
	if len(h.closes) != 1 || h.closes[0].id != 7 || h.closes[0].reason != ReasonClosed {
		t.Fatalf("handler.OnClose = %v, want [{7 closed}]", h.closes)
	}
	if len(*recs) != 1 {
		t.Fatalf("emitted %d signals, want 1", len(*recs))
	}
	r := (*recs)[0]
	if r.path != dbus.ObjectPath(ObjectPath) || r.name != Interface+"."+SignalNotificationClosed {
		t.Fatalf("emit target = %s %s", r.path, r.name)
	}
	if len(r.args) != 2 || r.args[0].(uint32) != 7 || r.args[1].(uint32) != uint32(ReasonClosed) {
		t.Fatalf("emit args = %v, want [7 3]", r.args)
	}
}

func TestEmitHelpers(t *testing.T) {
	emit, recs := recordEmitter()
	s := &Server{emitFn: emit}

	if err := s.EmitClosed(3, ReasonExpired); err != nil {
		t.Fatal(err)
	}
	if err := s.EmitActionInvoked(3, "reply"); err != nil {
		t.Fatal(err)
	}
	if len(*recs) != 2 {
		t.Fatalf("emitted %d, want 2", len(*recs))
	}
	closed, action := (*recs)[0], (*recs)[1]
	if closed.name != Interface+"."+SignalNotificationClosed ||
		closed.args[1].(uint32) != uint32(ReasonExpired) {
		t.Fatalf("EmitClosed wire = %s %v", closed.name, closed.args)
	}
	if action.name != Interface+"."+SignalActionInvoked ||
		action.args[0].(uint32) != 3 || action.args[1].(string) != "reply" {
		t.Fatalf("EmitActionInvoked wire = %s %v", action.name, action.args)
	}
}

func TestGetCapabilitiesAndServerInformation(t *testing.T) {
	x := &notifyExport{s: &Server{}}

	caps, derr := x.GetCapabilities()
	if derr != nil {
		t.Fatal(derr)
	}
	if len(caps) != 5 || caps[0] != "body" || caps[4] != "persistence" {
		t.Fatalf("capabilities = %v", caps)
	}

	name, vendor, version, spec, derr := x.GetServerInformation()
	if derr != nil {
		t.Fatal(derr)
	}
	if name != ServerName || vendor != VendorName || version != Version || spec != SpecVersion {
		t.Fatalf("server info = %q/%q/%q/%q", name, vendor, version, spec)
	}
}

func TestExportBranches(t *testing.T) {
	conn := newPipeConn(t)
	boom := errors.New("boom")
	okExport := func(interface{}, dbus.ObjectPath, string) error { return nil }

	// Success: exportFn ok, name granted (introspection is served internally
	// by the connection, so Export registers no introspection node).
	s := &Server{conn: conn, handler: &recHandler{}, exportFn: okExport,
		requestNameFn: func(string, uint32) (uint32, error) {
			return dbus.NameReplyPrimaryOwner, nil
		}}
	if err := s.Export(); err != nil {
		t.Fatalf("Export success path: %v", err)
	}

	// exportFn failure short-circuits before the name request.
	s2 := &Server{conn: conn, exportFn: func(interface{}, dbus.ObjectPath, string) error { return boom }}
	if err := s2.Export(); !errors.Is(err, boom) {
		t.Fatalf("export error = %v, want boom", err)
	}

	// request-name failure propagates.
	s3 := &Server{conn: conn, exportFn: okExport,
		requestNameFn: func(string, uint32) (uint32, error) {
			return 0, boom
		}}
	if err := s3.Export(); !errors.Is(err, boom) {
		t.Fatalf("request-name error = %v, want boom", err)
	}

	// A non-primary-owner reply is ErrNameTaken.
	s4 := &Server{conn: conn, exportFn: okExport,
		requestNameFn: func(string, uint32) (uint32, error) {
			return dbus.NameReplyInQueue, nil
		}}
	if err := s4.Export(); !errors.Is(err, ErrNameTaken) {
		t.Fatalf("expected ErrNameTaken, got %v", err)
	}
}
