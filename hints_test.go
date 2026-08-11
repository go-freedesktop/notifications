// Copyright (c) 2026 the go-freedesktop/notifications authors. All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

package notifications

import (
	"testing"

	"github.com/godbus/dbus/v5"
)

func TestDecodeHintsNilMap(t *testing.T) {
	n := &Notification{Urgency: UrgencyNormal}
	DecodeHints(n, nil) // must not panic and leave fields untouched
	if n.Urgency != UrgencyNormal || n.Category != "" || n.Image != nil {
		t.Fatalf("nil hints mutated the notification: %+v", n)
	}
}

func TestDecodeHintsWrongTypesIgnored(t *testing.T) {
	// Every hint carries the WRONG variant type; each must be ignored,
	// leaving the field at its zero/default value.
	n := &Notification{Urgency: UrgencyNormal}
	DecodeHints(n, map[string]dbus.Variant{
		"urgency":       dbus.MakeVariant("not-a-byte"),
		"category":      dbus.MakeVariant(int32(5)),
		"desktop-entry": dbus.MakeVariant(true),
		"resident":      dbus.MakeVariant("nope"),
		"transient":     dbus.MakeVariant(int32(1)),
		"image-path":    dbus.MakeVariant(byte(3)),
	})
	if n.Urgency != UrgencyNormal || n.Category != "" || n.DesktopEntry != "" ||
		n.Resident || n.Transient || n.ImagePath != "" {
		t.Fatalf("wrong-typed hints leaked into the notification: %+v", n)
	}
}

func TestDecodeHintsTransientAndLegacyImagePath(t *testing.T) {
	n := &Notification{}
	DecodeHints(n, map[string]dbus.Variant{
		"transient":  dbus.MakeVariant(true),
		"image_path": dbus.MakeVariant("/legacy/path.png"), // 1.1 spelling
	})
	if !n.Transient {
		t.Error("transient hint not decoded")
	}
	if n.ImagePath != "/legacy/path.png" {
		t.Errorf("image_path (legacy) = %q, want /legacy/path.png", n.ImagePath)
	}
}

func TestDecodeHintsInlineImageKeys(t *testing.T) {
	good := imageDataVariant(2, 1, 6, false, 8, 3, []byte{1, 2, 3, 4, 5, 6})
	// image-data (1.2) wins even when a malformed icon_data is also present.
	n := &Notification{}
	DecodeHints(n, map[string]dbus.Variant{
		"image-data": good,
		"icon_data":  dbus.MakeVariant("garbage"),
	})
	if n.Image == nil || n.Image.Bounds().Dx() != 2 {
		t.Fatalf("image-data not decoded: %+v", n.Image)
	}

	// A malformed image-data falls through to a valid legacy icon_data.
	n2 := &Notification{}
	DecodeHints(n2, map[string]dbus.Variant{
		"image-data": dbus.MakeVariant("garbage"),
		"icon_data":  good,
	})
	if n2.Image == nil || n2.Image.Bounds().Dx() != 2 {
		t.Fatalf("fell through to icon_data incorrectly: %+v", n2.Image)
	}

	// All image keys malformed -> no image, no panic.
	n3 := &Notification{}
	DecodeHints(n3, map[string]dbus.Variant{"image_data": dbus.MakeVariant(int32(0))})
	if n3.Image != nil {
		t.Fatalf("malformed image_data produced an image: %+v", n3.Image)
	}
}

func TestHintHelpersMissingKeys(t *testing.T) {
	empty := map[string]dbus.Variant{}
	if _, ok := byteHint(empty, "x"); ok {
		t.Error("byteHint on missing key returned ok")
	}
	if _, ok := stringHint(empty, "x"); ok {
		t.Error("stringHint on missing key returned ok")
	}
	if _, ok := boolHint(empty, "x"); ok {
		t.Error("boolHint on missing key returned ok")
	}
}
