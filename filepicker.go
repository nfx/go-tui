// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"context"

	"github.com/nfx/go-tui/internal/choice"
	"github.com/nfx/go-tui/internal/option"
)

func WithExtensions(exts ...string) option.Opt {
	return choice.WithExtensions(exts...)
}

func WithStartDir(dir string) option.Opt {
	return choice.WithStartDir(dir)
}

func WithIgnoreUp() option.Opt {
	return choice.WithIgnoreUp()
}

func WithIgnoreDirs() option.Opt {
	return choice.WithIgnoreDirs()
}

func WithShowHidden() option.Opt {
	return choice.WithShowHidden()
}

func FilePicker(ctx context.Context, title string, o ...option.Opt) (string, error) {
	return choice.FilePicker(ctx, title, o...)
}
