---
title: "Number"
weight: 45
---

`Number[T](ctx, label, opts...)` prompts for a decimal number. Specify an integer
or float type, including defined types such as `type Port uint16`; `uintptr`
is unsupported.

```go
count, err := tui.Number[int](ctx, "Workers", tui.WithDefault("4"))
gain, err := tui.Number[float32](ctx, "Gain", tui.WithDefault("1.00"))
port, err := tui.Number[Port](ctx, "Port", tui.WithDefault("8080"))
```

## Up and Down

Up/Down change integers by one. Floats step at the digit right of the caret:
at the end, the last digit; at the decimal point, units; after `12.`, tenths.

| Draft (`\|` is the caret) | Key  | Result    |
| ------------------------- | ---- | --------- |
| `12\|`                    | Up   | `13\|`    |
| `12.\|34`                 | Up   | `12.\|44` |
| `9.99\|`                  | Up   | `10.00\|` |
| `12.\|`                   | Up   | `12.\|1`  |

Steps use exact decimal arithmetic, preserve fractional digits, and never wrap
or clamp. Invalid drafts, out-of-range results, unsigned negatives, and changes
too small for `T` show an error without changing the draft. Empty drafts step
from zero.

## Accepted text

Use plain decimal notation with an optional sign and, for floats, `.` (`1.`,
`.5`, `+1.25`). Exponents, hexadecimal, `NaN`, `Inf`, digit separators, and locale
formatting are rejected. Integers retain full precision; floats round to `T`.
Overflow and nonzero underflow to zero are rejected.

Enter rejects empty or invalid drafts, including invalid defaults, leaving them
editable. The answer preserves the submitted text (`1.00` stays `1.00`).
Cancellation or errors return T's zero value.

## Options

Only `WithDefault`, `WithAtLeast`, `WithAtMost`, `WithInput` and `WithOutput` apply;
other [input](../input/) options, such as `WithValidation` and `WithRegex`,
fail with `ErrWrongWidget`.

`WithAtLeast` and `WithAtMost` set inclusive bounds of any numeric type, compared
exactly (`WithAtMost(0.1)` accepts a `float32` `0.1`). Enter rejects values outside
them, and Up/Down refuse to step past them, though they still step toward the
range from outside. A bound above the other, or a NaN, infinite, or
out-of-range bound, fails with `ErrInvalidOption`.

```go
port, err := tui.Number[Port](ctx, "Port", tui.WithAtLeast(1024), tui.WithAtMost(65535))
```
