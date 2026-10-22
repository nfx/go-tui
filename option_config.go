// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"context"
	"io"
)

type Config struct {
	ctx context.Context
	in  io.Reader
	out io.Writer
}

// implement [withIO] interface.
func (c *Config) SetReader(r io.Reader) {
	c.in = r
}

// implement [withIO] interface.
func (c *Config) SetWriter(w io.Writer) {
	c.out = w
}
