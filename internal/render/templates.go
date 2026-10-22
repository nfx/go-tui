// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package render

import (
	"errors"
	"fmt"
	"github.com/nfx/go-tui/theme"
	"text/template"
	"text/template/parse"
)

// templated is the input of one compile step: the source slots Src, named
// Name, inheriting unset slots from the compiled Parent.
type templated[A, B any] struct {
	Funcs  template.FuncMap
	Name   string
	Src    A
	Parent B
}

// nest preserves helpers and qualifies nested slots with nonempty field names.
func (in *templated[A, B]) nest[C, D any](field string, src C, parent D) templated[C, D] {
	name := in.Name
	switch {
	case name == "":
		name = field
	case field != "":
		name += "." + field
	}
	return templated[C, D]{Funcs: in.Funcs, Name: name, Src: src, Parent: parent}
}

// templates are the compiled [Templates], with inheritance resolved.
type templates struct {
	Prompt PromptSlots
	Choice choiceTemplates

	Input    PromptSlots
	Password secretTemplates
	Number   secretTemplates

	Dropdown    ChoiceFamily
	Multichoice multichoiceTemplates
	Confirm     ChoiceFamily
	FilePicker  ChoiceFamily

	Spinner  SpinnerSlots
	Progress progressTemplates
	Table    tableTemplates
}

// builtinTemplates are the built-in sources of the root slots, shared by
// Guided and Inline: only geometry differs. Family slots stay unset, so that
// they inherit, and a root slot without a source has no built-in yet.
var builtinTemplates = theme.Templates{
	Prompt: theme.PromptTemplates{
		Label:  `{{ accent (symbol "Prompt") }} {{ bold (text .Label) }}`,
		Answer: `{{ success (symbol "Success") }} {{ text .Label }} {{ muted "·" }} {{ text .Value }}`,
		Hint: `{{ range $i, $h := . }}{{ if $i }}{{ guide " · " }}{{ end }}` +
			`{{ accent $h.Left }} {{ guide $h.Right }}{{ end }}`,
		Feedback: `{{ error (symbol "Error") }} {{ error .Message }}`,
	},
	Choice: theme.ChoiceTemplates{
		Value:    `{{ label . }}`,
		Details:  theme.Hidden,
		Active:   `{{ bold (accent .Value) }}{{ with .Details }} {{ muted . }}{{ end }}`,
		Inactive: `{{ text .Value }}`,
		Empty:    `{{ muted "No matches" }}`,
	},
	Multichoice: theme.MultichoiceTemplates{
		Row: `{{ if .Active }}{{ accent (symbol "Focus") }}{{ else }} {{ end }} ` +
			`{{ if .Selected }}{{ accent (symbol "Checked") }}{{ else }}{{ muted (symbol "Unchecked") }}{{ end }} {{ .Value }}`,
		AnswerValue: `{{ range $i, $v := .Values }}{{ if $i }}, {{ end }}{{ $v }}{{ else }}none{{ end }}`,
	},
	// FilePicker.Value names a [fileView]: directories end with a slash, the
	// way up is muted. It applies unless a theme sets Choice.Value.
	FilePicker: theme.SelectTemplates{ChoiceTemplates: theme.ChoiceTemplates{
		Value: `{{ if .Parent }}{{ muted .Name }}{{ else }}{{ .Name }}{{ if .IsDir }}/{{ end }}{{ end }}`,
	}},
	// Spinner rows name a [spinnerView]. Only a running spinner animates; a
	// stopped one is neutral, since closing it says nothing about success.
	Spinner: theme.SpinnerTemplates{
		Running: `{{ accent .Frame }} {{ with .Prefix }}{{ text . }}{{ muted ":" }} {{ end }}{{ text .Message }}`,
		Stopped: `{{ muted (symbol "Cancel") }} {{ with .Prefix }}{{ text . }}{{ muted ":" }} {{ end }}{{ text .Message }}`,
		Failed:  `{{ error (symbol "Error") }} {{ with .Prefix }}{{ text . }}{{ muted ":" }} {{ end }}{{ text .Message }}`,
	},
	// Progress slots name a [progressView], except Hint, which names the
	// [hintView] of its metrics. The renderer draws the bar in the accent
	// role and its empty track in the guide role. This Frame is the one of
	// [ProgressStyle.Compact] bars; see [ProgressStyle.builtins].
	Progress: theme.ProgressTemplates{
		Label:   `{{ text .Label }}`,
		Metrics: `{{ range $i, $v := .Values }}{{ if $i }}{{ muted " · " }}{{ end }}{{ muted $v }}{{ end }}`,
		Hint: `{{ range $i, $h := . }}{{ if $i }}{{ guide (printf " %s " (symbol "Separator")) }}{{ end }}` +
			`{{ accent $h.Left }}{{ with $h.Right }} {{ guide . }}{{ end }}{{ end }}`,
		Frame: `{{ with .Label }}{{ . }} {{ end }}` + progressPercent + ` {{ .Bar }}{{ with .Metrics }} {{ . }}{{ end }}`,
	},
	// Table slots name a [columnView] and a [cellView]. Weight sets the
	// header apart; values stay in the foreground unless a caller colors them.
	Table: theme.TableTemplates{
		Header: `{{ bold (text .Header) }}`,
		Cell:   `{{ text .Value }}`,
	},
}

// compileSet resolves isolated slot inheritance over base and reports errors at defining slots.
func compileSet(t *theme.Templates, funcs template.FuncMap, base templates) (templates, error) {
	root := templated[struct{}, struct{}]{Funcs: funcs}
	var out templates
	var errs [12]error
	out.Prompt, errs[0] = compilePrompt(root.nest("Prompt", t.Prompt, base.Prompt))
	out.Input, errs[1] = compilePrompt(root.nest("Input", t.Input, out.Prompt))
	out.Password, errs[2] = compileSecret(root.nest("Password", t.Password, out.Input))
	out.Number, errs[3] = compileSecret(root.nest("Number", t.Number, out.Input))
	out.Choice, errs[4] = compileChoice(root.nest("Choice", t.Choice, base.Choice))
	family := ChoiceFamily{out.Prompt, out.Choice}
	out.Dropdown, errs[5] = compileChoiceFamily(root.nest("Dropdown", t.Dropdown, family))
	out.Multichoice, errs[6] = compileMultichoice(root.nest("Multichoice", t.Multichoice, multichoiceTemplates{
		ChoiceFamily: family,
		Row:          base.Multichoice.Row,
		AnswerValue:  base.Multichoice.AnswerValue,
	}))
	out.Confirm, errs[7] = compileChoiceFamily(root.nest("Confirm", t.Confirm, family))
	picker := family
	if t.Choice.Value == "" {
		// a shared projection reaches the picker, otherwise its own built-in
		picker.Value = base.FilePicker.Value
	}
	out.FilePicker, errs[8] = compileChoiceFamily(root.nest("FilePicker", t.FilePicker, picker))
	out.Spinner, errs[9] = compileSpinner(root.nest("Spinner", t.Spinner, base.Spinner))
	out.Progress, errs[10] = compileProgress(root.nest("Progress", t.Progress, base.Progress))
	out.Table, errs[11] = compileTable(root.nest("Table", t.Table, base.Table))
	if err := errors.Join(errs[:]...); err != nil {
		return templates{}, err
	}
	return out, nil
}

// PromptSlots are the compiled [PromptTemplates].
type PromptSlots struct{ Label, Answer, Hint, Feedback *Slot }

// compilePrompt compiles prompt slots, inheriting unset slots from the parent.
func compilePrompt(in templated[theme.PromptTemplates, PromptSlots]) (out PromptSlots, _ error) {
	var errs [4]error
	out.Label, errs[0] = compileSlot(in.nest("Label", in.Src.Label, in.Parent.Label), true)
	out.Answer, errs[1] = compileSlot(in.nest("Answer", in.Src.Answer, in.Parent.Answer), true)
	out.Hint, errs[2] = compileSlot(in.nest("Hint", in.Src.Hint, in.Parent.Hint), true)
	out.Feedback, errs[3] = compileSlot(in.nest("Feedback", in.Src.Feedback, in.Parent.Feedback), true)
	return out, errors.Join(errs[:]...)
}

// secretTemplates are the compiled [SecretTemplates].
type secretTemplates struct{ Label, Hint, Feedback *Slot }

// compileSecret compiles prompt slots without an answer slot, inheriting unset slots.
func compileSecret(in templated[theme.SecretTemplates, PromptSlots]) (out secretTemplates, _ error) {
	var errs [3]error
	out.Label, errs[0] = compileSlot(in.nest("Label", in.Src.Label, in.Parent.Label), true)
	out.Hint, errs[1] = compileSlot(in.nest("Hint", in.Src.Hint, in.Parent.Hint), true)
	out.Feedback, errs[2] = compileSlot(in.nest("Feedback", in.Src.Feedback, in.Parent.Feedback), true)
	return out, errors.Join(errs[:]...)
}

// choiceTemplates are the compiled [ChoiceTemplates].
type choiceTemplates struct{ Value, Details, Active, Inactive, Empty *Slot }

// compileChoice compiles inherited choice slots, rejecting Hidden for Value, Active, and Inactive.
func compileChoice(in templated[theme.ChoiceTemplates, choiceTemplates]) (out choiceTemplates, _ error) {
	var errs [5]error
	out.Value, errs[0] = compileSlot(in.nest("Value", in.Src.Value, in.Parent.Value), false)
	out.Details, errs[1] = compileSlot(in.nest("Details", in.Src.Details, in.Parent.Details), true)
	out.Active, errs[2] = compileSlot(in.nest("Active", in.Src.Active, in.Parent.Active), false)
	out.Inactive, errs[3] = compileSlot(in.nest("Inactive", in.Src.Inactive, in.Parent.Inactive), false)
	out.Empty, errs[4] = compileSlot(in.nest("Empty", in.Src.Empty, in.Parent.Empty), true)
	return out, errors.Join(errs[:]...)
}

// ChoiceFamily are the compiled [SelectTemplates].
type ChoiceFamily struct {
	PromptSlots
	choiceTemplates
}

// compileChoiceFamily compiles family overrides with shared prompt and choice fallbacks.
func compileChoiceFamily(in templated[theme.SelectTemplates, ChoiceFamily]) (out ChoiceFamily, _ error) {
	var errs [2]error
	out.PromptSlots, errs[0] = compilePrompt(in.nest("", in.Src.PromptTemplates, in.Parent.PromptSlots))
	out.choiceTemplates, errs[1] = compileChoice(in.nest("", in.Src.ChoiceTemplates, in.Parent.choiceTemplates))
	return out, errors.Join(errs[:]...)
}

// multichoiceTemplates are the compiled [MultichoiceTemplates].
type multichoiceTemplates struct {
	ChoiceFamily
	Row, AnswerValue *Slot
}

// compileMultichoice compiles family slots with built-in fallbacks for Row and AnswerValue.
func compileMultichoice(in templated[theme.MultichoiceTemplates, multichoiceTemplates],
) (out multichoiceTemplates, _ error) {
	var errs [3]error
	out.ChoiceFamily, errs[0] = compileChoiceFamily(in.nest("", in.Src.SelectTemplates, in.Parent.ChoiceFamily))
	out.Row, errs[1] = compileSlot(in.nest("Row", in.Src.Row, in.Parent.Row), false)
	out.AnswerValue, errs[2] = compileSlot(in.nest("AnswerValue", in.Src.AnswerValue, in.Parent.AnswerValue), true)
	return out, errors.Join(errs[:]...)
}

// SpinnerSlots are the compiled [SpinnerTemplates].
type SpinnerSlots struct{ Running, Stopped, Failed *Slot }

// compileSpinner compiles the spinner rows, which have no cross-family parent and
// cannot be hidden: a displayed spinner keeps its row.
func compileSpinner(in templated[theme.SpinnerTemplates, SpinnerSlots]) (out SpinnerSlots, _ error) {
	var errs [3]error
	out.Running, errs[0] = compileSlot(in.nest("Running", in.Src.Running, in.Parent.Running), false)
	out.Stopped, errs[1] = compileSlot(in.nest("Stopped", in.Src.Stopped, in.Parent.Stopped), false)
	out.Failed, errs[2] = compileSlot(in.nest("Failed", in.Src.Failed, in.Parent.Failed), false)
	return out, errors.Join(errs[:]...)
}

// progressTemplates are the compiled [ProgressTemplates].
type progressTemplates struct{ Label, Metrics, Hint, Frame *Slot }

// compileProgress compiles the progress slots, which have no cross-family parent.
// Label, Metrics and Hint may be hidden; the frame holds the bar, so it cannot.
func compileProgress(in templated[theme.ProgressTemplates, progressTemplates],
) (out progressTemplates, _ error) {
	var errs [4]error
	out.Label, errs[0] = compileSlot(in.nest("Label", in.Src.Label, in.Parent.Label), true)
	out.Metrics, errs[1] = compileSlot(in.nest("Metrics", in.Src.Metrics, in.Parent.Metrics), true)
	out.Hint, errs[2] = compileSlot(in.nest("Hint", in.Src.Hint, in.Parent.Hint), true)
	out.Frame, errs[3] = compileSlot(in.nest("Frame", in.Src.Frame, in.Parent.Frame), false)
	if errs[3] == nil && in.Src.Frame != "" {
		errs[3] = barOnce(out.Frame)
	}
	return out, errors.Join(errs[:]...)
}

// barOnce checks that a frame inserts the bar once, as a top-level action
// printing .Bar unchanged, and refers to .Bar nowhere else. Then nothing in
// the frame depends on the bar, and the renderer can measure the frame
// before sizing it.
func barOnce(s *Slot) error {
	inserts, uses := 0, 0
	if s.tmpl.Tree != nil {
		for _, n := range s.tmpl.Root.Nodes {
			if a, ok := n.(*parse.ActionNode); ok && isBar(a.Pipe) {
				inserts++
			}
		}
	}
	for _, tt := range s.tmpl.Templates() {
		if tt.Tree == nil {
			continue
		}
		err := walkTemplates(tt.Tree, tt.Root, func(_ *parse.Tree, n parse.Node) error {
			if usesBar(n) {
				uses++
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	if inserts != 1 || uses != 1 {
		return fmt.Errorf("theme.Templates.%s: want one top-level {{ .Bar }} and no other use of .Bar", s.tmpl.Name())
	}
	return nil
}

// isBar reports whether a pipeline only prints .Bar.
func isBar(p *parse.PipeNode) bool {
	if len(p.Decl) > 0 || len(p.Cmds) != 1 || len(p.Cmds[0].Args) != 1 {
		return false
	}
	f, ok := p.Cmds[0].Args[0].(*parse.FieldNode)
	return ok && len(f.Ident) == 1 && f.Ident[0] == "Bar"
}

// usesBar reports whether n reads a Bar field: .Bar, $.Bar, $x.Bar or (x).Bar.
func usesBar(n parse.Node) bool {
	switch n := n.(type) {
	case *parse.FieldNode:
		return n.Ident[0] == "Bar"
	case *parse.VariableNode:
		return len(n.Ident) > 1 && n.Ident[1] == "Bar"
	case *parse.ChainNode:
		return n.Field[0] == "Bar"
	}
	return false
}

// tableTemplates are the compiled [TableTemplates].
type tableTemplates struct{ Header, Cell *Slot }

// compileTable compiles the table slots, which have no cross-family parent. Only
// the header may be hidden: a cell keeps its value.
func compileTable(in templated[theme.TableTemplates, tableTemplates]) (out tableTemplates, _ error) {
	var errs [2]error
	out.Header, errs[0] = compileSlot(in.nest("Header", in.Src.Header, in.Parent.Header), true)
	out.Cell, errs[1] = compileSlot(in.nest("Cell", in.Src.Cell, in.Parent.Cell), false)
	return out, errors.Join(errs[:]...)
}

// progressPercent is the completion of a bar left-aligned in four columns,
// or the Unknown symbol without a total.
const progressPercent = `{{ if gt .Total 0 }}{{ accent (printf "%-4s" (printf "%d%%" .Percent)) }}` +
	`{{ else }}{{ accent (printf "%-4s" (symbol "Unknown")) }}{{ end }}`

// stackedProgressFrame is the built-in frame of bars that are not
// [ProgressStyle.Compact], which sets the label apart from the percentage.
const stackedProgressFrame = `{{ with .Label }}{{ . }}  {{ end }}` + progressPercent +
	` {{ .Bar }}{{ with .Metrics }} {{ . }}{{ end }}`

// progressBuiltins returns the built-in templates with the frame of the layout.
func progressBuiltins(s theme.ProgressStyle) theme.Templates {
	t := builtinTemplates
	if !s.Compact {
		t.Progress.Frame = stackedProgressFrame
	}
	return t
}
