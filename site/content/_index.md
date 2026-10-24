---
title: go-tui
layout: hextra-home
cascade:
  type: docs
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

<div style="width:100%;display:grid;grid-template-columns:repeat(auto-fit,minmax(min(100%,420px),1fr));gap:1.5rem;align-items:start;margin-top:3rem">
<div style="min-width:0">

{{< code filename="examples/misc/everything-and-logs/main.go" func="main" >}}

</div>
<div style="min-width:0">

{{< demo src="demos/misc/everything-and-logs.gif" alt="Prompts, spinners, progress and logs sharing one terminal" >}}

</div>
</div>
