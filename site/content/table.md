---
title: "Tables"
weight: 90
---

## Table

Renders rows with an explicit row template.

Use this when you want complete control over rendered columns.

{{< demo src="table/basic" alt="A table rendered with an explicit row template" >}}

{{< example name="table/basic" >}}

Wide characters, emoji and escape sequences are measured in columns, and multiline cells stay aligned:

{{< demo src="table/table-multiline" alt="A table with multiline cells" >}}

{{< example name="table/table-multiline" >}}

## TableIter

Consumes an iterator (`iter.Seq2`) and renders rows incrementally.

Iterator fetch errors are reported with row numbers.

{{< demo src="table/table-iter" alt="Rows streaming into a table from an iterator" >}}

{{< example name="table/table-iter" >}}

## TableX

Auto-generates a row template from struct metadata.

Use this for quick tabular output with minimal setup.

`TableAuto` takes options to skip, include, format and color columns, and `Facts` prints a single struct.

{{< demo src="table/table-auto" alt="Automatic tables with column options and facts" >}}

{{< example name="table/table-auto" >}}

## Header inference and tags

- Header names are inferred from `json` / `xml` tags first, then field names.
- `header:"CUSTOM"` overrides inferred headers.
- Header options: `align-right` and `align-left`.
- Nested structs are flattened unless the nested type exposes `String()`.

## Template helpers

Table templates use predefined helper functions documented in
[Template functions](themes.md#template-functions).
