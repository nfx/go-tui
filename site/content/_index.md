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
  Typed <a href="{{< relref "input" >}}" class="hx:text-primary-600 hx:underline hx:underline-offset-2">prompts</a>, coordinated <a href="{{< relref "progressbar" >}}" class="hx:text-primary-600 hx:underline hx:underline-offset-2">progress</a> and <a href="{{< relref "options#stderr" >}}" class="hx:text-primary-600 hx:underline hx:underline-offset-2">logs</a>, and explicit <a href="{{< relref "options#defaults-without-a-terminal" >}}" class="hx:text-primary-600 hx:underline hx:underline-offset-2">defaults for automation</a>. Keep your application's control flow.
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

{{< example name="misc/everything-and-logs" >}}

</div>
<div style="min-width:0">

{{< demo src="misc/everything-and-logs" alt="Prompts, spinners, progress and logs sharing one terminal" >}}

</div>
</div>
