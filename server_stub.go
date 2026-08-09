// Copyright (c) 2026 the go-freedesktop/notifications authors. All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

//go:build !linux

package notifications

import (
	"errors"

	"github.com/go-freedesktop/dbus"
)

// ErrUnsupported is returned by every Server operation on a non-Linux build.
// The freedesktop notification service is a Linux desktop-bus service; this
// stub exists only so the package (and portable callers) cross-compile cleanly
// for the other desktop targets, GOOS=darwin and windows.
var ErrUnsupported = errors.New("notifications: D-Bus server is only supported on GOOS=linux")

// Handler mirrors the Linux Handler interface so portable code compiles on
// every platform. See the Linux build for the operative documentation.
type Handler interface {
	OnNotify(n *Notification) uint32
	OnClose(id uint32, reason CloseReason)
}

// Server is the non-Linux stand-in for the Linux D-Bus server. Every method is
// a no-op returning ErrUnsupported.
type Server struct{}

// NewServer returns a stub Server on non-Linux platforms.
func NewServer(conn *dbus.Conn, h Handler) *Server { return &Server{} }

// Export reports ErrUnsupported: there is no session bus service to export to.
func (s *Server) Export() error { return ErrUnsupported }

// ExportReplace reports ErrUnsupported (there is no session bus to claim).
func (s *Server) ExportReplace() error { return ErrUnsupported }

// EmitClosed reports ErrUnsupported.
func (s *Server) EmitClosed(id uint32, reason CloseReason) error { return ErrUnsupported }

// EmitActionInvoked reports ErrUnsupported.
func (s *Server) EmitActionInvoked(id uint32, key string) error { return ErrUnsupported }
