// Copyright (c) 2026 the go-freedesktop/notifications authors. All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

package notifications

import (
	"reflect"
	"testing"

	"github.com/godbus/dbus/v5"
)

func TestCapabilities(t *testing.T) {
	got := Capabilities()
	want := []string{"body", "actions", "body-markup", "icon-static", "persistence"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Capabilities() = %v, want %v", got, want)
	}
}

func TestUrgencyString(t *testing.T) {
	cases := map[Urgency]string{
		UrgencyLow:      "low",
		UrgencyNormal:   "normal",
		UrgencyCritical: "critical",
		Urgency(99):     "normal", // out of range -> default
	}
	for u, want := range cases {
		if got := u.String(); got != want {
			t.Errorf("Urgency(%d).String() = %q, want %q", u, got, want)
		}
	}
}

func TestCloseReasonString(t *testing.T) {
	cases := map[CloseReason]string{
		ReasonExpired:   "expired",
		ReasonDismissed: "dismissed",
		ReasonClosed:    "closed",
		ReasonUndefined: "undefined",
		CloseReason(42): "undefined", // out of range -> default
	}
	for r, want := range cases {
		if got := r.String(); got != want {
			t.Errorf("CloseReason(%d).String() = %q, want %q", r, got, want)
		}
	}
}

func TestActionIsDefault(t *testing.T) {
	if !(Action{Key: "default"}).IsDefault() {
		t.Error(`Action{Key:"default"}.IsDefault() = false, want true`)
	}
	if (Action{Key: "open"}).IsDefault() {
		t.Error(`Action{Key:"open"}.IsDefault() = true, want false`)
	}
}

func TestNotificationSticky(t *testing.T) {
	cases := []struct {
		name string
		n    Notification
		want bool
	}{
		{"transient overrides all", Notification{Transient: true, ExpireMS: 0, Urgency: UrgencyCritical, Resident: true}, false},
		{"zero expire is sticky", Notification{ExpireMS: 0}, true},
		{"resident is sticky", Notification{ExpireMS: 3000, Resident: true}, true},
		{"critical is sticky", Notification{ExpireMS: 3000, Urgency: UrgencyCritical}, true},
		{"ordinary is not sticky", Notification{ExpireMS: 3000, Urgency: UrgencyNormal}, false},
		{"server-default (-1) is not sticky", Notification{ExpireMS: -1, Urgency: UrgencyNormal}, false},
	}
	for _, c := range cases {
		if got := c.n.Sticky(); got != c.want {
			t.Errorf("%s: Sticky() = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestParseActions(t *testing.T) {
	if got := parseActions(nil); got != nil {
		t.Errorf("parseActions(nil) = %v, want nil", got)
	}
	if got := parseActions([]string{"only"}); got != nil {
		t.Errorf("parseActions(len 1) = %v, want nil", got)
	}
	got := parseActions([]string{"default", "Open", "snooze", "Snooze", "orphan"})
	want := []Action{{"default", "Open"}, {"snooze", "Snooze"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseActions with odd trailing = %v, want %v (orphan dropped)", got, want)
	}
}

func TestDecode(t *testing.T) {
	hints := map[string]dbus.Variant{
		"urgency":       dbus.MakeVariant(byte(UrgencyCritical)),
		"category":      dbus.MakeVariant("email.arrived"),
		"desktop-entry": dbus.MakeVariant("org.example.Mail"),
		"resident":      dbus.MakeVariant(true),
		"image-path":    dbus.MakeVariant("/usr/share/icons/mail.png"),
	}
	n := Decode("Mail", 7, "mail-icon", "New message", "Hello <b>there</b>",
		[]string{"default", "Open"}, hints, 4000)

	if n.AppName != "Mail" || n.ReplacesID != 7 || n.AppIcon != "mail-icon" {
		t.Errorf("positional fields wrong: %+v", n)
	}
	if n.Summary != "New message" || n.Body != "Hello <b>there</b>" || n.ExpireMS != 4000 {
		t.Errorf("summary/body/expire wrong: %+v", n)
	}
	if len(n.Actions) != 1 || n.Actions[0] != (Action{"default", "Open"}) {
		t.Errorf("actions wrong: %+v", n.Actions)
	}
	if n.Urgency != UrgencyCritical || n.Category != "email.arrived" ||
		n.DesktopEntry != "org.example.Mail" || !n.Resident || n.ImagePath != "/usr/share/icons/mail.png" {
		t.Errorf("decoded hints wrong: %+v", n)
	}
}
