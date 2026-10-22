// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package render

import (
	"strings"
	"testing"
	"text/template"

	"github.com/nfx/go-tui/internal/assert"
)

// execute renders src with funcs and fails the test on any error.
func execute(t *testing.T, funcs template.FuncMap, src string, data any) string {
	t.Helper()
	tmpl, err := template.New("t").Funcs(funcs).Parse(src)
	assert.NoError(t, err)
	var b strings.Builder
	assert.NoError(t, tmpl.Execute(&b, data))
	return b.String()
}
