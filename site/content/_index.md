---
title: go-tui
layout: hextra-home
---

<div class="hx:mt-6 hx:mb-6">
{{< hextra/hero-headline >}}
  Interactivity for your Go CLI
{{< /hextra/hero-headline >}}
</div>

<div class="hx:mb-12">
{{< hextra/hero-subtitle >}}
  Typed prompts, coordinated progress and logs, and explicit defaults for automation. Keep your application's control flow.
{{< /hextra/hero-subtitle >}}
</div>

<div class="hx:mb-6">
{{< hextra/hero-button text="Get Started" link="docs/getting-started/" >}}
</div>

{{< hextra/feature-grid >}}
  {{< hextra/feature-card
    title="Keep your architecture"
    icon="sparkles"
    subtitle="Call widgets from ordinary Go functions, with contexts, readers, writers and typed results."
  >}}
  {{< hextra/feature-card
    title="One terminal coordinator"
    icon="puzzle"
    subtitle="Prompts and progress coexist with logs routed through tui.Stderr()."
  >}}
  {{< hextra/feature-card
    title="Ready for automation"
    icon="lightning-bolt"
    subtitle="Without a TTY, prompts validate explicit defaults without reading input; progress becomes structured logs."
  >}}
{{< /hextra/feature-grid >}}

go-tui directly depends only on `golang.org/x/sys` and `golang.org/x/term`.
It fits commands that need interactivity alongside their existing work.
Rich applications with grids, pages, trees and navigable views are outside
its scope; read [Alternatives](docs/alternatives/) for the tradeoffs.
