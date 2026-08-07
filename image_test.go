// Copyright (c) 2026 the go-freedesktop/notifications authors. All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

package notifications

import (
	"errors"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/go-freedesktop/dbus"
)

// imageDataVariant builds a variant carrying the (iiibiiay) image-data payload
// exactly as the D-Bus codec decodes it off the wire: a []interface{} of the
// seven positional fields.
func imageDataVariant(w, h, rowstride int32, hasAlpha bool, bps, channels int32, data []byte) dbus.Variant {
	fields := []interface{}{w, h, rowstride, hasAlpha, bps, channels, data}
	return dbus.MakeVariantWithSignature(fields, dbus.ParseSignatureMust("(iiibiiay)"))
}

func TestDecodeImageData3Channel(t *testing.T) {
	// 2x2 RGB, rowstride == width*3 (no padding).
	data := []byte{
		10, 11, 12, 20, 21, 22,
		30, 31, 32, 40, 41, 42,
	}
	img, err := decodeImageData(imageDataVariant(2, 2, 6, false, 8, 3, data))
	if err != nil {
		t.Fatal(err)
	}
	if got := img.RGBAAt(1, 0); got != (color.RGBA{20, 21, 22, 0xFF}) {
		t.Fatalf("pixel (1,0) = %+v, want opaque 20,21,22", got)
	}
	if got := img.RGBAAt(0, 1); got != (color.RGBA{30, 31, 32, 0xFF}) {
		t.Fatalf("pixel (0,1) = %+v, want opaque 30,31,32", got)
	}
}

func TestDecodeImageData4ChannelWithRowstridePadding(t *testing.T) {
	// 1x2 RGBA, rowstride 8 = 4 bytes payload + 4 bytes padding per row.
	data := []byte{
		1, 2, 3, 4, 0, 0, 0, 0,
		5, 6, 7, 8, 0, 0, 0, 0,
	}
	img, err := decodeImageData(imageDataVariant(1, 2, 8, true, 8, 4, data))
	if err != nil {
		t.Fatal(err)
	}
	if got := img.RGBAAt(0, 0); got != (color.RGBA{1, 2, 3, 4}) {
		t.Fatalf("pixel (0,0) = %+v, want 1,2,3,4", got)
	}
	if got := img.RGBAAt(0, 1); got != (color.RGBA{5, 6, 7, 8}) {
		t.Fatalf("pixel (0,1) = %+v (row past padding) want 5,6,7,8", got)
	}
}

func TestDecodeImageDataErrors(t *testing.T) {
	ok := []byte{1, 2, 3, 4, 5, 6} // 2x1 RGB
	cases := []struct {
		name string
		v    dbus.Variant
	}{
		{"not a struct", dbus.MakeVariant("scalar")},
		{"wrong field count", dbus.MakeVariantWithSignature([]interface{}{int32(1)}, dbus.ParseSignatureMust("(i)"))},
		{"field wrong type", dbus.MakeVariantWithSignature(
			[]interface{}{"w", int32(1), int32(3), false, int32(8), int32(3), ok},
			dbus.ParseSignatureMust("(siibiiay)"))},
		{"zero width", imageDataVariant(0, 1, 3, false, 8, 3, ok)},
		{"zero height", imageDataVariant(2, 0, 6, false, 8, 3, ok)},
		{"bad bits per sample", imageDataVariant(2, 1, 6, false, 16, 3, ok)},
		{"bad channel count", imageDataVariant(2, 1, 6, false, 8, 2, ok)},
		{"rowstride too small", imageDataVariant(2, 1, 5, false, 8, 3, ok)},
		{"truncated payload", imageDataVariant(2, 1, 6, false, 8, 3, []byte{1, 2, 3})},
	}
	for _, c := range cases {
		if _, err := decodeImageData(c.v); !errors.Is(err, ErrBadImageData) {
			t.Errorf("%s: err = %v, want ErrBadImageData", c.name, err)
		}
	}
}

func TestLoadImagePath(t *testing.T) {
	dir := t.TempDir()

	// A real PNG round-trips to an *image.RGBA of the right size.
	pngPath := filepath.Join(dir, "icon.png")
	src := image.NewRGBA(image.Rect(0, 0, 3, 2))
	src.Set(0, 0, color.RGBA{9, 8, 7, 255})
	f, err := os.Create(pngPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(f, src); err != nil {
		t.Fatal(err)
	}
	f.Close()

	img, err := LoadImagePath("file://" + pngPath) // exercises the file:// strip
	if err != nil {
		t.Fatal(err)
	}
	if img.Bounds().Dx() != 3 || img.Bounds().Dy() != 2 {
		t.Fatalf("decoded size = %v, want 3x2", img.Bounds())
	}

	// An SVG path is reported as a vector icon, not rasterised.
	if _, err := LoadImagePath("/some/where/logo.svg"); !errors.Is(err, ErrVectorIcon) {
		t.Errorf("svg: err = %v, want ErrVectorIcon", err)
	}

	// A missing file surfaces the open error.
	if _, err := LoadImagePath(filepath.Join(dir, "nope.png")); err == nil {
		t.Error("missing file: expected an error")
	}

	// A file with non-image contents surfaces a decode error.
	bad := filepath.Join(dir, "bad.png")
	if err := os.WriteFile(bad, []byte("not a png"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadImagePath(bad); err == nil {
		t.Error("garbage file: expected a decode error")
	}
}
