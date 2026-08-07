// Copyright (c) 2026 the go-freedesktop/notifications authors. All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

package toast

import (
	"image"
	"image/color"
	"reflect"
	"testing"

	"github.com/go-freedesktop/notifications"
	"github.com/go-widgets/toolkit"
)

func TestLifeFor(t *testing.T) {
	cases := []struct {
		name string
		n    notifications.Notification
		want int
	}{
		{"sticky (zero expire)", notifications.Notification{ExpireMS: 0}, 0},
		{"critical is sticky", notifications.Notification{ExpireMS: 3000, Urgency: notifications.UrgencyCritical}, 0},
		{"server default (-1)", notifications.Notification{ExpireMS: -1, Urgency: notifications.UrgencyNormal}, DefaultExpireMS / TickMS},
		{"explicit 5s", notifications.Notification{ExpireMS: 5000, Urgency: notifications.UrgencyNormal}, 50},
		{"sub-tick rounds up to 1", notifications.Notification{ExpireMS: 40, Urgency: notifications.UrgencyNormal}, 1},
	}
	for _, c := range cases {
		if got := LifeFor(&c.n); got != c.want {
			t.Errorf("%s: LifeFor = %d, want %d", c.name, got, c.want)
		}
	}
}

func TestKindFor(t *testing.T) {
	if got := KindFor(&notifications.Notification{Urgency: notifications.UrgencyCritical}); got != toolkit.ToastError {
		t.Errorf("critical -> %d, want ToastError", got)
	}
	if got := KindFor(&notifications.Notification{Urgency: notifications.UrgencyLow}); got != toolkit.ToastInfo {
		t.Errorf("low -> %d, want ToastInfo", got)
	}
}

func TestToToastLinesAndMarkup(t *testing.T) {
	n := &notifications.Notification{
		Summary:  "Build <b>done</b>",
		Body:     "line1 &amp; more\nline2 <i>x</i>",
		ExpireMS: 2000,
		Urgency:  notifications.UrgencyNormal,
	}
	tt := ToToast(n, toolkit.DefaultLight(), nil, nil)
	want := []string{"Build done", "line1 & more", "line2 x"}
	if !reflect.DeepEqual(tt.Lines, want) {
		t.Fatalf("Lines = %q, want %q", tt.Lines, want)
	}
	if !tt.Visible {
		t.Error("toast should be visible")
	}
	if tt.Life != 20 {
		t.Errorf("Life = %d, want 20", tt.Life)
	}
}

func TestToToastSummaryOnly(t *testing.T) {
	tt := ToToast(&notifications.Notification{Summary: "hi"}, toolkit.DefaultLight(), nil, nil)
	if !reflect.DeepEqual(tt.Lines, []string{"hi"}) {
		t.Fatalf("Lines = %q, want [hi]", tt.Lines)
	}
}

func TestToToastActionsSkipDefault(t *testing.T) {
	n := &notifications.Notification{
		ID:      77,
		Summary: "s",
		Actions: []notifications.Action{
			{Key: "default", Label: "Activate"},
			{Key: "reply", Label: "Reply"},
			{Key: "later", Label: "Later"},
		},
	}
	tt := ToToast(n, toolkit.DefaultLight(), nil, nil)
	if len(tt.Actions) != 2 {
		t.Fatalf("got %d action buttons, want 2 (default filtered)", len(tt.Actions))
	}
	if tt.Actions[0].Label != "Reply" || tt.Actions[1].Label != "Later" {
		t.Fatalf("action labels = %q/%q", tt.Actions[0].Label, tt.Actions[1].Label)
	}
}

// TestToToastClickRoutesActionInvoked drives a synthetic click through the
// laid-out ButtonRects and asserts the right (id,key) is emitted -- and that a
// click outside every button emits nothing.
func TestToToastClickRoutesActionInvoked(t *testing.T) {
	n := &notifications.Notification{
		ID:      42,
		Summary: "Message",
		Actions: []notifications.Action{
			{Key: "reply", Label: "Reply"},
			{Key: "archive", Label: "Archive"},
		},
	}
	var gotID uint32
	var gotKey string
	var calls int
	emit := func(id uint32, key string) { gotID, gotKey, calls = id, key, calls+1 }

	tt := ToToast(n, toolkit.DefaultLight(), nil, emit)
	tt.AnchorIn(toolkit.Rect{X: 0, Y: 0, W: 400, H: 300}, toolkit.BottomRight, 0)

	rects := tt.ButtonRects()
	if len(rects) != 2 {
		t.Fatalf("ButtonRects len = %d, want 2", len(rects))
	}
	// Click dead-centre of the second button ("archive").
	r := rects[1]
	tt.OnEvent(toolkit.Event{Kind: toolkit.EventClick, X: r.X + r.W/2, Y: r.Y + r.H/2})
	if calls != 1 || gotID != 42 || gotKey != "archive" {
		t.Fatalf("click on button[1]: calls=%d id=%d key=%q, want 1/42/archive", calls, gotID, gotKey)
	}

	// A click entirely outside the pill emits nothing more.
	tt.Visible = true
	tt.OnEvent(toolkit.Event{Kind: toolkit.EventClick, X: -100, Y: -100})
	if calls != 1 {
		t.Fatalf("out-of-bounds click emitted: calls=%d", calls)
	}
}

// TestToToastNilEmitterInert proves an action Callback with a nil emitter is a
// safe no-op (it still dismisses the toast).
func TestToToastNilEmitterInert(t *testing.T) {
	n := &notifications.Notification{ID: 1, Summary: "s", Actions: []notifications.Action{{Key: "k", Label: "L"}}}
	tt := ToToast(n, toolkit.DefaultLight(), nil, nil)
	tt.AnchorIn(toolkit.Rect{X: 0, Y: 0, W: 300, H: 200}, toolkit.TopRight, 0)
	r := tt.ButtonRects()[0]
	tt.OnEvent(toolkit.Event{Kind: toolkit.EventClick, X: r.X + 1, Y: r.Y + 1})
	if tt.Visible {
		t.Error("clicking an action should dismiss the toast even with a nil emitter")
	}
}

func TestResolveIconPreference(t *testing.T) {
	pix := []byte{1, 2, 3, 4}

	// 1. Inline image wins.
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	img.Set(0, 0, color.RGBA{5, 6, 7, 8})
	tt := ToToast(&notifications.Notification{Summary: "s", Image: img, AppIcon: "ignored"},
		toolkit.DefaultLight(), func(string) ([]byte, int, int, bool) { return nil, 0, 0, false }, nil)
	if tt.IW != 1 || tt.IH != 1 || len(tt.Pixels) != 4 {
		t.Fatalf("inline image not used: IW=%d IH=%d len=%d", tt.IW, tt.IH, len(tt.Pixels))
	}

	// 2. image-path resolved through icons.
	look := func(name string) ([]byte, int, int, bool) {
		if name == "/p.png" {
			return pix, 1, 1, true
		}
		return nil, 0, 0, false
	}
	tt = ToToast(&notifications.Notification{Summary: "s", ImagePath: "/p.png"}, toolkit.DefaultLight(), look, nil)
	if tt.IW != 1 || len(tt.Pixels) != 4 {
		t.Fatal("image-path lookup not used")
	}

	// 3. image-path misses -> fall back to app_icon.
	look2 := func(name string) ([]byte, int, int, bool) {
		if name == "app" {
			return pix, 1, 1, true
		}
		return nil, 0, 0, false
	}
	tt = ToToast(&notifications.Notification{Summary: "s", ImagePath: "/missing", AppIcon: "app"},
		toolkit.DefaultLight(), look2, nil)
	if tt.IW != 1 {
		t.Fatal("app_icon fallback not used")
	}

	// 4. nil icons + a name -> no icon.
	tt = ToToast(&notifications.Notification{Summary: "s", AppIcon: "app"}, toolkit.DefaultLight(), nil, nil)
	if tt.Pixels != nil {
		t.Fatal("nil IconLookup should yield no pixels")
	}

	// 5. icons present but nothing resolves -> no icon.
	tt = ToToast(&notifications.Notification{Summary: "s", AppIcon: "app"}, toolkit.DefaultLight(),
		func(string) ([]byte, int, int, bool) { return nil, 0, 0, false }, nil)
	if tt.Pixels != nil {
		t.Fatal("unresolved icon should yield no pixels")
	}
}

// TestRgbaPixelsHonoursOriginAndStride covers the row-copy math for an image
// whose bounds do not start at the origin.
func TestRgbaPixelsHonoursOriginAndStride(t *testing.T) {
	img := image.NewRGBA(image.Rect(2, 3, 4, 5)) // 2x2, Min=(2,3)
	img.Set(2, 3, color.RGBA{11, 22, 33, 44})
	img.Set(3, 4, color.RGBA{55, 66, 77, 88})
	pix, w, h := rgbaPixels(img)
	if w != 2 || h != 2 || len(pix) != 16 {
		t.Fatalf("w=%d h=%d len=%d, want 2/2/16", w, h, len(pix))
	}
	if pix[0] != 11 || pix[1] != 22 || pix[2] != 33 || pix[3] != 44 {
		t.Fatalf("first pixel = %v, want 11,22,33,44", pix[:4])
	}
	if pix[12] != 55 || pix[15] != 88 {
		t.Fatalf("last pixel = %v, want 55,66,77,88", pix[12:16])
	}
}

func TestStripMarkup(t *testing.T) {
	cases := map[string]string{
		"plain text":  "plain text",
		"<b>bold</b>": "bold",
		"a &amp; b &lt;c&gt; &quot;q&quot; &apos;a&apos;": `a & b <c> "q" 'a'`,
		"trailing <tag never closed":                      "trailing ",
		"bare & ampersand":                                "bare & ampersand",
	}
	for in, want := range cases {
		if got := stripMarkup(in); got != want {
			t.Errorf("stripMarkup(%q) = %q, want %q", in, got, want)
		}
	}
}
