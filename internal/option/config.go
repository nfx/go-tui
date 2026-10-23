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
