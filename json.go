// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"bytes"
	"context"
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
	theme := defaultIO.currentTheme()
	out, err := jsonIndent(src, theme.source.JSON)
	if err != nil {
		return err
	}
	if p := defaultIO.profileFor(w); p.attrs() {
		style := newJSONLook(defaultIO.bindTheme(context.Background(), theme, w, nil))
		colored := bytes.NewBuffer(make([]byte, 0, 2*out.Len()))
		style.recolor(out, colored)
		out = colored
	}
	out.WriteByte('\n')
	_, err = out.WriteTo(w)
	return err
}
