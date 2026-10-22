// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package option

import (
	"context"
	"io"
)

type Config struct {
	Ctx context.Context
	In  io.Reader
	Out io.Writer
}

// implement [withIO] interface.
func (c *Config) SetReader(r io.Reader) {
	c.In = r
}

// implement [withIO] interface.
func (c *Config) SetWriter(w io.Writer) {
	c.Out = w
}
