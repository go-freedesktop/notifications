// Copyright (c) 2026 the go-freedesktop/notifications authors. All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

package notifications

import (
	"image"

	"github.com/go-freedesktop/dbus"
)

// Urgency is the freedesktop "urgency" hint: a byte ranking how much the
// notification demands attention. The spec defines exactly three levels.
type Urgency byte

const (
	// UrgencyLow is background information the user need not act on.
	UrgencyLow Urgency = 0
	// UrgencyNormal is the default level for ordinary notifications.
	UrgencyNormal Urgency = 1
	// UrgencyCritical must stay visible until the user acts (it maps to a
	// resident, error-coloured Toast).
	UrgencyCritical Urgency = 2
)

// String names the urgency level for diagnostics.
func (u Urgency) String() string {
	switch u {
	case UrgencyLow:
		return "low"
	case UrgencyCritical:
		return "critical"
	default:
		return "normal"
	}
}

// CloseReason is the reason code carried by the NotificationClosed signal, as
// enumerated by the specification.
type CloseReason uint32

const (
	// ReasonExpired: the notification's timeout elapsed.
	ReasonExpired CloseReason = 1
	// ReasonDismissed: the user dismissed the notification.
	ReasonDismissed CloseReason = 2
	// ReasonClosed: the notification was closed by a CloseNotification call.
	ReasonClosed CloseReason = 3
	// ReasonUndefined: closed for some other/undefined reason.
	ReasonUndefined CloseReason = 4
)

// String names the close reason for diagnostics.
func (r CloseReason) String() string {
	switch r {
	case ReasonExpired:
		return "expired"
	case ReasonDismissed:
		return "dismissed"
	case ReasonClosed:
		return "closed"
	default:
		return "undefined"
	}
}

// Action is one entry of the Notify "actions" array: a machine Key the client
// keys ActionInvoked on, plus the human-readable Label rendered on the button.
// The reserved key "default" marks the action taken on a plain activation
// (a click on the notification body rather than a specific button).
type Action struct {
	Key   string
	Label string
}

// IsDefault reports whether this is the reserved "default" action.
func (a Action) IsDefault() bool { return a.Key == "default" }

// Notification is a decoded org.freedesktop.Notifications.Notify request: the
// spec's positional arguments plus everything unpacked from its hint
// dictionary. It is the platform-neutral value the server hands to a Handler
// and the ./toast bridge turns into a Toast.
type Notification struct {
	// ID is the notification id assigned by the server (0 until assigned).
	ID uint32
	// AppName is the optional application name (first Notify argument).
	AppName string
	// ReplacesID is the id of a notification this one replaces (0 = none).
	ReplacesID uint32
	// AppIcon is the app_icon argument: an icon name (to resolve through an
	// icon theme) or a file/URI path.
	AppIcon string
	// Summary is the single-line title (required by the spec).
	Summary string
	// Body is the optional multi-line body; it may carry the spec's
	// hypertext-subset markup.
	Body string
	// Actions are the parsed action pairs, in order.
	Actions []Action
	// ExpireMS is the requested timeout in milliseconds: -1 = server
	// default, 0 = never expire (persist until dismissed / closed).
	ExpireMS int32

	// Urgency is the decoded "urgency" hint (defaults to UrgencyNormal).
	Urgency Urgency
	// Category is the decoded "category" hint (may be empty).
	Category string
	// DesktopEntry is the decoded "desktop-entry" hint (may be empty).
	DesktopEntry string
	// Resident is the decoded "resident" hint: the notification is not
	// removed after an action is invoked.
	Resident bool
	// Transient is the decoded "transient" hint: bypass any persistence.
	Transient bool

	// Image is the inline image supplied through an "image-data" /
	// "image_data" / "icon_data" hint (nil when none was supplied).
	Image *image.RGBA
	// ImagePath is the decoded "image-path" / "image_path" hint: a path or
	// icon name pointing at an image on disk (may be empty).
	ImagePath string
}

// Sticky reports whether the notification should persist on screen until the
// user acts, rather than auto-expiring: an explicit zero timeout, a resident
// hint, or critical urgency all make it sticky (unless it is transient).
func (n *Notification) Sticky() bool {
	if n.Transient {
		return false
	}
	return n.ExpireMS == 0 || n.Resident || n.Urgency == UrgencyCritical
}

// parseActions turns the flat Notify actions array -- alternating
// key,label,key,label,... -- into Action pairs. A trailing key with no label
// (an odd-length array) is dropped rather than treated as an error, matching
// the leniency real daemons show malformed clients. A nil/empty array yields
// no actions.
func parseActions(flat []string) []Action {
	if len(flat) < 2 {
		return nil
	}
	out := make([]Action, 0, len(flat)/2)
	for i := 0; i+1 < len(flat); i += 2 {
		out = append(out, Action{Key: flat[i], Label: flat[i+1]})
	}
	return out
}

// Decode assembles a Notification from the raw positional arguments of a
// Notify call and its hint dictionary. It never fails: malformed hints are
// ignored field by field (see DecodeHints) so a hostile or buggy client can
// never break the daemon.
func Decode(appName string, replacesID uint32, appIcon, summary, body string,
	actions []string, hints map[string]dbus.Variant, expireMS int32) *Notification {
	n := &Notification{
		AppName:    appName,
		ReplacesID: replacesID,
		AppIcon:    appIcon,
		Summary:    summary,
		Body:       body,
		Actions:    parseActions(actions),
		ExpireMS:   expireMS,
		Urgency:    UrgencyNormal,
	}
	DecodeHints(n, hints)
	return n
}
