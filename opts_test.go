// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"bytes"
	"testing"
	"time"
)

type optsDummy struct {
	config
}

func TestWithIOOptions(t *testing.T) {
	reader := &bytes.Buffer{}
	writer := &bytes.Buffer{}
	d := &optsDummy{}

	if err := WithInput(reader)(d); err != nil {
		t.Fatalf("WithInput failed: %v", err)
	}
	if err := WithOutput(writer)(d); err != nil {
		t.Fatalf("WithOutput failed: %v", err)
	}
	if d.in != reader {
		t.Fatalf("reader not set")
	}
	if d.out != writer {
		t.Fatalf("writer not set")
	}
}

func TestWithContextAndTimeout(t *testing.T) {
	base := t.Context()
	d := &optsDummy{}

	if err := WithContext(base)(d); err != nil {
		t.Fatalf("WithContext failed: %v", err)
	}
	if d.ctx != base {
		t.Fatalf("context not set")
	}

	if err := WithTimeout(10 * time.Millisecond)(d); err != nil {
		t.Fatalf("WithTimeout failed: %v", err)
	}
	deadline, ok := d.ctx.Deadline()
	if !ok {
		t.Fatalf("expected deadline set on context")
	}
	if deadline.Before(time.Now()) {
		t.Fatalf("deadline should be in the future")
	}
}
