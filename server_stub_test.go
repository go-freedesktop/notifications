// Copyright (c) 2026 the go-freedesktop/notifications authors. All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

//go:build !linux

package notifications

import (
	"errors"
	"testing"
)

// stubHandler satisfies the Handler interface so the stub compiles against it.
type stubHandler struct{}

func (stubHandler) OnNotify(*Notification) uint32 { return 0 }
func (stubHandler) OnClose(uint32, CloseReason)   {}

func TestStubServerUnsupported(t *testing.T) {
	s := NewServer(nil, stubHandler{})
	if s == nil {
		t.Fatal("NewServer returned nil")
	}
	if err := s.Export(); !errors.Is(err, ErrUnsupported) {
		t.Errorf("Export() = %v, want ErrUnsupported", err)
	}
	if err := s.EmitClosed(1, ReasonExpired); !errors.Is(err, ErrUnsupported) {
		t.Errorf("EmitClosed() = %v, want ErrUnsupported", err)
	}
	if err := s.EmitActionInvoked(1, "default"); !errors.Is(err, ErrUnsupported) {
		t.Errorf("EmitActionInvoked() = %v, want ErrUnsupported", err)
	}
}
