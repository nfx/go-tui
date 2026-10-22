// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"bytes"
	"fmt"
	"strings"
)

// formatArgs joins helper arguments without separators.
func (span) formatArgs(args []any) Text {
	var result Text
	for _, t := range args {
		switch x := t.(type) {
		case *string:
			if x != nil {
				result = append(result, *x...)
			}
		case []string:
			result = append(result, strings.Join(x, ", ")...)
		case *int:
			if x != nil {
				result = fmt.Append(result, *x)
			}
		default:
			result = fmt.Append(result, t)
		}
	}
	return result
}

// span is the style one helper call establishes. It is immutable and keeps
// no state between calls, so its helpers are safe for concurrent executions.
type span struct {
	open     []sgrCode
	channels SGRChannel
	closer   string
}

// sgrCode is one opening SGR sequence and the channels it sets.
type sgrCode struct {
	seq      string
	channels SGRChannel
}

// helper adapts s to a template helper with the argument conversions.
func (s span) helper() func(...any) string {
	return func(args ...any) string { return s.wrap(s.formatArgs(args)).String() }
}

// wrap styles body, restoring outer channels after nested resets when content follows.
// Non-SGR escape sequences pass through unchanged.
func (s span) wrap(body Text) Text {
	if len(s.open) == 0 {
		return body
	}
	var out Text
	for _, c := range s.open {
		out = append(out, c.seq...)
	}
	if bytes.IndexByte(body, 0x1b) < 0 {
		out = append(out, body...)
	} else {
		var pending SGRChannel
		for seg := range body.Segments() {
			if pending != 0 {
				s.restore(&out, pending)
			}
			out = append(out, seg.Text...)
			_, cleared := seg.SGREffect()
			pending = cleared & s.channels
		}
	}
	out = append(out, s.closer...)
	return out
}

// restore appends the opening codes of s that set any of channels, in order.
func (s span) restore(out *Text, channels SGRChannel) {
	for _, c := range s.open {
		if c.channels&channels != 0 {
			*out = append(*out, c.seq...)
		}
	}
}
