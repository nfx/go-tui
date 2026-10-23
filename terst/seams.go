// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package terst

import (
	"strings"
	"testing"

	"github.com/nfx/go-tui/internal/term"
)

// WithEnv makes the output environment of the test the one that the
// KEY=VALUE pairs describe, as if they were the only variables set.
func WithEnv(t *testing.T, pairs ...string) {
	t.Helper()
	vars := map[string]string{}
	for _, p := range pairs {
		k, v, _ := strings.Cut(p, "=")
		vars[k] = v
	}
	swap(t, &term.Default.Env, term.ReadOutputEnv(func(k string) (string, bool) { v, ok := vars[k]; return v, ok }))
}

// swap sets *p to v for the test and restores the previous value in cleanup.
func swap[T any](t *testing.T, p *T, v T) {
	t.Helper()
	prev := *p
	*p = v
	t.Cleanup(func() { *p = prev })
}

// StrictOutput restores production terminal classification for a test, where
// writers without terminal capabilities are not terminals.
func StrictOutput(t *testing.T) {
	t.Helper()
	swap(t, &term.OutputIsTerminal, term.HostsTerminal)
}
