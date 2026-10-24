---
title: "Tables"
weight: 90
---

## Table

Renders rows with an explicit row template.

```go
type Row struct {
	Name   string
	Active bool
	Score  float64
}

rows := []Row{{"alice", true, 95.2}, {"bob", false, 71.0}}
tmpl := `{{bold .Name}}\t{{if .Active}}yes{{else}}no{{end}}\t{{printf "%.1f" .Score}}`
err := tui.Table(os.Stdout, tmpl, rows)
if err != nil {
	return err
}
```

Use this when you want complete control over rendered columns.

## TableIter

Consumes an iterator (`iter.Seq2`) and renders rows incrementally.

```go
seq := func(yield func(Row, error) bool) {
	for _, r := range rows {
		if !yield(r, nil) {
			return
		}
	}
}

err := tui.TableIter(os.Stdout, `{{.Name}}\t{{.Score}}`, seq)
if err != nil {
	return err
}
```

Iterator fetch errors are reported with row numbers.

## TableX

Auto-generates a row template from struct metadata.

```go
type Row struct {
	Name   string  `json:"name"`
	Active bool    `json:"active"`
	Score  float64 `json:"score"`
}

rows := []Row{{"alice", true, 95.2}, {"bob", false, 71.0}}
err := tui.TableX(os.Stdout, rows)
if err != nil {
	return err
}
```

Use this for quick tabular output with minimal setup.

## Header inference and tags

- Header names are inferred from `json` / `xml` tags first, then field names.
- `header:"CUSTOM"` overrides inferred headers.
- Header options: `align-right` and `align-left`.
- Nested structs are flattened unless the nested type exposes `String()`.

## Template helpers

Table templates use predefined helper functions documented in
[Template functions](themes.md#template-functions).
