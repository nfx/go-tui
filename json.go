// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"bytes"
	"context"
	"github.com/nfx/go-tui/internal/term"
	"io"
)

// PrettyJSON pretty-prints JSON data, with two-space indentation unless the
// theme sets another [JSONStyle]. When the destination writer accepts color,
// keys use the accent color, dimmed with nesting depth, values the
// foreground and brackets a muted shade. Color follows the writer and the
// NO_COLOR, FORCE_COLOR and CLICOLOR_FORCE conventions, never os.Stdout, and
// the terminal is never queried. Without color the output is plain JSON.
// Malformed input returns an error before anything is written.
func PrettyJSON(w io.Writer, src any) error {
	theme := term.Default.CurrentTheme()
	out, err := jsonIndent(src, theme.Source.JSON)
	if err != nil {
		return err
	}
	if p := term.Default.ProfileFor(w); p.Attrs() {
		style := newJSONLook(term.Default.BindTheme(context.Background(), theme, w, nil))
		colored := bytes.NewBuffer(make([]byte, 0, 2*out.Len()))
		style.recolor(out, colored)
		out = colored
	}
	out.WriteByte('\n')
	_, err = out.WriteTo(w)
	return err
}
