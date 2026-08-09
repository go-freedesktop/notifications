// Copyright (c) 2026 the go-freedesktop/notifications authors. All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"strings"
	"testing"

	"github.com/go-freedesktop/notifications"
	ntoast "github.com/go-freedesktop/notifications/toast"
)

func TestParseFlagsDefaults(t *testing.T) {
	cfg, err := parseFlags(nil, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("parseFlags(nil): %v", err)
	}
	if cfg.replace || cfg.verbose || cfg.version {
		t.Fatalf("unexpected boolean defaults: %+v", cfg)
	}
	if cfg.timeoutMS != ntoast.DefaultExpireMS {
		t.Fatalf("default timeout = %d, want %d", cfg.timeoutMS, ntoast.DefaultExpireMS)
	}
}

func TestParseFlagsSet(t *testing.T) {
	cfg, err := parseFlags([]string{"-replace", "-verbose", "-timeout", "1500"}, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("parseFlags: %v", err)
	}
	if !cfg.replace || !cfg.verbose || cfg.timeoutMS != 1500 {
		t.Fatalf("parsed = %+v", cfg)
	}
}

func TestParseFlagsNonPositiveTimeoutFallsBack(t *testing.T) {
	cfg, err := parseFlags([]string{"-timeout", "0"}, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("parseFlags: %v", err)
	}
	if cfg.timeoutMS != ntoast.DefaultExpireMS {
		t.Fatalf("timeout 0 -> %d, want default %d", cfg.timeoutMS, ntoast.DefaultExpireMS)
	}
}

func TestParseFlagsHelp(t *testing.T) {
	var out bytes.Buffer
	_, err := parseFlags([]string{"-h"}, &out)
	if !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("-h err = %v, want ErrHelp", err)
	}
	if !strings.Contains(out.String(), notifications.BusName) {
		t.Fatalf("usage did not mention the bus name:\n%s", out.String())
	}
}

func TestParseFlagsBad(t *testing.T) {
	_, err := parseFlags([]string{"-nope"}, &bytes.Buffer{})
	if err == nil || errors.Is(err, flag.ErrHelp) {
		t.Fatalf("bad flag err = %v, want a parse error", err)
	}
}

// fakeExporter drives claim's branches.
type fakeExporter struct {
	exportErr, replaceErr error
	usedReplace           bool
}

func (f *fakeExporter) Export() error { return f.exportErr }
func (f *fakeExporter) ExportReplace() error {
	f.usedReplace = true
	return f.replaceErr
}

func TestClaim(t *testing.T) {
	logf := func(string, ...any) {}

	// Success (plain Export).
	f := &fakeExporter{}
	if code, ok := claim(f, false, logf); !ok || code != exitOK || f.usedReplace {
		t.Fatalf("success: code=%d ok=%v replace=%v", code, ok, f.usedReplace)
	}

	// -replace routes through ExportReplace.
	fr := &fakeExporter{}
	if code, ok := claim(fr, true, logf); !ok || code != exitOK || !fr.usedReplace {
		t.Fatalf("replace success: code=%d ok=%v replace=%v", code, ok, fr.usedReplace)
	}

	// Name taken -> exitNameTaken, not ok.
	ft := &fakeExporter{exportErr: notifications.ErrNameTaken}
	if code, ok := claim(ft, false, logf); ok || code != exitNameTaken {
		t.Fatalf("name-taken: code=%d ok=%v, want (%d,false)", code, ok, exitNameTaken)
	}

	// Any other error -> exitError, not ok.
	fe := &fakeExporter{exportErr: errors.New("boom")}
	if code, ok := claim(fe, false, logf); ok || code != exitError {
		t.Fatalf("other-error: code=%d ok=%v, want (%d,false)", code, ok, exitError)
	}
}

// recHandler records forwarded calls and can be told to panic.
type recHandler struct {
	got          *notifications.Notification
	closedID     uint32
	panicNotify  bool
	panicClose   bool
	notifyCalled bool
	closeCalled  bool
}

func (h *recHandler) OnNotify(n *notifications.Notification) uint32 {
	h.notifyCalled = true
	if h.panicNotify {
		panic("notify boom")
	}
	h.got = n
	return n.ID
}
func (h *recHandler) OnClose(id uint32, _ notifications.CloseReason) {
	h.closeCalled = true
	if h.panicClose {
		panic("close boom")
	}
	h.closedID = id
}

func TestSafeHandlerDefaultTimeoutAndVerbose(t *testing.T) {
	var logs bytes.Buffer
	inner := &recHandler{}
	h := &safeHandler{inner: inner, timeoutMS: 2500, verbose: true,
		logf: func(f string, a ...any) { logs.WriteString(fmt.Sprintf(f, a...)) }}

	n := &notifications.Notification{ID: 7, AppName: "mail", Summary: "hi", ExpireMS: -1}
	if id := h.OnNotify(n); id != 7 {
		t.Fatalf("OnNotify id = %d, want 7", id)
	}
	if n.ExpireMS != 2500 {
		t.Fatalf("default timeout not applied: ExpireMS=%d", n.ExpireMS)
	}
	if inner.got != n {
		t.Fatal("inner handler did not receive the notification")
	}
	if !strings.Contains(logs.String(), "id=7") {
		t.Fatalf("verbose log missing: %q", logs.String())
	}
}

func TestSafeHandlerKeepsExplicitTimeout(t *testing.T) {
	inner := &recHandler{}
	h := &safeHandler{inner: inner, timeoutMS: 2500, logf: func(string, ...any) {}}
	n := &notifications.Notification{ID: 1, ExpireMS: 800}
	h.OnNotify(n)
	if n.ExpireMS != 800 {
		t.Fatalf("explicit timeout overwritten: %d", n.ExpireMS)
	}
}

func TestSafeHandlerPanicIsolation(t *testing.T) {
	var logs bytes.Buffer
	logf := func(f string, a ...any) { logs.WriteString(fmt.Sprintf(f, a...)) }

	inner := &recHandler{panicNotify: true}
	h := &safeHandler{inner: inner, timeoutMS: 1000, logf: logf}
	n := &notifications.Notification{ID: 42, ExpireMS: 1000}
	if id := h.OnNotify(n); id != 42 {
		t.Fatalf("panic recovery returned id %d, want 42 (pre-assigned)", id)
	}
	if !strings.Contains(logs.String(), "recovered from panic handling") {
		t.Fatalf("panic not logged: %q", logs.String())
	}

	// OnClose panic is likewise isolated (no re-raise escapes).
	logs.Reset()
	inner2 := &recHandler{panicClose: true}
	h2 := &safeHandler{inner: inner2, logf: logf}
	h2.OnClose(9, notifications.ReasonClosed)
	if !strings.Contains(logs.String(), "recovered from panic closing") {
		t.Fatalf("close panic not logged: %q", logs.String())
	}

	// OnClose without a panic forwards through.
	inner3 := &recHandler{}
	h3 := &safeHandler{inner: inner3, logf: logf}
	h3.OnClose(5, notifications.ReasonExpired)
	if !inner3.closeCalled || inner3.closedID != 5 {
		t.Fatalf("OnClose not forwarded: %+v", inner3)
	}
}

func TestRunVersion(t *testing.T) {
	var out bytes.Buffer
	if code := run([]string{"-version"}, &out); code != exitOK {
		t.Fatalf("run -version exit = %d, want %d", code, exitOK)
	}
	if !strings.Contains(out.String(), notifications.Version) {
		t.Fatalf("version output = %q", out.String())
	}
}

func TestRunHelpAndBadFlag(t *testing.T) {
	var out bytes.Buffer
	if code := run([]string{"-h"}, &out); code != exitOK {
		t.Fatalf("run -h exit = %d, want %d", code, exitOK)
	}
	if code := run([]string{"-nope"}, &bytes.Buffer{}); code != exitUsage {
		t.Fatalf("run -nope exit = %d, want %d", code, exitUsage)
	}
}
