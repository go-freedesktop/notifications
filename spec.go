// Copyright (c) 2026 the go-freedesktop/notifications authors. All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

// Package notifications is a pure-Go (CGO_ENABLED=0) implementation of the
// freedesktop.org Desktop Notifications Specification -- the D-Bus service
// org.freedesktop.Notifications that desktop applications call (via
// notify-send, libnotify, GLib.Notification, ...) to post transient
// notification bubbles.
//
// The package is split into a platform-neutral core (this file plus
// notification.go, hints.go and image.go) that models a Notification and
// decodes the wire-level hint dictionary and image payloads, a Linux D-Bus
// server (server_linux.go) that exports the four spec methods and emits the
// two spec signals over github.com/godbus/dbus/v5, and a pure bridge
// (./toast) that turns a decoded Notification into a github.com/go-widgets
// Toast widget so a go-widgets desktop can act as the notification daemon.
//
// Specification: https://specifications.freedesktop.org/notification-spec/latest/
package notifications

// Well-known D-Bus name, object path and interface of the notification
// service, as mandated by the specification.
const (
	// BusName is the well-known bus name a notification daemon owns.
	BusName = "org.freedesktop.Notifications"
	// ObjectPath is the object path the service is exported at.
	ObjectPath = "/org/freedesktop/Notifications"
	// Interface is the D-Bus interface the four methods and two signals
	// belong to.
	Interface = "org.freedesktop.Notifications"
)

// The four method member names of the interface.
const (
	MethodNotify             = "Notify"
	MethodCloseNotification  = "CloseNotification"
	MethodGetCapabilities    = "GetCapabilities"
	MethodGetServerInfo      = "GetServerInformation"
	SignalNotificationClosed = "NotificationClosed"
	SignalActionInvoked      = "ActionInvoked"
)

// Server-information values reported by GetServerInformation. SpecVersion is
// the version of the notification specification the server implements.
const (
	ServerName  = "go-widgets-notifyd"
	VendorName  = "go-freedesktop"
	Version     = "0.1.0"
	SpecVersion = "1.2"
)

// Capabilities returns the notification capabilities this implementation
// advertises through GetCapabilities. The list is deliberately TRUTHFUL: it
// names only what the ./toast bridge actually renders --
//
//   - "body"        : the notification carries a body text distinct from the
//     summary (rendered as a second Toast line);
//   - "actions"     : action buttons are rendered and ActionInvoked is emitted
//     when one is clicked;
//   - "body-markup"  : the body may contain the small hypertext-subset markup
//     the spec defines (the bridge strips the tags to plain text);
//   - "icon-static" : a static (non-animated) icon or image is rendered beside
//     the text;
//   - "persistence" : a notification with an infinite / resident lifetime stays
//     on screen until dismissed (a sticky Toast).
//
// Capabilities we do NOT render (sound, action-icons, icon-multi, ...) are
// omitted so a client is never misled about what the daemon can do.
func Capabilities() []string {
	return []string{"body", "actions", "body-markup", "icon-static", "persistence"}
}
