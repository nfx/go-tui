// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package render

import (
	"errors"
	"github.com/nfx/go-tui/internal/ansi"
	"github.com/nfx/go-tui/internal/color"
	"github.com/nfx/go-tui/theme"
	"strings"
	"testing"
	"text/template"
	"time"

	"github.com/nfx/go-tui/internal/assert"
)

// compileTemplates compiles the built-in theme with test-specific edits.
func compileTemplates(t *testing.T, edit func(*theme.Theme)) (*Compiled, error) {
	t.Helper()
	th := theme.New()
	edit(&th)
	return Compile(th)
}

func TestTemplateZeroInheritsAndEmptyHides(t *testing.T) {
	c, err := compileTemplates(t, func(th *theme.Theme) {
		th.Templates.Prompt.Label = theme.Template("{{ bold .Label }}")
		th.Templates.Dropdown.Hint = theme.Hidden
	})
	if err != nil {
		t.Fatal(err)
	}
	tt := c.Templates
	for p, s := range map[string]*Slot{
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
	c, err := compileTemplates(t, func(th *theme.Theme) {
		th.Templates.Prompt.Label = theme.Template("shared")
		th.Templates.Input.Label = theme.Template("input")
	})
	if err != nil {
		t.Fatal(err)
	}
	tt := c.Templates
	for _, tc := range []struct {
		path string
		slot *Slot
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
		_, err := compileTemplates(t, func(th *theme.Theme) {
			switch path {
			case "Choice.Active":
				th.Templates.Choice.Active = theme.Hidden
			case "Choice.Inactive":
				th.Templates.Choice.Inactive = theme.Hidden
			case "Choice.Value":
				th.Templates.Choice.Value = theme.Hidden
			default:
				th.Templates.Multichoice.Row = theme.Hidden
			}
		})
		if err == nil || !strings.Contains(err.Error(), path) {
			t.Errorf("%s: want path-qualified error, got %v", path, err)
		}
	}
}

func TestTemplateErrorNamesDefiningSlot(t *testing.T) {
	_, err := compileTemplates(t, func(th *theme.Theme) {
		th.Templates.Prompt.Answer = theme.Template("{{ .Value ")
	})
	if err == nil || !strings.Contains(err.Error(), "theme.Templates.Prompt.Answer") {
		t.Fatalf("got %v", err)
	}
	_, err = compileTemplates(t, func(th *theme.Theme) {
		th.Templates.Input.Label = theme.Template("{{ nosuchhelper .Label }}")
	})
	if err == nil || !strings.Contains(err.Error(), "theme.Templates.Input.Label") {
		t.Fatalf("unknown helper: got %v", err)
	}
}

func TestTemplateDefineIsIsolatedPerSlot(t *testing.T) {
	c, err := compileTemplates(t, func(th *theme.Theme) {
		th.Templates.Prompt.Label = theme.Template(`{{ define "x" }}A{{ end }}{{ template "x" }}`)
		th.Templates.Prompt.Hint = theme.Template(`{{ define "x" }}B{{ end }}{{ template "x" }}`)
	})
	if err != nil {
		t.Fatal(err)
	}
	exec := func(s *Slot) string {
		var b strings.Builder
		if err := s.tmpl.Execute(&b, nil); err != nil {
			t.Fatal(err)
		}
		return b.String()
	}
	if a, b := exec(c.Templates.Prompt.Label), exec(c.Templates.Prompt.Hint); a != "A" || b != "B" {
		t.Fatalf("define leaked between slots: %q %q", a, b)
	}
}

func TestBoundSlotRendersOrFailsWhole(t *testing.T) {
	boom := errors.New("boom")
	c, err := compileTemplates(t, func(th *theme.Theme) {
		th.Funcs = template.FuncMap{"fails": func(string) (string, error) { return "", boom }}
		th.Templates.Prompt.Label = `{{ bold .Label }}`
		th.Templates.Prompt.Hint = `{{ bold "partial" }}{{ fails .Label }}`
		th.Templates.Prompt.Feedback = "{{ .Label }}\n"
		th.Templates.Prompt.Answer = "{{ .Label }}\x1b[2K"
		th.Templates.Choice.Empty = theme.Hidden
	})
	assert.NoError(t, err)
	data := struct{ Label string }{"x"}
	render := func(s *Slot, p color.Profile) (ansi.Text, error) {
		b, err := s.Bind(c.Funcs(p, Roles{}))
		assert.NoError(t, err)
		return b.Render(data)
	}

	out, err := render(c.Templates.Prompt.Label, color.ProfileTrueColor)
	assert.NoError(t, err)
	assert.Equal(t, "\x1b[1mx\x1b[0m", out.String())
	out, err = render(c.Templates.Prompt.Label, color.ProfilePlain)
	assert.NoError(t, err)
	assert.Equal(t, "x", out.String()) // binding does not change the installed slot

	out, err = render(c.Templates.Prompt.Hint, color.ProfileTrueColor)
	assert.ErrorIs(t, err, boom)
	assert.Contains(t, err.Error(), "theme.Templates.Prompt.Hint")
	assert.True(t, out == nil)

	for _, s := range []*Slot{c.Templates.Prompt.Feedback, c.Templates.Prompt.Answer} {
		out, err = render(s, color.ProfileTrueColor)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "want text and SGR only")
		assert.True(t, out == nil)
	}

	out, err = render(c.Templates.Choice.Empty, color.ProfileTrueColor)
	assert.NoError(t, err)
	assert.True(t, out == nil)
}

func TestBuiltinPromptSlots(t *testing.T) {
	c, err := compileTemplates(t, func(th *theme.Theme) { th.Symbols = theme.New().Symbols.ASCII() })
	assert.NoError(t, err)
	render := func(s *Slot, data any) string {
		t.Helper()
		b, err := s.Bind(c.Funcs(color.ProfilePlain, Roles{}))
		assert.NoError(t, err)
		out, err := b.Render(data)
		assert.NoError(t, err)
		return out.String()
	}
	tt := c.Templates
	assert.Equal(t, "? Name", render(tt.Input.Label, LabelView{"Name"}))
	assert.Equal(t, "? Name", render(tt.Password.Label, LabelView{"Name"}))
	assert.Equal(t, "+ Name · v", render(tt.Input.Answer, AnswerView{Label: "Name", Value: "v"}))
	assert.Equal(t, "! bad", render(tt.Number.Feedback, FeedbackView{"bad"}))
	keys := HintView{{ansi.Text("up/down"), ansi.Text("adjust")}, {ansi.Text("enter"), ansi.Text("submit")}}
	assert.Equal(t, "up/down adjust · enter submit", render(tt.Number.Hint, keys))
	assert.Equal(t, "", render(tt.Number.Hint, HintView{}))
	// built-ins stay out of the editable configuration
	assert.Equal(t, theme.Template(""), c.Editable().Templates.Prompt.Label)
}

func TestBuiltinMultichoiceSlots(t *testing.T) {
	c, err := compileTemplates(t, func(th *theme.Theme) { th.Symbols = theme.New().Symbols.ASCII() })
	assert.NoError(t, err)
	render := func(s *Slot, data any) string {
		t.Helper()
		b, err := s.Bind(c.Funcs(color.ProfilePlain, Roles{}))
		assert.NoError(t, err)
		out, err := b.Render(data)
		assert.NoError(t, err)
		return out.String()
	}
	tt := c.Templates.Multichoice
	for _, tc := range []struct {
		row  fakeMultichoiceRow
		want string
	}{
		{fakeMultichoiceRow{Value: "v", Active: true, Selected: true}, "> x v"},
		{fakeMultichoiceRow{Value: "v", Active: true}, "> o v"},
		{fakeMultichoiceRow{Value: "v", Selected: true}, "  x v"},
		{fakeMultichoiceRow{Value: "v"}, "  o v"},
	} {
		assert.Equal(t, tc.want, render(tt.Row, tc.row))
	}
	assert.Equal(t, "a, b", render(tt.AnswerValue, fakeCollectionView{Values: []string{"a", "b"}, Count: 2}))
	assert.Equal(t, "none", render(tt.AnswerValue, fakeCollectionView{}))
	assert.Equal(t, "No matches", render(tt.Empty, fakeEmptyView{"zz"}))
	assert.Equal(t, "+ Pick · a, b", render(tt.Answer, AnswerView{Label: "Pick", Value: "a, b"}))
}

func TestMultichoiceRowAndAnswerValueFallBackToBuiltins(t *testing.T) {
	c, err := compileTemplates(t, func(th *theme.Theme) {
		th.Templates.Multichoice.Label = "family"
		th.Templates.Multichoice.AnswerValue = theme.Hidden
	})
	assert.NoError(t, err)
	builtin, err := compileSet(&builtinTemplates, c.Funcs(color.ProfilePlain, Roles{}), templates{})
	assert.NoError(t, err)
	tt := c.Templates.Multichoice
	assert.Equal(t, builtin.Multichoice.Row.tmpl.Root.String(), tt.Row.tmpl.Root.String())
	assert.True(t, tt.AnswerValue.hidden)
	// a family edit does not reach the other families
	assert.Equal(t, "family", tt.Label.tmpl.Root.String())
	assert.True(t, c.Templates.Dropdown.Label.tmpl.Root.String() != "family")
}

func TestColorLiteralsFailInstallation(t *testing.T) {
	for _, src := range []theme.Template{
		`{{ fg "#2D9B5" .Label }}`,
		`{{ bg "teal" .Label }}`,
		`{{ fg "" .Label }}`,
		`{{ bold (bg "#FFF3C4" (fg "00ADD8" .Label)) }}`,
		`{{ if .Label }}{{ fg "#12345G" . }}{{ end }}`,
		`{{ range .Values }}{{ else }}{{ fg "#1234" "x" }}{{ end }}`,
		`{{ with .Label }}{{ . }}{{ else }}{{ bg "#FFFFFFFF" "x" }}{{ end }}`,
		`{{ define "x" }}{{ fg "rgb(0,0,0)" . }}{{ end }}{{ template "x" .Label }}`,
	} {
		_, err := compileTemplates(t, func(th *theme.Theme) { th.Templates.Input.Label = src })
		if err == nil || !strings.Contains(err.Error(), "theme.Templates.Input.Label") ||
			!strings.Contains(err.Error(), `want "#RRGGBB" or "#RGB"`) {
			t.Errorf("%s: got %v", src, err)
		}
	}
	_, err := compileTemplates(t, func(th *theme.Theme) { th.Templates.Input.Label = `{{ fg "#0AD" .Label }}` })
	assert.NoError(t, err)
	// dynamic colors, and text literals in the value position, are not checked
	_, err = compileTemplates(t, func(th *theme.Theme) {
		th.Templates.Input.Label = `{{ fg .Color .Label }}{{ fg "#000" "not a color" }}{{ printf "%s" "teal" }}`
	})
	assert.NoError(t, err)
}

func TestColorLiteralErrorHasPosition(t *testing.T) {
	_, err := compileTemplates(t, func(th *theme.Theme) { th.Templates.Prompt.Answer = "x\n{{ fg \"#12\" .Value }}" })
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "theme.Templates.Prompt.Answer: Prompt.Answer:2:")
	assert.Contains(t, err.Error(), ": fg: ")
}

func TestBuiltinSpinnerSlots(t *testing.T) {
	c, err := compileTemplates(t, func(th *theme.Theme) { th.Symbols = theme.New().Symbols.ASCII() })
	assert.NoError(t, err)
	render := func(s *Slot, data fakeSpinnerView) string {
		t.Helper()
		b, err := s.Bind(c.Funcs(color.ProfilePlain, Roles{}))
		assert.NoError(t, err)
		out, err := b.Render(data)
		assert.NoError(t, err)
		return out.String()
	}
	tt := c.Templates.Spinner
	assert.Equal(t, ".. job: m", render(tt.Running, fakeSpinnerView{Frame: "..", Prefix: "job", Message: "m"}))
	assert.Equal(t, ".. m", render(tt.Running, fakeSpinnerView{Frame: "..", Message: "m"}))
	assert.Equal(t, "- job: m", render(tt.Stopped, fakeSpinnerView{Frame: "..", Prefix: "job", Message: "m"}))
	assert.Equal(t, "! m", render(tt.Failed, fakeSpinnerView{Frame: "..", Message: "m"}))
}

func TestSpinnerSlotsRejectHidden(t *testing.T) {
	_, err := compileTemplates(t, func(th *theme.Theme) { th.Templates.Spinner.Stopped = theme.Hidden })
	assert.Contains(t, err.Error(), "theme.Templates.Spinner.Stopped: empty source is not allowed")
}

func TestSpinnerStyleValidation(t *testing.T) {
	c, err := Compile(theme.Theme{})
	assert.NoError(t, err)
	assert.Equal(t, []string{"▘", "▀", "▝", "▐", "▗", "▄", "▖", "▌"}, c.Source.Symbols.Spinner)
	assert.Equal(t, 100*time.Millisecond, c.Source.Spinner.Interval)
	c, err = Compile(theme.Theme{Symbols: theme.New().Symbols.ASCII()})
	assert.NoError(t, err)
	assert.Equal(t, []string{"|", "/", "-", `\`}, c.Source.Symbols.Spinner)

	th := theme.New()
	th.Spinner = theme.SpinnerStyle{Interval: -time.Second}
	_, err = Compile(th)
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
		th := theme.New()
		th.Symbols.Spinner = []string{"a", tc.frame}
		_, err := Compile(th)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), tc.want)
	}
}

func TestBuiltinProgressSlots(t *testing.T) {
	c, err := compileTemplates(t, func(*theme.Theme) {})
	assert.NoError(t, err)
	render := func(s *Slot, data fakeProgressView) string {
		t.Helper()
		b, err := s.Bind(c.Funcs(color.ProfilePlain, Roles{}))
		assert.NoError(t, err)
		out, err := b.Render(data)
		assert.NoError(t, err)
		return out.String()
	}
	tt := c.Templates.Progress
	view := fakeProgressView{
		Label:   "build",
		Total:   100,
		Percent: 7,
		Values:  []string{"1.00/s", "3s remaining"},
		Bar:     "##",
	}
	assert.Equal(t, "build", render(tt.Label, view))
	assert.Equal(t, "1.00/s · 3s remaining", render(tt.Metrics, view))
	assert.Equal(t, "", render(tt.Metrics, fakeProgressView{}))
	// the stacked frame sets the label apart from a four-column percentage
	assert.Equal(t, "build  7%   ##", render(tt.Frame, view))
	assert.Equal(t, "100% ##", render(tt.Frame, fakeProgressView{Total: 1, Percent: 100, Bar: "##"}))
	// without a total, the completion is unknown rather than zero
	assert.Equal(t, "build  —    ##", render(tt.Frame, fakeProgressView{Label: "build", Bar: "##"}))

	hint := func(s *Slot, data HintView) string {
		t.Helper()
		b, err := s.Bind(c.Funcs(color.ProfilePlain, Roles{}))
		assert.NoError(t, err)
		out, err := b.Render(data)
		assert.NoError(t, err)
		return out.String()
	}
	pairs := HintView{
		{Left: ansi.Text("62 / 100"), Right: ansi.Text("MiB")},
		{Left: ansi.Text("900 KiB / 10 MiB")},
		{Left: ansi.Text("12s"), Right: ansi.Text("left")},
	}
	assert.Equal(t, "62 / 100 MiB · 900 KiB / 10 MiB · 12s left", hint(tt.Hint, pairs))
	assert.Equal(t, "", hint(tt.Hint, nil))
	// a prompt hint edit does not reach progress, and ASCII separates with a bar
	c, err = compileTemplates(t, func(th *theme.Theme) {
		th.Templates.Prompt.Hint = theme.Hidden
		th.Symbols = theme.New().Symbols.ASCII()
		th.Progress.Compact = true
	})
	assert.NoError(t, err)
	tt = c.Templates.Progress
	assert.Equal(t, "62 / 100 MiB | 900 KiB / 10 MiB | 12s left", hint(tt.Hint, pairs))
	// the compact frame keeps one space after the label
	view.Metrics = "m"
	assert.Equal(t, "build 7%   ## m", render(tt.Frame, view))
	assert.Equal(t, "build ?    ## m", render(tt.Frame, fakeProgressView{Label: "build", Bar: "##", Metrics: "m"}))
}

func TestProgressFrameInsertsBarOnce(t *testing.T) {
	for _, frame := range []theme.Template{
		`{{ .Bar }}`,
		`{{ .Label }} [{{- .Bar -}}] {{ with .Metrics }}{{ . }}{{ end }}`,
		`{{ define "m" }}{{ .Metrics }}{{ end }}{{ .Bar }}{{ template "m" . }}`,
	} {
		_, err := compileTemplates(t, func(th *theme.Theme) { th.Templates.Progress.Frame = frame })
		assert.NoError(t, err)
	}
	for _, frame := range []theme.Template{
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
		_, err := compileTemplates(t, func(th *theme.Theme) { th.Templates.Progress.Frame = frame })
		assert.Error(t, err)
		assert.Contains(
			t,
			err.Error(),
			"theme.Templates.Progress.Frame: want one top-level {{ .Bar }} and no other use of .Bar",
		)
	}
	_, err := compileTemplates(t, func(th *theme.Theme) { th.Templates.Progress.Frame = theme.Hidden })
	assert.Contains(t, err.Error(), "theme.Templates.Progress.Frame: empty source is not allowed")
	// label and metrics may be hidden, and other slots stay unchecked
	c, err := compileTemplates(t, func(th *theme.Theme) {
		th.Templates.Progress.Label = theme.Hidden
		th.Templates.Progress.Metrics = `{{ .Bar }}{{ .Bar }}`
	})
	assert.NoError(t, err)
	assert.True(t, c.Templates.Progress.Label.hidden)
}

func TestProgressStyleValidation(t *testing.T) {
	// a theme built by hand stacks the metrics below the bar, like the preset
	c, err := Compile(theme.Theme{})
	assert.NoError(t, err)
	assert.Equal(t, theme.ProgressStyle{MinBarWidth: 1}, c.Source.Progress)
	c, err = Compile(theme.New())
	assert.NoError(t, err)
	assert.Equal(t, theme.New().Progress, c.Source.Progress)
	th := theme.New()
	th.Progress = theme.ProgressStyle{MaxBarWidth: 3}
	th.Symbols.BarFill, th.Symbols.BarHead = "#", ""
	c, err = Compile(th)
	assert.NoError(t, err)
	assert.Equal(t, theme.ProgressStyle{MinBarWidth: 1, MaxBarWidth: 3}, c.Source.Progress)
	assert.Equal(t, "#", c.Source.Symbols.BarFill)
	assert.Equal(t, "╸", c.Source.Symbols.BarHead)
	for _, tc := range []struct {
		style theme.ProgressStyle
		want  string
	}{
		{theme.ProgressStyle{MinBarWidth: -1}, "theme.Progress.MinBarWidth: -1: must not be negative"},
		{theme.ProgressStyle{MaxBarWidth: -1}, "theme.Progress.MaxBarWidth: -1: must not be negative"},
		{theme.ProgressStyle{MinBarWidth: 5, MaxBarWidth: 4}, "theme.Progress.MaxBarWidth: 4: below MinBarWidth 5"},
	} {
		th := theme.New()
		th.Progress = tc.style
		_, err := Compile(th)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), tc.want)
	}
}

func TestBarSymbolsValidation(t *testing.T) {
	for _, tc := range []struct {
		edit func(*theme.Symbols)
		want string
	}{
		{func(s *theme.Symbols) { s.BarFill = "##" }, `theme.Symbols.BarFill: "##": want one display column, got 2`},
		{func(s *theme.Symbols) { s.BarHead = "Ａ" }, `theme.Symbols.BarHead: "Ａ": want one display column, got 2`},
		{func(s *theme.Symbols) { s.BarEmpty = "\t" }, `theme.Symbols.BarEmpty: "\t": control character`},
		{func(s *theme.Symbols) { s.Unknown = "??" }, `theme.Symbols.Unknown: "??": want one display column, got 2`},
		{func(s *theme.Symbols) { s.Separator = "\n" }, `theme.Symbols.Separator: "\n": control character`},
	} {
		th := theme.New()
		tc.edit(&th.Symbols)
		_, err := Compile(th)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), tc.want)
	}
	for name, want := range map[string]string{
		"BarFill": "=", "BarHead": ">", "BarEmpty": "-", "Unknown": "?", "Separator": "|",
	} {
		got, err := theme.New().Symbols.ASCII().Named(name)
		assert.NoError(t, err)
		assert.Equal(t, want, got)
	}
}

// cyan is a foreground the table slot tests use as the text role.
const cyan = "\x1b[36m"

func TestBuiltinTableSlots(t *testing.T) {
	c, err := compileTemplates(t, func(*theme.Theme) {})
	assert.NoError(t, err)
	render := func(p color.Profile, s *Slot, data any) string {
		t.Helper()
		roles := Roles{Foreground: ansi.Text(cyan)}
		if p < color.ProfileANSI16 {
			roles = Roles{}
		}
		b, err := s.Bind(c.Funcs(p, roles))
		assert.NoError(t, err)
		out, err := b.Render(data)
		assert.NoError(t, err)
		return out.String()
	}
	tt := c.Templates.Table
	column := fakeColumnView{Header: "NAME", Name: "Name", Kind: "string"}
	assert.Equal(t, "NAME", render(color.ProfilePlain, tt.Header, column))
	assert.Equal(t, ansi.Bold+"NAME"+ansi.Reset, render(color.ProfileAttrs, tt.Header, column))
	assert.Equal(t, ansi.Bold+cyan+"NAME"+color.FgDefault+ansi.Reset, render(color.ProfileTrueColor, tt.Header, column))
	assert.Equal(t, "x", render(color.ProfilePlain, tt.Cell, fakeCellView{fakeColumnView: column, Value: "x"}))
	assert.Equal(
		t,
		cyan+"x"+color.FgDefault,
		render(color.ProfileTrueColor, tt.Cell, fakeCellView{fakeColumnView: column, Value: "x"}),
	)
}

func TestTableSlotsHiding(t *testing.T) {
	c, err := compileTemplates(t, func(th *theme.Theme) { th.Templates.Table.Header = theme.Hidden })
	assert.NoError(t, err)
	assert.True(t, c.Templates.Table.Header.hidden)
	_, err = compileTemplates(t, func(th *theme.Theme) { th.Templates.Table.Cell = theme.Hidden })
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "theme.Templates.Table.Cell: empty source is not allowed")
	_, err = compileTemplates(t, func(th *theme.Theme) { th.Templates.Table.Cell = `{{ fg "#12" .Value }}` })
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "theme.Templates.Table.Cell: ")
}

func TestTableStyleValidation(t *testing.T) {
	c, err := Compile(theme.Theme{})
	assert.NoError(t, err)
	assert.Equal(t, theme.New().Table, c.Source.Table)
	th := theme.New()
	th.Table = theme.TableStyle{MinColumnWidth: 2}
	c, err = Compile(th)
	assert.NoError(t, err)
	assert.Equal(t, theme.TableStyle{ColumnGap: 2, MinColumnWidth: 2}, c.Source.Table)
	for _, tc := range []struct {
		style theme.TableStyle
		want  string
	}{
		{theme.TableStyle{ColumnGap: -1}, "theme.Table.ColumnGap: -1: must not be negative"},
		{theme.TableStyle{MinColumnWidth: -2}, "theme.Table.MinColumnWidth: -2: must not be negative"},
	} {
		th := theme.New()
		th.Table = tc.style
		_, err := Compile(th)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), tc.want)
	}
}
