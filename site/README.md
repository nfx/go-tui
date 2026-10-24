# Documentation site

Hugo site (Hextra theme) for go-tui. Pages live in `content/`.

```bash
make site-preview   # live server
make lint-site      # build and check links with htmltest
```

`site/examples` is a build artifact copied from the top-level `examples/`
directory by `make`. Edit the originals, never the copy.

## Embedding examples with the `code` shortcode

Docs show real, compiled example programs rather than hand-pasted snippets, so
they cannot drift from the API. Use the `code` shortcode from
`layouts/shortcodes/code.html`:

```text
{{< code filename="examples/progress/progress-multi/main.go" func="main" >}}
```

`filename` is relative to `site/` and is required. Pick one selector:

| Parameter        | Renders                                                                   |
|------------------|---------------------------------------------------------------------------|
| `func`           | body of `func <name>(` without the signature and one level of indentation |
| `section`        | lines between `// BEGIN <name>` and `// END <name>` markers               |
| `example_output` | the `// Output:` comment of a `func <name>` Go example, as plain text     |
| `lines`          | explicit ranges such as `3-10,15-20`                                      |
| _(none)_         | the whole file                                                            |

Optional: `language` (defaults to the file extension), `trim`, `trim_start`
and `trim_end` (drop lines from a `section` or `example_output`).

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
