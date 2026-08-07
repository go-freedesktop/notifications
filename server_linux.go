// Copyright (c) 2026 the go-freedesktop/notifications authors. All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

//go:build linux

package notifications

import (
	"errors"
	"sync"

	"github.com/go-freedesktop/dbus"
)

// ErrNameTaken is returned by Export when another process already owns the
// org.freedesktop.Notifications name (a notification daemon is already
// running).
var ErrNameTaken = errors.New("notifications: another daemon already owns " + BusName)

// Handler is the application-side callback set a Server drives. OnNotify is
// invoked for each incoming Notify (the Notification already carries its
// server-assigned, non-zero ID); it renders the notification and returns the
// id the caller should see (normally n.ID). OnClose is invoked when a
// notification is closed -- by a CloseNotification call, an expiry or a
// dismissal -- so the handler can retire the on-screen surface.
type Handler interface {
	OnNotify(n *Notification) uint32
	OnClose(id uint32, reason CloseReason)
}

// Server exports the org.freedesktop.Notifications service on a D-Bus
// connection and forwards decoded requests to a Handler. It is safe for
// concurrent use: the id allocator is mutex-guarded.
//
// The three fallible D-Bus operations the server performs -- exporting the
// method object, claiming the well-known name, and emitting a signal -- are
// indirected through function fields (exportFn, requestNameFn, emitFn) so the
// method handlers can be unit-tested as plain Go calls, with injected fakes
// covering every branch, without ever bringing up a bus or a connection.
type Server struct {
	conn    *dbus.Conn
	handler Handler

	mu     sync.Mutex
	nextID uint32

	exportFn      func(v interface{}, path dbus.ObjectPath, iface string) error
	requestNameFn func(name string, flags uint32) (uint32, error)
	emitFn        func(path dbus.ObjectPath, name string, values ...interface{}) error
}

// NewServer returns a Server that will export the notification service on conn
// and forward requests to h.
func NewServer(conn *dbus.Conn, h Handler) *Server {
	return &Server{
		conn:          conn,
		handler:       h,
		exportFn:      conn.Export,
		requestNameFn: conn.RequestName,
		emitFn:        conn.Emit,
	}
}

// Export publishes the four notification methods on conn at ObjectPath and
// claims the well-known BusName. It returns ErrNameTaken if the name is already
// owned, or the underlying error if exporting or the name request fails.
//
// The org.freedesktop.DBus.Introspectable interface is served automatically by
// the connection (it reflects the exported methods into an introspection
// document), so no introspection node is registered here.
func (s *Server) Export() error {
	if err := s.exportFn(&notifyExport{s: s}, dbus.ObjectPath(ObjectPath), Interface); err != nil {
		return err
	}
	code, err := s.requestNameFn(BusName, dbus.NameFlagDoNotQueue)
	if err != nil {
		return err
	}
	if code != dbus.NameReplyPrimaryOwner {
		return ErrNameTaken
	}
	return nil
}

// EmitClosed emits the NotificationClosed signal for id with the given reason.
func (s *Server) EmitClosed(id uint32, reason CloseReason) error {
	return s.emitFn(dbus.ObjectPath(ObjectPath), Interface+"."+SignalNotificationClosed,
		id, uint32(reason))
}

// EmitActionInvoked emits the ActionInvoked signal binding action key to id.
func (s *Server) EmitActionInvoked(id uint32, key string) error {
	return s.emitFn(dbus.ObjectPath(ObjectPath), Interface+"."+SignalActionInvoked,
		id, key)
}

// assign gives n its id -- reusing ReplacesID when non-zero, otherwise the
// next monotonic, non-zero counter value (wrapping past uint32 max back to 1) --
// then dispatches it to the handler and returns the handler's reported id.
func (s *Server) assign(n *Notification) uint32 {
	s.mu.Lock()
	if n.ReplacesID != 0 {
		n.ID = n.ReplacesID
	} else {
		s.nextID++
		if s.nextID == 0 {
			s.nextID = 1
		}
		n.ID = s.nextID
	}
	s.mu.Unlock()
	return s.handler.OnNotify(n)
}

// notifyExport is the receiver the connection dispatches the interface's method
// calls to. Keeping the wire methods on a dedicated type (rather than on Server
// directly) means only the four spec methods are exported over D-Bus.
type notifyExport struct{ s *Server }

// Notify implements org.freedesktop.Notifications.Notify
// (susssasa{sv}i -> u).
func (x *notifyExport) Notify(appName string, replacesID uint32, appIcon, summary, body string,
	actions []string, hints map[string]dbus.Variant, expireTimeout int32) (uint32, *dbus.Error) {
	n := Decode(appName, replacesID, appIcon, summary, body, actions, hints, expireTimeout)
	return x.s.assign(n), nil
}

// CloseNotification implements org.freedesktop.Notifications.CloseNotification:
// it retires the notification through the handler and emits NotificationClosed
// with the "closed by call" reason.
func (x *notifyExport) CloseNotification(id uint32) *dbus.Error {
	x.s.handler.OnClose(id, ReasonClosed)
	_ = x.s.EmitClosed(id, ReasonClosed)
	return nil
}

// GetCapabilities implements org.freedesktop.Notifications.GetCapabilities.
func (x *notifyExport) GetCapabilities() ([]string, *dbus.Error) {
	return Capabilities(), nil
}

// GetServerInformation implements
// org.freedesktop.Notifications.GetServerInformation, returning
// (name, vendor, version, spec_version).
func (x *notifyExport) GetServerInformation() (string, string, string, string, *dbus.Error) {
	return ServerName, VendorName, Version, SpecVersion, nil
}
