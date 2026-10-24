---
title: "Clipboard"
weight: 100
---

API:

- `ShouldPasteFromClipboard() string`

This helper returns clipboard text (trimmed trailing [CR](https://en.wikipedia.org/wiki/Carriage_return)/LF) or an empty string when clipboard access fails.

## Platform backends

- macOS: `pbpaste`
- Linux: `xclip`, then `xsel`, then `wl-paste`
- Windows: `powershell -command Get-Clipboard`

If no implementation is available for the platform, it fails internally with [`ErrUnsupportedPlatform`](options.md#common-errors) and returns an empty string.

## Example

```go
maybePasted := tui.ShouldPasteFromClipboard()
if maybePasted != "" {
	fmt.Println("clipboard:", maybePasted)
}
```
