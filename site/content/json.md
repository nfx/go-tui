---
title: "Pretty JSON"
weight: 110
---

API:

- `PrettyJSON(w, src)`

`PrettyJSON` indents [JSON](https://en.wikipedia.org/wiki/JSON) and applies depth-aware [ANSI](https://en.wikipedia.org/wiki/ANSI_escape_code) coloring when writing to a terminal.

## Input types

- `string`
- `[]byte`
- Any Go value marshalable by `encoding/json`

## Example

{{< example name="prettyjson/pretty-json" >}}

## Behavior

- Non-terminal output: plain indented JSON.
- Terminal output: colored keys/values by nesting depth.
- Invalid JSON input returns an error from the indent/marshal stage.
