---
title: "Tables"
weight: 90
---

## Table

Renders rows with an explicit row template.

Use this when you want complete control over rendered columns.

{{< demo src="demos/table/basic.gif" alt="A table rendered with an explicit row template" >}}

{{< code filename="examples/table/basic/main.go" func="main" >}}

Wide characters, emoji and escape sequences are measured in columns, and multiline cells stay aligned:

{{< demo src="demos/table/table-multiline.gif" alt="A table with multiline cells" >}}

{{< code filename="examples/table/table-multiline/main.go" func="main" >}}

## TableIter

Consumes an iterator (`iter.Seq2`) and renders rows incrementally.

Iterator fetch errors are reported with row numbers.

{{< demo src="demos/table/table-iter.gif" alt="Rows streaming into a table from an iterator" >}}

{{< code filename="examples/table/table-iter/main.go" func="main" >}}

## TableX

Auto-generates a row template from struct metadata.

Use this for quick tabular output with minimal setup.

`TableAuto` takes options to skip, include, format and color columns, and `Facts` prints a single struct.

{{< demo src="demos/table/table-auto.gif" alt="Automatic tables with column options and facts" >}}

{{< code filename="examples/table/table-auto/main.go" func="main" >}}

## Header inference and tags

- Header names are inferred from `json` / `xml` tags first, then field names.
- `header:"CUSTOM"` overrides inferred headers.
- Header options: `align-right` and `align-left`.
- Nested structs are flattened unless the nested type exposes `String()`.

## Template helpers

Table templates use predefined helper functions documented in
[Template functions](themes.md#template-functions).
