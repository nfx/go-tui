---
title: "Alternatives"
weight: 170
---

go-tui is for an existing Go command that needs to ask questions, select typed
values, report progress and print useful output. Each operation fits ordinary
Go control flow and accepts familiar contexts, streams or iterators. Your
application keeps ownership of its workflow.

## One workflow, interactive or automated

Non-TTY defaults are part of the prompt contract. When output is not a terminal,
or [CI](https://en.wikipedia.org/wiki/Continuous_integration) disables interaction, prompts use explicit defaults without reading input
or printing prompts. Defaults still undergo validation and choice matching.
Missing defaults return [`ErrNoTTY`](options.md#common-errors); invalid or ambiguous defaults return
[`ErrInvalidOption`](options.md#common-errors). [`Confirm`](dropdowns.md#confirm) is the bool-only convenience exception: failures
return `false`. See [Shared Options and Helpers](options.md#defaults-without-a-terminal).

This makes it practical to reuse command logic in a terminal, CI and redirected
execution without maintaining a separate prompt implementation. [Progress bars](progressbar.md)
and [spinners](spinners.md) draw nothing in those environments and report their documented
events through `slog`; see [Progress Bars](progressbar.md#without-a-terminal) and
[Spinners](spinners.md).

## Prompts, progress and logs together

Widgets share a terminal coordinator. Route ordinary logs and other output
through [`tui.Stderr()`](options.md#stderr) to keep them ordered with default widgets. This reduces
the integration work of combining packages that each redraw the terminal.
Writers that bypass the coordinator, including direct `os.Stderr` writes, do
not participate. A logger such as [tint](https://github.com/lmittmann/tint) is a
useful companion: give its handler [`tui.Stderr()`](options.md#stderr) as the destination.

### Multiple progress bars without a separate library

Libraries such as [mpb](https://github.com/vbauerster/mpb) exist largely to
manage several bars drawing to one terminal. With go-tui you do not need one:
every bar created with [`tui.NewMaxProgressBar`](progressbar.md#newmaxprogressbar) registers with the same
coordinator, so concurrent goroutines can advance their own bars while each
keeps its own rate and estimate, and logs written through `tui.Stderr()` stay
ordered above them. No container, wait group wrapper or render loop is involved.

{{< recorded name="progress/progress-multi" title="Multiple progress bars advancing concurrently" >}}

Dynamic totals and custom decorators are not supported; if you need those,
mpb remains the better fit. See [Progress Bars](progressbar.md) for the full API.

## Choose the amount of UI architecture you need

The production module directly requires only `golang.org/x/sys` and
`golang.org/x/term`. [Bubble Tea](https://github.com/charmbracelet/bubbletea)
brings a larger [dependency graph](https://github.com/charmbracelet/bubbletea/blob/main/go.mod)
and an explicit application model for [UI](https://en.wikipedia.org/wiki/User_interface) state and events. That is an intentional
architecture choice: valuable when building an application around its UI, more
structure than a command needs just to ask a question. With go-tui, adding
interactivity does not require reorganizing the application around that model.

Rich interactive applications with grids, pages, trees and navigable views are
outside go-tui's scope. Bubble Tea offers a better architecture for those
applications; [tview](https://github.com/rivo/tview) supplies rich widgets and
layouts. go-tui's [tables](table.md) are formatted output, not a dashboard framework.

For more specialized requirements, consider:

- [Huh](https://github.com/charmbracelet/huh) for grouped forms, dynamic fields
  and a dedicated screen-reader accessible mode. Sequential go-tui calls can
  express a questionnaire, but do not provide a multi-field form controller.
- [readline](https://github.com/chzyer/readline) for a [REPL](https://en.wikipedia.org/wiki/Read–eval–print_loop) editor with history
  and completion. go-tui's [`Input`](input.md) and [`Password`](input.md) are individual prompts.

Non-TTY defaults are an automation feature, not a screen-reader interaction
mode. Dependency counts also do not establish binary size or rendering speed;
those require equivalent-workload measurements.
