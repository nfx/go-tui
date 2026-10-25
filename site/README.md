# Documentation site

Hugo site (Hextra theme) for go-tui. Pages live in `content/`.

```bash
make site-preview   # live server
make lint-site      # build and check links with htmltest
```

`site/examples` is a build artifact copied from the top-level `examples/`
directory by `make`. Edit the originals, never the copy.

## Embedding examples with the `example` shortcode

Docs show real, compiled example programs rather than hand-pasted snippets, so
they cannot drift from the API. Use the `example` shortcode from
`layouts/shortcodes/example.html`:

```text
{{< example name="progress/progress-multi" >}}
```

`name` is the path under `examples/` and is required. The file is `main.go`
unless `name` ends in `.go`. By default the body of `func main` is rendered
without the signature and one level of indentation. Pick one selector to
override that:

| Parameter | Renders                                                                   |
|-----------|---------------------------------------------------------------------------|
| `func`    | body of `func <name>(` without the signature and one level of indentation |
| `section` | lines between `// BEGIN <name>` and `// END <name>` markers               |
| `lines`   | explicit ranges such as `3-10,15-20`                                      |

Optional: `language` (defaults to the file extension), `trim`, `trim_start`
and `trim_end` (drop lines from a `section`).

## Showing a recording next to its code with `recorded`

Most examples have a demo recording. The `recorded` shortcode renders both as
"Demo" and "Code" tabs:

```text
{{< recorded name="progress/progress-multi" title="Multiple progress bars advancing concurrently" >}}
```

`name` is the example path (also the GIF path under `static/demos/` without the
`.gif` suffix) and `title` is the alt text. Every other parameter is passed to
`example`, so `func`, `section`, `lines` and `trim` work as above. Use the
plain `example` shortcode for examples without a recording.

## Cross-linking API references

When prose mentions an API symbol or option, link it to the heading that
documents it instead of leaving bare code formatting. Each widget page uses
one heading per function or option, so anchors are stable and predictable:

```markdown
[`tui.NewMaxProgressBar`](progressbar.md#newmaxprogressbar)
[`tui.Stderr()`](options.md#stderr)
```

- Link with the relative `page.md#heading` form, not a URL, so `lint-site` can
  verify it.
- Anchors are the lowercased heading text; check the heading exists when you
  rename or add one.
- Link the first mention in a section; repeating the link on every mention is
  noise.
