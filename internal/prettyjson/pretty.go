// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package prettyjson

import (
	"bytes"
	"context"
	"io"

	"github.com/nfx/go-tui/internal/term"
)

// PrettyJSON pretty-prints JSON data, with two-space indentation unless the theme sets another [theme.JSON].
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
