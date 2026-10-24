---
title: "Text, Width and Escapes"
weight: 150
---

Widgets share one text model for measuring, clipping, wrapping,
and cursor movement. That keeps terminal layout consistent.

## What is supported

- **Columns, not bytes or runes.** Wide/fullwidth characters and emoji clusters
  take two columns; combining marks take none.
- **Clusters stay intact.** Cursor movement, deletion, wrapping, and truncation
  keep base characters with combining marks, variation selectors, Hangul jamo,
  emoji modifiers, keycaps, tags, [ZWJ](https://en.wikipedia.org/wiki/Zero-width_joiner) sequences, and flags. `Password` inputs
  intentionally move and delete by rune.
- **Escape sequences have zero width.** [CSI](https://en.wikipedia.org/wiki/ANSI_escape_code#CSI_(Control_Sequence_Introducer)_sequences) plus [OSC](https://en.wikipedia.org/wiki/ANSI_escape_code#OSC_(Operating_System_Command)_sequences), [DCS](https://en.wikipedia.org/wiki/ANSI_escape_code#Fe_Escape_sequences), [SOS](https://en.wikipedia.org/wiki/ANSI_escape_code#Fe_Escape_sequences), [PM](https://en.wikipedia.org/wiki/ANSI_escape_code#Fe_Escape_sequences), [APC](https://en.wikipedia.org/wiki/ANSI_escape_code#Fe_Escape_sequences), and
  two-byte/nF sequences are kept whole. OSC may end in [BEL](https://en.wikipedia.org/wiki/Bell_character); control strings
  otherwise end in `ESC \\`.
- **Writes may split sequences.** A viewport retains a trailing rune or escape
  sequence for the next write, up to 4096 bytes. Truncation drops incomplete
  sequences.
- **Truncation closes styles.** If text leaves [SGR](https://en.wikipedia.org/wiki/ANSI_escape_code#SGR_(Select_Graphic_Rendition)_parameters) styling open, output ends in
  one `ESC [0m`. Invalid [UTF-8](https://en.wikipedia.org/wiki/UTF-8) becomes one `U+FFFD` column.

## What is not supported

- [C1 controls](https://en.wikipedia.org/wiki/C0_and_C1_control_codes#C1_controls), including 8-bit [CSI](https://en.wikipedia.org/wiki/ANSI_escape_code#CSI_(Control_Sequence_Introducer)_sequences), are zero-width controls, not escape
  introducers. Tabs are not expanded; tabs and line breaks are zero-width.
- Ambiguous-width characters use one column. Emoji and combining behavior can
  differ on terminals with older Unicode data.
- This is not full [UAX #29](https://unicode.org/reports/tr29/) segmentation: Indic conjuncts, prepended characters,
  and out-of-order Hangul jamo are measured by their parts. [`U+FE0E`](https://en.wikipedia.org/wiki/Variation_Selectors_(Unicode_block)) is ignored;
  [`U+FE0F`](https://en.wikipedia.org/wiki/Variation_Selectors_(Unicode_block)) widens only non-ASCII emoji.
- A cluster split between viewport writes is measured as two clusters. [RTL](https://en.wikipedia.org/wiki/Bidirectional_text) text
  is measured but not reordered.
- Escape sequences are preserved, not executed. Embedded cursor movement or
  screen controls can still disrupt layout. Only SGR state is tracked.
- Broken escapes are dropped because they could swallow following text. A
  viewport stops waiting for an unterminated control string after 4096 bytes.
