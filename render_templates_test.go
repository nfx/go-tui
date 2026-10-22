// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"errors"
	"strings"
	"testing"
	"text/template"
	"time"

	"github.com/nfx/go-tui/internal/assert"
)

// compileTemplates compiles the built-in theme with test-specific edits.
func compileTemplates(t *testing.T, edit func(*Theme)) (*compiledTheme, error) {
	t.Helper()
	th := NewTheme()
	edit(&th)
	return th.compile()
}

func TestTemplateZeroInheritsAndEmptyHides(t *testing.T) {
	c, err := compileTemplates(t, func(th *Theme) {
		th.Templates.Prompt.Label = Template("{{ bold .Label }}")
		th.Templates.Dropdown.Hint = Hidden
	})
	if err != nil {
		t.Fatal(err)
	}
	tt := c.templates
	for p, s := range map[string]*compiledSlot{
		"Prompt": tt.Prompt.Label, "Input": tt.Input.Label, "Password": tt.Password.Label,
		"Number": tt.Number.Label, "Dropdown": tt.Dropdown.Label, "Multichoice": tt.Multichoice.Label,
		"Confirm": tt.Confirm.Label, "FilePicker": tt.FilePicker.Label,
	} {
		if s == nil || s.tmpl == nil {
			t.Errorf("%s.Label did not inherit the shared label", p)
		}
	}
	if s := tt.Dropdown.Hint; s == nil || !s.hidden {
		t.Error("explicit empty did not hide Dropdown.Hint")
	}
	if s := tt.Multichoice.Hint; s == nil || s != tt.Prompt.Hint {
		t.Error("unset slot must fall to the built-in default")
	}
}

func TestTemplateFamilyOverrideStaysLocal(t *testing.T) {
	c, err := compileTemplates(t, func(th *Theme) {
		th.Templates.Prompt.Label = Template("shared")
		th.Templates.Input.Label = Template("input")
	})
	if err != nil {
		t.Fatal(err)
	}
	tt := c.templates
	for _, tc := range []struct {
		path string
		slot *compiledSlot
		want string
	}{
		{"Prompt.Label", tt.Prompt.Label, "shared"},
		{"Input.Label", tt.Input.Label, "input"},
		{"Password.Label", tt.Password.Label, "input"},
		{"Number.Label", tt.Number.Label, "input"},
		{"Dropdown.Label", tt.Dropdown.Label, "shared"},
	} {
		if got := tc.slot.tmpl.Root.String(); got != tc.want {
			t.Errorf("%s = %q, want %q", tc.path, got, tc.want)
		}
	}
}

func TestTemplateRequiredSlotRejectsEmpty(t *testing.T) {
	for _, path := range []string{"Choice.Active", "Choice.Inactive", "Choice.Value", "Multichoice.Row"} {
		_, err := compileTemplates(t, func(th *Theme) {
			switch path {
			case "Choice.Active":
				th.Templates.Choice.Active = Hidden
			case "Choice.Inactive":
				th.Templates.Choice.Inactive = Hidden
			case "Choice.Value":
				th.Templates.Choice.Value = Hidden
			default:
				th.Templates.Multichoice.Row = Hidden
			}
		})
		if err == nil || !strings.Contains(err.Error(), path) {
			t.Errorf("%s: want path-qualified error, got %v", path, err)
		}
	}
}

func TestTemplateErrorNamesDefiningSlot(t *testing.T) {
	_, err := compileTemplates(t, func(th *Theme) {
		th.Templates.Prompt.Answer = Template("{{ .Value ")
	})
	if err == nil || !strings.Contains(err.Error(), "theme.Templates.Prompt.Answer") {
		t.Fatalf("got %v", err)
	}
	_, err = compileTemplates(t, func(th *Theme) {
		th.Templates.Input.Label = Template("{{ nosuchhelper .Label }}")
	})
	if err == nil || !strings.Contains(err.Error(), "theme.Templates.Input.Label") {
		t.Fatalf("unknown helper: got %v", err)
	}
}

func TestTemplateDefineIsIsolatedPerSlot(t *testing.T) {
	c, err := compileTemplates(t, func(th *Theme) {
		th.Templates.Prompt.Label = Template(`{{ define "x" }}A{{ end }}{{ template "x" }}`)
		th.Templates.Prompt.Hint = Template(`{{ define "x" }}B{{ end }}{{ template "x" }}`)
	})
	if err != nil {
		t.Fatal(err)
	}
	exec := func(s *compiledSlot) string {
		var b strings.Builder
		if err := s.tmpl.Execute(&b, nil); err != nil {
			t.Fatal(err)
		}
		return b.String()
	}
	if a, b := exec(c.templates.Prompt.Label), exec(c.templates.Prompt.Hint); a != "A" || b != "B" {
		t.Fatalf("define leaked between slots: %q %q", a, b)
	}
}

// keepTheme restores the installed theme after a test.
func keepTheme(t *testing.T) *compiledTheme {
	t.Helper()
	prev := defaultIO.currentTheme()
	t.Cleanup(func() { defaultIO.theme.Store(prev) })
	return prev
}

func TestBoundSlotRendersOrFailsWhole(t *testing.T) {
	boom := errors.New("boom")
	c, err := compileTemplates(t, func(th *Theme) {
		th.Funcs = template.FuncMap{"fails": func(string) (string, error) { return "", boom }}
		th.Templates.Prompt.Label = `{{ bold .Label }}`
		th.Templates.Prompt.Hint = `{{ bold "partial" }}{{ fails .Label }}`
		th.Templates.Prompt.Feedback = "{{ .Label }}\n"
		th.Templates.Prompt.Answer = "{{ .Label }}\x1b[2K"
		th.Templates.Choice.Empty = Hidden
	})
	assert.NoError(t, err)
	data := struct{ Label string }{"x"}
	render := func(s *compiledSlot, p Profile) (Text, error) {
		b, err := s.bind(c.funcs(p, roleStyles{}))
		assert.NoError(t, err)
		return b.render(data)
	}

	out, err := render(c.templates.Prompt.Label, ProfileTrueColor)
	assert.NoError(t, err)
	assert.Equal(t, "\x1b[1mx\x1b[0m", out.String())
	out, err = render(c.templates.Prompt.Label, ProfilePlain)
	assert.NoError(t, err)
	assert.Equal(t, "x", out.String()) // binding does not change the installed slot

	out, err = render(c.templates.Prompt.Hint, ProfileTrueColor)
	assert.ErrorIs(t, err, boom)
	assert.Contains(t, err.Error(), "theme.Templates.Prompt.Hint")
	assert.True(t, out == nil)

	for _, s := range []*compiledSlot{c.templates.Prompt.Feedback, c.templates.Prompt.Answer} {
		out, err = render(s, ProfileTrueColor)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "want text and SGR only")
		assert.True(t, out == nil)
	}

	out, err = render(c.templates.Choice.Empty, ProfileTrueColor)
	assert.NoError(t, err)
	assert.True(t, out == nil)
}

func TestBuiltinPromptSlots(t *testing.T) {
	c, err := compileTemplates(t, func(th *Theme) { th.Symbols = asciiSymbols })
	assert.NoError(t, err)
	render := func(s *compiledSlot, data any) string {
		t.Helper()
		b, err := s.bind(c.funcs(ProfilePlain, roleStyles{}))
		assert.NoError(t, err)
		out, err := b.render(data)
		assert.NoError(t, err)
		return out.String()
	}
	tt := c.templates
	assert.Equal(t, "? Name", render(tt.Input.Label, labelView{"Name"}))
	assert.Equal(t, "? Name", render(tt.Password.Label, labelView{"Name"}))
	assert.Equal(t, "+ Name · v", render(tt.Input.Answer, answerView{Label: "Name", Value: "v"}))
	assert.Equal(t, "! bad", render(tt.Number.Feedback, feedbackView{"bad"}))
	keys := hintView{{Text("up/down"), Text("adjust")}, {Text("enter"), Text("submit")}}
	assert.Equal(t, "up/down adjust · enter submit", render(tt.Number.Hint, keys))
	assert.Equal(t, "", render(tt.Number.Hint, hintView{}))
	// built-ins stay out of the editable configuration
	assert.Equal(t, Template(""), c.editable().Templates.Prompt.Label)
}

func TestBuiltinMultichoiceSlots(t *testing.T) {
	c, err := compileTemplates(t, func(th *Theme) { th.Symbols = asciiSymbols })
	assert.NoError(t, err)
	render := func(s *compiledSlot, data any) string {
		t.Helper()
		b, err := s.bind(c.funcs(ProfilePlain, roleStyles{}))
		assert.NoError(t, err)
		out, err := b.render(data)
		assert.NoError(t, err)
		return out.String()
	}
	tt := c.templates.Multichoice
	for _, tc := range []struct {
		row  multichoiceRow
		want string
	}{
		{multichoiceRow{Value: "v", Active: true, Selected: true}, "> x v"},
		{multichoiceRow{Value: "v", Active: true}, "> o v"},
		{multichoiceRow{Value: "v", Selected: true}, "  x v"},
		{multichoiceRow{Value: "v"}, "  o v"},
	} {
		assert.Equal(t, tc.want, render(tt.Row, tc.row))
	}
	assert.Equal(t, "a, b", render(tt.AnswerValue, collectionView{Values: []string{"a", "b"}, Count: 2}))
	assert.Equal(t, "none", render(tt.AnswerValue, collectionView{}))
	assert.Equal(t, "No matches", render(tt.Empty, emptyView{"zz"}))
	assert.Equal(t, "+ Pick · a, b", render(tt.Answer, answerView{Label: "Pick", Value: "a, b"}))
}

func TestMultichoiceRowAndAnswerValueFallBackToBuiltins(t *testing.T) {
	c, err := compileTemplates(t, func(th *Theme) {
		th.Templates.Multichoice.Label = "family"
		th.Templates.Multichoice.AnswerValue = Hidden
	})
	assert.NoError(t, err)
	builtin, err := builtinTemplates.compile(c.funcs(ProfilePlain, roleStyles{}), templates{})
	assert.NoError(t, err)
	tt := c.templates.Multichoice
	assert.Equal(t, builtin.Multichoice.Row.tmpl.Root.String(), tt.Row.tmpl.Root.String())
	assert.True(t, tt.AnswerValue.hidden)
	// a family edit does not reach the other families
	assert.Equal(t, "family", tt.Label.tmpl.Root.String())
	assert.True(t, c.templates.Dropdown.Label.tmpl.Root.String() != "family")
}

func TestColorLiteralsFailInstallation(t *testing.T) {
	for _, src := range []Template{
		`{{ fg "#2D9B5" .Label }}`,
		`{{ bg "teal" .Label }}`,
		`{{ fg "" .Label }}`,
		`{{ bold (bg "#FFF3C4" (fg "00ADD8" .Label)) }}`,
		`{{ if .Label }}{{ fg "#12345G" . }}{{ end }}`,
		`{{ range .Values }}{{ else }}{{ fg "#1234" "x" }}{{ end }}`,
		`{{ with .Label }}{{ . }}{{ else }}{{ bg "#FFFFFFFF" "x" }}{{ end }}`,
		`{{ define "x" }}{{ fg "rgb(0,0,0)" . }}{{ end }}{{ template "x" .Label }}`,
	} {
		_, err := compileTemplates(t, func(th *Theme) { th.Templates.Input.Label = src })
		if err == nil || !strings.Contains(err.Error(), "theme.Templates.Input.Label") ||
			!strings.Contains(err.Error(), `want "#RRGGBB" or "#RGB"`) {
			t.Errorf("%s: got %v", src, err)
		}
	}
	_, err := compileTemplates(t, func(th *Theme) { th.Templates.Input.Label = `{{ fg "#0AD" .Label }}` })
	assert.NoError(t, err)
	// dynamic colors, and text literals in the value position, are not checked
	_, err = compileTemplates(t, func(th *Theme) {
		th.Templates.Input.Label = `{{ fg .Color .Label }}{{ fg "#000" "not a color" }}{{ printf "%s" "teal" }}`
	})
	assert.NoError(t, err)
}

func TestColorLiteralErrorHasPosition(t *testing.T) {
	_, err := compileTemplates(t, func(th *Theme) { th.Templates.Prompt.Answer = "x\n{{ fg \"#12\" .Value }}" })
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "theme.Templates.Prompt.Answer: Prompt.Answer:2:")
	assert.Contains(t, err.Error(), ": fg: ")
}

func TestBuiltinSpinnerSlots(t *testing.T) {
	c, err := compileTemplates(t, func(th *Theme) { th.Symbols = asciiSymbols })
	assert.NoError(t, err)
	render := func(s *compiledSlot, data spinnerView) string {
		t.Helper()
		b, err := s.bind(c.funcs(ProfilePlain, roleStyles{}))
		assert.NoError(t, err)
		out, err := b.render(data)
		assert.NoError(t, err)
		return out.String()
	}
	tt := c.templates.Spinner
	assert.Equal(t, ".. job: m", render(tt.Running, spinnerView{Frame: "..", Prefix: "job", Message: "m"}))
	assert.Equal(t, ".. m", render(tt.Running, spinnerView{Frame: "..", Message: "m"}))
	assert.Equal(t, "- job: m", render(tt.Stopped, spinnerView{Frame: "..", Prefix: "job", Message: "m"}))
	assert.Equal(t, "! m", render(tt.Failed, spinnerView{Frame: "..", Message: "m"}))
}

func TestSpinnerSlotsRejectHidden(t *testing.T) {
	_, err := compileTemplates(t, func(th *Theme) { th.Templates.Spinner.Stopped = Hidden })
	assert.Contains(t, err.Error(), "theme.Templates.Spinner.Stopped: empty source is not allowed")
}

func TestSpinnerStyleValidation(t *testing.T) {
	c, err := Theme{}.compile()
	assert.NoError(t, err)
	assert.Equal(t, []string{"▘", "▀", "▝", "▐", "▗", "▄", "▖", "▌"}, c.source.Symbols.Spinner)
	assert.Equal(t, 100*time.Millisecond, c.source.Spinner.Interval)
	c, err = Theme{Symbols: asciiSymbols}.compile()
	assert.NoError(t, err)
	assert.Equal(t, []string{"|", "/", "-", `\`}, c.source.Symbols.Spinner)

	th := NewTheme()
	th.Spinner = SpinnerStyle{Interval: -time.Second}
	_, err = th.compile()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "theme.Spinner.Interval: -1s: must not be negative")

	for _, tc := range []struct {
		frame string
		want  string
	}{
		{"", "theme.Symbols.Spinner[1]: must not be empty"},
		{"a\n", "theme.Symbols.Spinner[1]"},
		{"\x1b[32ma", "theme.Symbols.Spinner[1]"},
		{"ab", "theme.Symbols.Spinner[1]: \"ab\": want one display column"},
		{"\u754c", "theme.Symbols.Spinner[1]: \"\u754c\": want one display column, got 2"},
		{"\u0301", "theme.Symbols.Spinner[1]"},
	} {
		th := NewTheme()
		th.Symbols.Spinner = []string{"a", tc.frame}
		_, err := th.compile()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), tc.want)
	}
}

func TestSpinnerFramesAreCopied(t *testing.T) {
	keepTheme(t)
	th := NewTheme()
	th.Symbols.Spinner = []string{"a", "b"}
	assert.NoError(t, defaultIO.installTheme(th))
	installed := defaultIO.currentTheme()
	th.Symbols.Spinner[0] = "x"
	got := installed.editable()
	got.Symbols.Spinner[1] = "y"
	assert.Equal(t, []string{"a", "b"}, installed.source.Symbols.Spinner)
	// the presets are not shared with editable themes
	NewTheme().Symbols.Spinner[0] = "?"
	assert.Equal(t, "▘", NewTheme().Symbols.Spinner[0])
	assert.Equal(t, "▘", unicodeSymbols.Spinner[0])
}

func TestBuiltinProgressSlots(t *testing.T) {
	c, err := compileTemplates(t, func(*Theme) {})
	assert.NoError(t, err)
	render := func(s *compiledSlot, data progressView) string {
		t.Helper()
		b, err := s.bind(c.funcs(ProfilePlain, roleStyles{}))
		assert.NoError(t, err)
		out, err := b.render(data)
		assert.NoError(t, err)
		return out.String()
	}
	tt := c.templates.Progress
	view := progressView{Label: "build", Total: 100, Percent: 7, Values: []string{"1.00/s", "3s remaining"}, Bar: "##"}
	assert.Equal(t, "build", render(tt.Label, view))
	assert.Equal(t, "1.00/s · 3s remaining", render(tt.Metrics, view))
	assert.Equal(t, "", render(tt.Metrics, progressView{}))
	// the stacked frame sets the label apart from a four-column percentage
	assert.Equal(t, "build  7%   ##", render(tt.Frame, view))
	assert.Equal(t, "100% ##", render(tt.Frame, progressView{Total: 1, Percent: 100, Bar: "##"}))
	// without a total, the completion is unknown rather than zero
	assert.Equal(t, "build  —    ##", render(tt.Frame, progressView{Label: "build", Bar: "##"}))

	hint := func(s *compiledSlot, data hintView) string {
		t.Helper()
		b, err := s.bind(c.funcs(ProfilePlain, roleStyles{}))
		assert.NoError(t, err)
		out, err := b.render(data)
		assert.NoError(t, err)
		return out.String()
	}
	pairs := hintView{
		{Left: Text("62 / 100"), Right: Text("MiB")},
		{Left: Text("900 KiB / 10 MiB")},
		{Left: Text("12s"), Right: Text("left")},
	}
	assert.Equal(t, "62 / 100 MiB · 900 KiB / 10 MiB · 12s left", hint(tt.Hint, pairs))
	assert.Equal(t, "", hint(tt.Hint, nil))
	// a prompt hint edit does not reach progress, and ASCII separates with a bar
	c, err = compileTemplates(t, func(th *Theme) {
		th.Templates.Prompt.Hint = Hidden
		th.Symbols = asciiSymbols
		th.Progress.Compact = true
	})
	assert.NoError(t, err)
	tt = c.templates.Progress
	assert.Equal(t, "62 / 100 MiB | 900 KiB / 10 MiB | 12s left", hint(tt.Hint, pairs))
	// the compact frame keeps one space after the label
	view.Metrics = "m"
	assert.Equal(t, "build 7%   ## m", render(tt.Frame, view))
	assert.Equal(t, "build ?    ## m", render(tt.Frame, progressView{Label: "build", Bar: "##", Metrics: "m"}))
}

func TestProgressFrameInsertsBarOnce(t *testing.T) {
	for _, frame := range []Template{
		`{{ .Bar }}`,
		`{{ .Label }} [{{- .Bar -}}] {{ with .Metrics }}{{ . }}{{ end }}`,
		`{{ define "m" }}{{ .Metrics }}{{ end }}{{ .Bar }}{{ template "m" . }}`,
	} {
		_, err := compileTemplates(t, func(th *Theme) { th.Templates.Progress.Frame = frame })
		assert.NoError(t, err)
	}
	for _, frame := range []Template{
		`{{ .Label }}`,
		`{{ .Bar }}{{ .Bar }}`,
		`{{ if .Label }}{{ .Bar }}{{ end }}`,
		`{{ with .Label }}{{ $.Bar }}{{ end }}`,
		`{{ printf "%s" .Bar }}`,
		`{{ .Bar | printf "%s" }}`,
		`{{ $b := .Bar }}{{ $b }}`,
		`{{ .Bar }}{{ len .Bar }}`,
		`{{ .Bar }}{{ if .Bar }}x{{ end }}`,
		`{{ .Bar }}{{ $.Bar }}`,
		`{{ .Bar }}{{ (.).Bar }}`,
		`{{ define "b" }}{{ .Bar }}{{ end }}{{ template "b" . }}`,
		`{{ define "b" }}{{ .Bar }}{{ end }}`,
	} {
		_, err := compileTemplates(t, func(th *Theme) { th.Templates.Progress.Frame = frame })
		assert.Error(t, err)
		assert.Contains(
			t,
			err.Error(),
			"theme.Templates.Progress.Frame: want one top-level {{ .Bar }} and no other use of .Bar",
		)
	}
	_, err := compileTemplates(t, func(th *Theme) { th.Templates.Progress.Frame = Hidden })
	assert.Contains(t, err.Error(), "theme.Templates.Progress.Frame: empty source is not allowed")
	// label and metrics may be hidden, and other slots stay unchecked
	c, err := compileTemplates(t, func(th *Theme) {
		th.Templates.Progress.Label = Hidden
		th.Templates.Progress.Metrics = `{{ .Bar }}{{ .Bar }}`
	})
	assert.NoError(t, err)
	assert.True(t, c.templates.Progress.Label.hidden)
}

func TestProgressStyleValidation(t *testing.T) {
	// a theme built by hand stacks the metrics below the bar, like the preset
	c, err := Theme{}.compile()
	assert.NoError(t, err)
	assert.Equal(t, ProgressStyle{MinBarWidth: 1}, c.source.Progress)
	c, err = NewTheme().compile()
	assert.NoError(t, err)
	assert.Equal(t, progressStyle, c.source.Progress)
	th := NewTheme()
	th.Progress = ProgressStyle{MaxBarWidth: 3}
	th.Symbols.BarFill, th.Symbols.BarHead = "#", ""
	c, err = th.compile()
	assert.NoError(t, err)
	assert.Equal(t, ProgressStyle{MinBarWidth: 1, MaxBarWidth: 3}, c.source.Progress)
	assert.Equal(t, "#", c.source.Symbols.BarFill)
	assert.Equal(t, "╸", c.source.Symbols.BarHead)
	for _, tc := range []struct {
		style ProgressStyle
		want  string
	}{
		{ProgressStyle{MinBarWidth: -1}, "theme.Progress.MinBarWidth: -1: must not be negative"},
		{ProgressStyle{MaxBarWidth: -1}, "theme.Progress.MaxBarWidth: -1: must not be negative"},
		{ProgressStyle{MinBarWidth: 5, MaxBarWidth: 4}, "theme.Progress.MaxBarWidth: 4: below MinBarWidth 5"},
	} {
		th := NewTheme()
		th.Progress = tc.style
		_, err := th.compile()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), tc.want)
	}
}

func TestBarSymbolsValidation(t *testing.T) {
	for _, tc := range []struct {
		edit func(*Symbols)
		want string
	}{
		{func(s *Symbols) { s.BarFill = "##" }, `theme.Symbols.BarFill: "##": want one display column, got 2`},
		{func(s *Symbols) { s.BarHead = "Ａ" }, `theme.Symbols.BarHead: "Ａ": want one display column, got 2`},
		{func(s *Symbols) { s.BarEmpty = "\t" }, `theme.Symbols.BarEmpty: "\t": control character`},
		{func(s *Symbols) { s.Unknown = "??" }, `theme.Symbols.Unknown: "??": want one display column, got 2`},
		{func(s *Symbols) { s.Separator = "\n" }, `theme.Symbols.Separator: "\n": control character`},
	} {
		th := NewTheme()
		tc.edit(&th.Symbols)
		_, err := th.compile()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), tc.want)
	}
	for name, want := range map[string]string{
		"BarFill": "=", "BarHead": ">", "BarEmpty": "-", "Unknown": "?", "Separator": "|",
	} {
		got, err := asciiSymbols.named(name)
		assert.NoError(t, err)
		assert.Equal(t, want, got)
	}
}

// cyan is a foreground the table slot tests use as the text role.
const cyan = "\x1b[36m"

func TestBuiltinTableSlots(t *testing.T) {
	c, err := compileTemplates(t, func(*Theme) {})
	assert.NoError(t, err)
	render := func(p Profile, s *compiledSlot, data any) string {
		t.Helper()
		roles := roleStyles{foreground: Text(cyan)}
		if p < ProfileANSI16 {
			roles = roleStyles{}
		}
		b, err := s.bind(c.funcs(p, roles))
		assert.NoError(t, err)
		out, err := b.render(data)
		assert.NoError(t, err)
		return out.String()
	}
	tt := c.templates.Table
	column := columnView{Header: "NAME", Name: "Name", Kind: "string"}
	assert.Equal(t, "NAME", render(ProfilePlain, tt.Header, column))
	assert.Equal(t, Bold+"NAME"+Reset, render(ProfileAttrs, tt.Header, column))
	assert.Equal(t, Bold+cyan+"NAME"+FgDefault+Reset, render(ProfileTrueColor, tt.Header, column))
	assert.Equal(t, "x", render(ProfilePlain, tt.Cell, cellView{columnView: column, Value: "x"}))
	assert.Equal(t, cyan+"x"+FgDefault, render(ProfileTrueColor, tt.Cell, cellView{columnView: column, Value: "x"}))
}

func TestTableSlotsHiding(t *testing.T) {
	c, err := compileTemplates(t, func(th *Theme) { th.Templates.Table.Header = Hidden })
	assert.NoError(t, err)
	assert.True(t, c.templates.Table.Header.hidden)
	_, err = compileTemplates(t, func(th *Theme) { th.Templates.Table.Cell = Hidden })
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "theme.Templates.Table.Cell: empty source is not allowed")
	_, err = compileTemplates(t, func(th *Theme) { th.Templates.Table.Cell = `{{ fg "#12" .Value }}` })
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "theme.Templates.Table.Cell: ")
}

func TestTableStyleValidation(t *testing.T) {
	c, err := Theme{}.compile()
	assert.NoError(t, err)
	assert.Equal(t, tableStyle, c.source.Table)
	th := NewTheme()
	th.Table = TableStyle{MinColumnWidth: 2}
	c, err = th.compile()
	assert.NoError(t, err)
	assert.Equal(t, TableStyle{ColumnGap: 2, MinColumnWidth: 2}, c.source.Table)
	for _, tc := range []struct {
		style TableStyle
		want  string
	}{
		{TableStyle{ColumnGap: -1}, "theme.Table.ColumnGap: -1: must not be negative"},
		{TableStyle{MinColumnWidth: -2}, "theme.Table.MinColumnWidth: -2: must not be negative"},
	} {
		th := NewTheme()
		th.Table = tc.style
		_, err := th.compile()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), tc.want)
	}
}
