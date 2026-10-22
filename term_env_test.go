// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/nfx/go-tui/internal/assert"
)

func TestOutputEnvEstimatesBackgroundAtStartup(t *testing.T) {
	vars := map[string]string{"COLORFGBG": "0;15"}
	e := readOutputEnv(func(k string) (string, bool) { v, ok := vars[k]; return v, ok })
	assert.Equal(t, background{light: true}, e.bgHint)
	vars["COLORFGBG"] = "15;0" // later changes are not seen
	assert.Equal(t, background{light: true}, e.bgHint)
	assert.Equal(t, background{}, readOutputEnv(func(string) (string, bool) { return "", false }).bgHint)
}

func TestOutputEnvDetectsUnicode(t *testing.T) {
	for _, tc := range []struct {
		env     string
		legacy  bool
		unicode bool
	}{
		{"", false, true},
		{"LANG=C", false, true},
		{"LANG=en_US.UTF-8", false, true},
		{"LC_ALL=de_DE.utf8@euro", false, true},
		{"LANG=en_US.ISO-8859-1", false, false},
		{"LC_CTYPE=ru_RU.KOI8-R", false, false},
		{"TERM=linux", false, false},
		{"TERM=xterm-256color", false, true},
		{"", true, false},
		{"TERM=xterm", true, false},
		{"WT_SESSION=1", true, true},
		{"TERM_PROGRAM=vscode", true, true},
		{"TERM=alacritty", true, true},
		{"CI=true", true, true},
		{"CI=false", true, false},
	} {
		t.Run(fmt.Sprintf("%s legacy=%v", tc.env, tc.legacy), func(t *testing.T) {
			prev := legacyConsoles
			legacyConsoles = tc.legacy
			t.Cleanup(func() { legacyConsoles = prev })
			vars := map[string]string{}
			if name, v, ok := strings.Cut(tc.env, "="); ok {
				vars[name] = v
			}
			e := readOutputEnv(func(k string) (string, bool) { v, ok := vars[k]; return v, ok })
			assert.Equal(t, tc.unicode, e.hasUnicode())
		})
	}
}
