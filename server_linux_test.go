// Copyright (c) 2026 the go-freedesktop/notifications authors. All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

//go:build linux

package notifications

import (
	"bufio"
	"errors"
	"io"
	"math"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
)

// --- peer-to-peer harness (no dbus-daemon) --------------------------------
//
// godbus has no server role, so two client Conns are wired through a middle
// goroutine that plays the SASL auth-server for each end and then splices the
// two byte streams. This exercises the real susssasa{sv}i marshalling over an
// in-memory net.Pipe with zero external processes.

// authServe performs the bus side of the EXTERNAL SASL handshake for one
// client end, consuming exactly up to and including "BEGIN\r\n".
func authServe(c net.Conn) error {
	r := bufio.NewReader(c)
	if _, err := r.ReadByte(); err != nil { // leading null byte
		return err
	}
	if _, err := r.ReadBytes('\n'); err != nil { // "AUTH"
		return err
	}
	if _, err := c.Write([]byte("REJECTED EXTERNAL\r\n")); err != nil {
		return err
	}
	if _, err := r.ReadBytes('\n'); err != nil { // "AUTH EXTERNAL <hex>"
		return err
	}
	if _, err := c.Write([]byte("OK 0123456789abcdef0123456789abcdef\r\n")); err != nil {
		return err
	}
	if _, err := r.ReadBytes('\n'); err != nil { // "BEGIN"
		return err
	}
	if r.Buffered() != 0 {
		return errors.New("auth: client over-read past BEGIN")
	}
	return nil
}

// pipePair returns two authenticated, spliced godbus connections.
func pipePair(t *testing.T) (server, client *dbus.Conn) {
	t.Helper()
	sConn, sMid := net.Pipe()
	cConn, cMid := net.Pipe()

	go func() {
		if authServe(sMid) != nil || authServe(cMid) != nil {
			return
		}
		go func() { _, _ = io.Copy(sMid, cMid) }()
		_, _ = io.Copy(cMid, sMid)
	}()

	var err error
	if server, err = dbus.NewConn(sConn); err != nil {
		t.Fatal(err)
	}
	if client, err = dbus.NewConn(cConn); err != nil {
		t.Fatal(err)
	}
	if err = server.Auth([]dbus.Auth{dbus.AuthExternal("0")}); err != nil {
		t.Fatalf("server auth: %v", err)
	}
	if err = client.Auth([]dbus.Auth{dbus.AuthExternal("0")}); err != nil {
		t.Fatalf("client auth: %v", err)
	}
	t.Cleanup(func() { _ = server.Close(); _ = client.Close() })
	return server, client
}

// recHandler records the calls a Server forwards to it. godbus dispatches
// method calls on their own goroutines, so access is mutex-guarded for -race.
type recHandler struct {
	mu     sync.Mutex
	last   *Notification
	closes []struct {
		id     uint32
		reason CloseReason
	}
}

func (h *recHandler) OnNotify(n *Notification) uint32 {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.last = n
	return n.ID
}
func (h *recHandler) OnClose(id uint32, reason CloseReason) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.closes = append(h.closes, struct {
		id     uint32
		reason CloseReason
	}{id, reason})
}
func (h *recHandler) lastNote() *Notification {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.last
}
func (h *recHandler) closeCount() (int, CloseReason) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.closes) == 0 {
		return 0, 0
	}
	return len(h.closes), h.closes[0].reason
}

// --- tests ----------------------------------------------------------------

func TestServerP2PNotifyCloseAndSignals(t *testing.T) {
	server, client := pipePair(t)
	h := &recHandler{}
	s := NewServer(server, h)
	// Fake the name request so Export needs no live bus (fleet fake-injection).
	s.requestNameFn = func(string, dbus.RequestNameFlags) (dbus.RequestNameReply, error) {
		return dbus.RequestNameReplyPrimaryOwner, nil
	}
	if err := s.Export(); err != nil {
		t.Fatalf("Export: %v", err)
	}

	sigs := make(chan *dbus.Signal, 8)
	client.Signal(sigs)

	obj := client.Object(BusName, dbus.ObjectPath(ObjectPath))

	// Notify with a real image-data hint to drive full susssasa{sv}i marshalling.
	imgData := imageDataVariant(1, 1, 3, false, 8, 3, []byte{7, 8, 9})
	var id uint32
	err := obj.Call(Interface+"."+MethodNotify, 0,
		"Mail", uint32(0), "mail", "Subject", "Body <b>x</b>",
		[]string{"default", "Open", "reply", "Reply"},
		map[string]dbus.Variant{
			"urgency":    dbus.MakeVariant(byte(UrgencyCritical)),
			"image-data": imgData,
		},
		int32(-1)).Store(&id)
	if err != nil {
		t.Fatalf("Notify call: %v", err)
	}
	if id != 1 {
		t.Fatalf("first Notify id = %d, want 1", id)
	}
	last := h.lastNote()
	if last == nil || last.Summary != "Subject" || last.Urgency != UrgencyCritical || last.Image == nil {
		t.Fatalf("handler received wrong notification: %+v", last)
	}
	if len(last.Actions) != 2 {
		t.Fatalf("actions decoded = %d, want 2", len(last.Actions))
	}

	// GetCapabilities + GetServerInformation over the wire.
	var caps []string
	if err := obj.Call(Interface+"."+MethodGetCapabilities, 0).Store(&caps); err != nil {
		t.Fatal(err)
	}
	if len(caps) != 5 || caps[0] != "body" {
		t.Fatalf("capabilities = %v", caps)
	}
	var name, vendor, version, spec string
	if err := obj.Call(Interface+"."+MethodGetServerInfo, 0).Store(&name, &vendor, &version, &spec); err != nil {
		t.Fatal(err)
	}
	if name != ServerName || vendor != VendorName || version != Version || spec != SpecVersion {
		t.Fatalf("server info = %q/%q/%q/%q", name, vendor, version, spec)
	}

	// CloseNotification emits NotificationClosed(id, 3=closed) and notifies the handler.
	if call := obj.Call(Interface+"."+MethodCloseNotification, 0, id); call.Err != nil {
		t.Fatalf("CloseNotification: %v", call.Err)
	}
	waitSignal(t, sigs, Interface+"."+SignalNotificationClosed, id, uint32(ReasonClosed))
	if n, r := h.closeCount(); n != 1 || r != ReasonClosed {
		t.Fatalf("OnClose not called once with ReasonClosed: n=%d r=%v", n, r)
	}

	// A server-initiated ActionInvoked reaches the client.
	if err := s.EmitActionInvoked(id, "reply"); err != nil {
		t.Fatal(err)
	}
	waitSignalKey(t, sigs, Interface+"."+SignalActionInvoked, id, "reply")

	// Second Notify allocates the next monotonic id.
	var id2 uint32
	if err := obj.Call(Interface+"."+MethodNotify, 0, "", uint32(0), "", "s2", "",
		[]string{}, map[string]dbus.Variant{}, int32(1000)).Store(&id2); err != nil {
		t.Fatal(err)
	}
	if id2 != 2 {
		t.Fatalf("second id = %d, want 2", id2)
	}
}

func TestServerAssignReplacesAndWrap(t *testing.T) {
	h := &recHandler{}
	s := &Server{handler: h}

	// ReplacesID is reused verbatim.
	if got := s.assign(&Notification{ReplacesID: 55}); got != 55 {
		t.Fatalf("replaces_id reuse = %d, want 55", got)
	}
	// The monotonic counter skips zero on wrap-around.
	s.nextID = math.MaxUint32
	n := &Notification{}
	if got := s.assign(n); got != 1 || n.ID != 1 {
		t.Fatalf("wrap-around id = %d (n.ID=%d), want 1", got, n.ID)
	}
}

func TestServerExportErrorPaths(t *testing.T) {
	server, _ := pipePair(t)
	boom := errors.New("boom")

	// exportFn failure short-circuits Export.
	s := NewServer(server, &recHandler{})
	s.exportFn = func(interface{}, dbus.ObjectPath, string) error { return boom }
	if err := s.Export(); !errors.Is(err, boom) {
		t.Fatalf("export error not propagated: %v", err)
	}

	// requestName failure propagates.
	s2 := NewServer(server, &recHandler{})
	s2.requestNameFn = func(string, dbus.RequestNameFlags) (dbus.RequestNameReply, error) {
		return 0, boom
	}
	if err := s2.Export(); !errors.Is(err, boom) {
		t.Fatalf("requestName error not propagated: %v", err)
	}

	// A non-primary-owner reply is ErrNameTaken.
	s3 := NewServer(server, &recHandler{})
	s3.requestNameFn = func(string, dbus.RequestNameFlags) (dbus.RequestNameReply, error) {
		return dbus.RequestNameReplyInQueue, nil
	}
	if err := s3.Export(); !errors.Is(err, ErrNameTaken) {
		t.Fatalf("expected ErrNameTaken, got %v", err)
	}
}

// waitSignal waits for a signal with the given name and (uint32,uint32) body.
func waitSignal(t *testing.T, ch <-chan *dbus.Signal, name string, a, b uint32) {
	t.Helper()
	for {
		select {
		case sig := <-ch:
			if sig.Name != name {
				continue
			}
			if len(sig.Body) != 2 || sig.Body[0].(uint32) != a || sig.Body[1].(uint32) != b {
				t.Fatalf("%s body = %v, want [%d %d]", name, sig.Body, a, b)
			}
			return
		case <-time.After(3 * time.Second):
			t.Fatalf("timed out waiting for %s", name)
		}
	}
}

// waitSignalKey waits for a signal with a (uint32,string) body.
func waitSignalKey(t *testing.T, ch <-chan *dbus.Signal, name string, id uint32, key string) {
	t.Helper()
	for {
		select {
		case sig := <-ch:
			if sig.Name != name {
				continue
			}
			if len(sig.Body) != 2 || sig.Body[0].(uint32) != id || sig.Body[1].(string) != key {
				t.Fatalf("%s body = %v, want [%d %q]", name, sig.Body, id, key)
			}
			return
		case <-time.After(3 * time.Second):
			t.Fatalf("timed out waiting for %s", name)
		}
	}
}
