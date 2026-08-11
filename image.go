// Copyright (c) 2026 the go-freedesktop/notifications authors. All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

package notifications

import (
	"errors"
	"image"
	"image/draw"
	_ "image/gif"  // register GIF decoder for LoadImagePath
	_ "image/jpeg" // register JPEG decoder for LoadImagePath
	_ "image/png"  // register PNG decoder for LoadImagePath
	"os"
	"strings"

	"github.com/godbus/dbus/v5"
)

// Errors returned by the image decoders.
var (
	// ErrBadImageData is returned by decodeImageData when the inline
	// image-data hint is not a well-formed (iiibiiay) payload.
	ErrBadImageData = errors.New("notifications: malformed image-data hint")
	// ErrVectorIcon is returned by LoadImagePath for a scalable (SVG) path:
	// this pure-raster decoder does not rasterise vector art, so the caller
	// should fall back to an icon-theme raster lookup or drop the image.
	ErrVectorIcon = errors.New("notifications: vector (svg) icon path not rasterised")
)

// decodeImageData converts a freedesktop "image-data" hint -- the D-Bus struct
// (iiibiiay) of width, height, rowstride, has_alpha, bits_per_sample, channels
// and the raw pixel bytes -- into a straight-alpha *image.RGBA.
//
// Only the spec's canonical 8-bits-per-sample, 3-channel (RGB) or 4-channel
// (RGBA) layouts are accepted; every other shape, and any payload too short
// for the declared geometry, is rejected with ErrBadImageData so a malformed
// client can never drive an out-of-bounds read.
func decodeImageData(v dbus.Variant) (*image.RGBA, error) {
	fields, ok := v.Value().([]interface{})
	if !ok || len(fields) != 7 {
		return nil, ErrBadImageData
	}
	width, ok0 := fields[0].(int32)
	height, ok1 := fields[1].(int32)
	rowstride, ok2 := fields[2].(int32)
	_, ok3 := fields[3].(bool) // has_alpha: advisory; channels is authoritative
	bitsPerSample, ok4 := fields[4].(int32)
	channels, ok5 := fields[5].(int32)
	data, ok6 := fields[6].([]byte)
	if !(ok0 && ok1 && ok2 && ok3 && ok4 && ok5 && ok6) {
		return nil, ErrBadImageData
	}
	if width <= 0 || height <= 0 {
		return nil, ErrBadImageData
	}
	if bitsPerSample != 8 {
		return nil, ErrBadImageData
	}
	if channels != 3 && channels != 4 {
		return nil, ErrBadImageData
	}
	if rowstride < width*channels {
		return nil, ErrBadImageData
	}
	// The last row need only hold width*channels bytes, earlier rows a full
	// rowstride; reject anything shorter (a truncated payload).
	need := int(rowstride)*int(height-1) + int(width*channels)
	if len(data) < need {
		return nil, ErrBadImageData
	}

	img := image.NewRGBA(image.Rect(0, 0, int(width), int(height)))
	for y := 0; y < int(height); y++ {
		src := y * int(rowstride)
		dst := y * img.Stride
		for x := 0; x < int(width); x++ {
			s := src + x*int(channels)
			d := dst + x*4
			img.Pix[d+0] = data[s+0]
			img.Pix[d+1] = data[s+1]
			img.Pix[d+2] = data[s+2]
			if channels == 4 {
				img.Pix[d+3] = data[s+3]
			} else {
				img.Pix[d+3] = 0xFF
			}
		}
	}
	return img, nil
}

// LoadImagePath decodes an on-disk image referenced by a notification
// "image-path" hint (or a resolved app_icon path) into an *image.RGBA. A
// "file://" URI prefix is accepted and stripped. Raster formats (PNG, JPEG,
// GIF) are decoded through the standard library; a ".svg" path returns
// ErrVectorIcon because this decoder is deliberately pure-raster and drags in
// no SVG rasteriser. A missing or undecodable file returns the underlying
// error.
func LoadImagePath(path string) (*image.RGBA, error) {
	path = strings.TrimPrefix(path, "file://")
	if strings.HasSuffix(strings.ToLower(path), ".svg") {
		return nil, ErrVectorIcon
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	if err != nil {
		return nil, err
	}
	b := img.Bounds()
	out := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(out, out.Bounds(), img, b.Min, draw.Src)
	return out, nil
}
