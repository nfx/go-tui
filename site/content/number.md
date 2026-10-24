---
title: "Number"
weight: 30
---

`Number[T](ctx, label, opts...)` prompts for a decimal number. Specify an integer
or float type, including defined types such as `type Port uint16`; `uintptr`
is unsupported.

{{< demo src="demos/prompt/number.gif" alt="Number prompts for int, defined and float types" >}}

{{< code filename="examples/prompt/number/main.go" func="main" >}}

## Without a terminal

When the output is not a terminal, `Number` reads no input, prints nothing and
returns [`WithDefault`](options.md#defaults-without-a-terminal) after the usual checks. A numeric default, including a
defined type, is written in plain decimal notation at its own width (never
`1e+21`), then parsed as `T` and checked against the range and the bounds.
Anything else is used as text. A default that is not a valid `T`, overflows it
or is out of bounds fails with [`ErrInvalidOption`](options.md#common-errors) and returns the zero value;
no default fails with [`ErrNoTTY`](options.md#common-errors).

```go
port, err := tui.Number[Port](ctx, "Port", tui.WithDefault(8080)) // 8080 in a CI job
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

Only [`WithDefault`](options.md#defaults-without-a-terminal), `WithAtLeast`, `WithAtMost`, `WithInput` and `WithOutput` apply;
other [input](input.md) options, such as [`WithValidation`](input.md#withvalidation) and [`WithRegex`](input.md#withregex),
fail with [`ErrWrongWidget`](options.md#common-errors).

`WithAtLeast` and `WithAtMost` set inclusive bounds of any numeric type, compared
exactly (`WithAtMost(0.1)` accepts a `float32` `0.1`). Enter rejects values outside
them, and Up/Down refuse to step past them, though they still step toward the
range from outside. A bound above the other, or a NaN, infinite, or
out-of-range bound, fails with [`ErrInvalidOption`](options.md#common-errors).

```go
port, err := tui.Number[Port](ctx, "Port", tui.WithAtLeast(1024), tui.WithAtMost(65535))
```
