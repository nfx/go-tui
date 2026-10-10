---
title: "Text, Width and Escapes"
weight: 90
---

Widgets share one text model (`chunks.go`) for measuring, clipping, wrapping,
and cursor movement. That keeps terminal layout consistent.

## What is supported

- **Columns, not bytes or runes.** Wide/fullwidth characters and emoji clusters
  take two columns; combining marks take none.
- **Clusters stay intact.** Cursor movement, deletion, wrapping, and truncation
  keep base characters with combining marks, variation selectors, Hangul jamo,
  emoji modifiers, keycaps, tags, ZWJ sequences, and flags. Password inputs
  intentionally move and delete by rune.
- **Escape sequences have zero width.** CSI plus OSC, DCS, SOS, PM, APC, and
  two-byte/nF sequences are kept whole. OSC may end in BEL; control strings
  otherwise end in `ESC \\`.
- **Writes may split sequences.** A viewport retains a trailing rune or escape
  sequence for the next write, up to 4096 bytes. Truncation drops incomplete
  sequences.
- **Truncation closes styles.** If text leaves SGR styling open, output ends in
  one `ESC [0m`. Invalid UTF-8 becomes one `U+FFFD` column.

Widths come from the Go toolchain's Unicode data (17.0 for Go 1.27). Regenerate
`glyphs.go` with `go run glyphs_gen.go | gofmt > glyphs.go`.

## What is not supported

- C1 controls, including 8-bit CSI, are zero-width controls, not escape
  introducers. Tabs are not expanded; tabs and line breaks are zero-width.
- Ambiguous-width characters use one column. Emoji and combining behavior can
  differ on terminals with older Unicode data.
- This is not full UAX #29 segmentation: Indic conjuncts, prepended characters,
  and out-of-order Hangul jamo are measured by their parts. `U+FE0E` is ignored;
  `U+FE0F` widens only non-ASCII emoji.
- A cluster split between viewport writes is measured as two clusters. RTL text
  is measured but not reordered.
- Escape sequences are preserved, not executed. Embedded cursor movement or
  screen controls can still disrupt layout. Only SGR state is tracked.
- Broken escapes are dropped because they could swallow following text. A
  viewport stops waiting for an unterminated control string after 4096 bytes.
