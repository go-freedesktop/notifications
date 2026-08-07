// Copyright (c) 2026 the go-freedesktop/notifications authors. All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

package notifications

import "github.com/godbus/dbus/v5"

// imageDataKeys are the hint keys that may carry an inline image, most-recent
// spec version first. The specification renamed the key across versions:
// "icon_data" (1.0) -> "image_data" (1.1) -> "image-data" (1.2).
var imageDataKeys = []string{"image-data", "image_data", "icon_data"}

// imagePathKeys are the hint keys that may carry an on-disk image path or icon
// name: "image-path" (1.2) with the older "image_path" (1.1) accepted too.
var imagePathKeys = []string{"image-path", "image_path"}

// DecodeHints folds the Notify hint dictionary into n. Every field is decoded
// defensively: a missing key leaves the corresponding field at its zero value,
// and a value of the wrong D-Bus type is ignored rather than propagated as an
// error, so a malformed client can never break decoding. A nil map is a no-op.
func DecodeHints(n *Notification, hints map[string]dbus.Variant) {
	if hints == nil {
		return
	}
	if v, ok := byteHint(hints, "urgency"); ok {
		n.Urgency = Urgency(v)
	}
	if v, ok := stringHint(hints, "category"); ok {
		n.Category = v
	}
	if v, ok := stringHint(hints, "desktop-entry"); ok {
		n.DesktopEntry = v
	}
	if v, ok := boolHint(hints, "resident"); ok {
		n.Resident = v
	}
	if v, ok := boolHint(hints, "transient"); ok {
		n.Transient = v
	}
	for _, k := range imagePathKeys {
		if v, ok := stringHint(hints, k); ok {
			n.ImagePath = v
			break
		}
	}
	for _, k := range imageDataKeys {
		v, present := hints[k]
		if !present {
			continue
		}
		if img, err := decodeImageData(v); err == nil {
			n.Image = img
			break
		}
	}
}

// byteHint returns the byte value of hint key, and whether key was present with
// a byte value. A missing key or a non-byte value yields ok == false.
func byteHint(hints map[string]dbus.Variant, key string) (byte, bool) {
	v, ok := hints[key]
	if !ok {
		return 0, false
	}
	b, ok := v.Value().(byte)
	return b, ok
}

// stringHint returns the string value of hint key, and whether key was present
// with a string value.
func stringHint(hints map[string]dbus.Variant, key string) (string, bool) {
	v, ok := hints[key]
	if !ok {
		return "", false
	}
	s, ok := v.Value().(string)
	return s, ok
}

// boolHint returns the bool value of hint key, and whether key was present with
// a bool value.
func boolHint(hints map[string]dbus.Variant, key string) (bool, bool) {
	v, ok := hints[key]
	if !ok {
		return false, false
	}
	b, ok := v.Value().(bool)
	return b, ok
}
