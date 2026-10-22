// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"errors"
	"fmt"
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
	Prompt promptTemplates
	Choice choiceTemplates

	Input    promptTemplates
	Password secretTemplates
	Number   secretTemplates

	Dropdown    choiceFamily
	Multichoice multichoiceTemplates
	Confirm     choiceFamily
	FilePicker  choiceFamily

	Spinner  spinnerTemplates
	Progress progressTemplates
	Table    tableTemplates
}

// builtinTemplates are the built-in sources of the root slots, shared by
// Guided and Inline: only geometry differs. Family slots stay unset, so that
// they inherit, and a root slot without a source has no built-in yet.
var builtinTemplates = Templates{
	Prompt: PromptTemplates{
		Label:  `{{ accent (symbol "Prompt") }} {{ bold (text .Label) }}`,
		Answer: `{{ success (symbol "Success") }} {{ text .Label }} {{ muted "·" }} {{ text .Value }}`,
		Hint: `{{ range $i, $h := . }}{{ if $i }}{{ guide " · " }}{{ end }}` +
			`{{ accent $h.Left }} {{ guide $h.Right }}{{ end }}`,
		Feedback: `{{ error (symbol "Error") }} {{ error .Message }}`,
	},
	Choice: ChoiceTemplates{
		Value:    `{{ label . }}`,
		Details:  Hidden,
		Active:   `{{ bold (accent .Value) }}{{ with .Details }} {{ muted . }}{{ end }}`,
		Inactive: `{{ text .Value }}`,
		Empty:    `{{ muted "No matches" }}`,
	},
	Multichoice: MultichoiceTemplates{
		Row: `{{ if .Active }}{{ accent (symbol "Focus") }}{{ else }} {{ end }} ` +
			`{{ if .Selected }}{{ accent (symbol "Checked") }}{{ else }}{{ muted (symbol "Unchecked") }}{{ end }} {{ .Value }}`,
		AnswerValue: `{{ range $i, $v := .Values }}{{ if $i }}, {{ end }}{{ $v }}{{ else }}none{{ end }}`,
	},
	// FilePicker.Value names a [fileView]: directories end with a slash, the
	// way up is muted. It applies unless a theme sets Choice.Value.
	FilePicker: SelectTemplates{ChoiceTemplates: ChoiceTemplates{
		Value: `{{ if .Parent }}{{ muted .Name }}{{ else }}{{ .Name }}{{ if .IsDir }}/{{ end }}{{ end }}`,
	}},
	// Spinner rows name a [spinnerView]. Only a running spinner animates; a
	// stopped one is neutral, since closing it says nothing about success.
	Spinner: SpinnerTemplates{
		Running: `{{ accent .Frame }} {{ with .Prefix }}{{ text . }}{{ muted ":" }} {{ end }}{{ text .Message }}`,
		Stopped: `{{ muted (symbol "Cancel") }} {{ with .Prefix }}{{ text . }}{{ muted ":" }} {{ end }}{{ text .Message }}`,
		Failed:  `{{ error (symbol "Error") }} {{ with .Prefix }}{{ text . }}{{ muted ":" }} {{ end }}{{ text .Message }}`,
	},
	// Progress slots name a [progressView], except Hint, which names the
	// [hintView] of its metrics. The renderer draws the bar in the accent
	// role and its empty track in the guide role. This Frame is the one of
	// [ProgressStyle.Compact] bars; see [ProgressStyle.builtins].
	Progress: ProgressTemplates{
		Label:   `{{ text .Label }}`,
		Metrics: `{{ range $i, $v := .Values }}{{ if $i }}{{ muted " · " }}{{ end }}{{ muted $v }}{{ end }}`,
		Hint: `{{ range $i, $h := . }}{{ if $i }}{{ guide (printf " %s " (symbol "Separator")) }}{{ end }}` +
			`{{ accent $h.Left }}{{ with $h.Right }} {{ guide . }}{{ end }}{{ end }}`,
		Frame: `{{ with .Label }}{{ . }} {{ end }}` + progressPercent + ` {{ .Bar }}{{ with .Metrics }} {{ . }}{{ end }}`,
	},
	// Table slots name a [columnView] and a [cellView]. Weight sets the
	// header apart; values stay in the foreground unless a caller colors them.
	Table: TableTemplates{
		Header: `{{ bold (text .Header) }}`,
		Cell:   `{{ text .Value }}`,
	},
}

// compile resolves isolated slot inheritance over base and reports errors at defining slots.
func (t *Templates) compile(funcs template.FuncMap, base templates) (templates, error) {
	root := templated[struct{}, struct{}]{Funcs: funcs}
	var out templates
	var errs [12]error
	out.Prompt, errs[0] = t.prompt(root.nest("Prompt", t.Prompt, base.Prompt))
	out.Input, errs[1] = t.prompt(root.nest("Input", t.Input, out.Prompt))
	out.Password, errs[2] = t.secret(root.nest("Password", t.Password, out.Input))
	out.Number, errs[3] = t.secret(root.nest("Number", t.Number, out.Input))
	out.Choice, errs[4] = t.choice(root.nest("Choice", t.Choice, base.Choice))
	family := choiceFamily{out.Prompt, out.Choice}
	out.Dropdown, errs[5] = t.choiceFamily(root.nest("Dropdown", t.Dropdown, family))
	out.Multichoice, errs[6] = t.multichoice(root.nest("Multichoice", t.Multichoice, multichoiceTemplates{
		choiceFamily: family,
		Row:          base.Multichoice.Row,
		AnswerValue:  base.Multichoice.AnswerValue,
	}))
	out.Confirm, errs[7] = t.choiceFamily(root.nest("Confirm", t.Confirm, family))
	picker := family
	if t.Choice.Value == "" {
		// a shared projection reaches the picker, otherwise its own built-in
		picker.Value = base.FilePicker.Value
	}
	out.FilePicker, errs[8] = t.choiceFamily(root.nest("FilePicker", t.FilePicker, picker))
	out.Spinner, errs[9] = t.spinner(root.nest("Spinner", t.Spinner, base.Spinner))
	out.Progress, errs[10] = t.progress(root.nest("Progress", t.Progress, base.Progress))
	out.Table, errs[11] = t.table(root.nest("Table", t.Table, base.Table))
	if err := errors.Join(errs[:]...); err != nil {
		return templates{}, err
	}
	return out, nil
}

// promptTemplates are the compiled [PromptTemplates].
type promptTemplates struct{ Label, Answer, Hint, Feedback *compiledSlot }

// prompt compiles prompt slots, inheriting unset slots from the parent.
func (t *Templates) prompt(in templated[PromptTemplates, promptTemplates]) (out promptTemplates, _ error) {
	var errs [4]error
	out.Label, errs[0] = t.slot(in.nest("Label", in.Src.Label, in.Parent.Label), true)
	out.Answer, errs[1] = t.slot(in.nest("Answer", in.Src.Answer, in.Parent.Answer), true)
	out.Hint, errs[2] = t.slot(in.nest("Hint", in.Src.Hint, in.Parent.Hint), true)
	out.Feedback, errs[3] = t.slot(in.nest("Feedback", in.Src.Feedback, in.Parent.Feedback), true)
	return out, errors.Join(errs[:]...)
}

// secretTemplates are the compiled [SecretTemplates].
type secretTemplates struct{ Label, Hint, Feedback *compiledSlot }

// secret compiles prompt slots without an answer slot, inheriting unset slots.
func (t *Templates) secret(in templated[SecretTemplates, promptTemplates]) (out secretTemplates, _ error) {
	var errs [3]error
	out.Label, errs[0] = t.slot(in.nest("Label", in.Src.Label, in.Parent.Label), true)
	out.Hint, errs[1] = t.slot(in.nest("Hint", in.Src.Hint, in.Parent.Hint), true)
	out.Feedback, errs[2] = t.slot(in.nest("Feedback", in.Src.Feedback, in.Parent.Feedback), true)
	return out, errors.Join(errs[:]...)
}

// choiceTemplates are the compiled [ChoiceTemplates].
type choiceTemplates struct{ Value, Details, Active, Inactive, Empty *compiledSlot }

// choice compiles inherited choice slots, rejecting Hidden for Value, Active, and Inactive.
func (t *Templates) choice(in templated[ChoiceTemplates, choiceTemplates]) (out choiceTemplates, _ error) {
	var errs [5]error
	out.Value, errs[0] = t.slot(in.nest("Value", in.Src.Value, in.Parent.Value), false)
	out.Details, errs[1] = t.slot(in.nest("Details", in.Src.Details, in.Parent.Details), true)
	out.Active, errs[2] = t.slot(in.nest("Active", in.Src.Active, in.Parent.Active), false)
	out.Inactive, errs[3] = t.slot(in.nest("Inactive", in.Src.Inactive, in.Parent.Inactive), false)
	out.Empty, errs[4] = t.slot(in.nest("Empty", in.Src.Empty, in.Parent.Empty), true)
	return out, errors.Join(errs[:]...)
}

// choiceFamily are the compiled [SelectTemplates].
type choiceFamily struct {
	promptTemplates
	choiceTemplates
}

// choiceFamily compiles family overrides with shared prompt and choice fallbacks.
func (t *Templates) choiceFamily(in templated[SelectTemplates, choiceFamily]) (out choiceFamily, _ error) {
	var errs [2]error
	out.promptTemplates, errs[0] = t.prompt(in.nest("", in.Src.PromptTemplates, in.Parent.promptTemplates))
	out.choiceTemplates, errs[1] = t.choice(in.nest("", in.Src.ChoiceTemplates, in.Parent.choiceTemplates))
	return out, errors.Join(errs[:]...)
}

// multichoiceTemplates are the compiled [MultichoiceTemplates].
type multichoiceTemplates struct {
	choiceFamily
	Row, AnswerValue *compiledSlot
}

// multichoice compiles family slots with built-in fallbacks for Row and AnswerValue.
func (t *Templates) multichoice(
	in templated[MultichoiceTemplates, multichoiceTemplates],
) (out multichoiceTemplates, _ error) {
	var errs [3]error
	out.choiceFamily, errs[0] = t.choiceFamily(in.nest("", in.Src.SelectTemplates, in.Parent.choiceFamily))
	out.Row, errs[1] = t.slot(in.nest("Row", in.Src.Row, in.Parent.Row), false)
	out.AnswerValue, errs[2] = t.slot(in.nest("AnswerValue", in.Src.AnswerValue, in.Parent.AnswerValue), true)
	return out, errors.Join(errs[:]...)
}

// spinnerTemplates are the compiled [SpinnerTemplates].
type spinnerTemplates struct{ Running, Stopped, Failed *compiledSlot }

// spinner compiles the spinner rows, which have no cross-family parent and
// cannot be hidden: a displayed spinner keeps its row.
func (t *Templates) spinner(in templated[SpinnerTemplates, spinnerTemplates]) (out spinnerTemplates, _ error) {
	var errs [3]error
	out.Running, errs[0] = t.slot(in.nest("Running", in.Src.Running, in.Parent.Running), false)
	out.Stopped, errs[1] = t.slot(in.nest("Stopped", in.Src.Stopped, in.Parent.Stopped), false)
	out.Failed, errs[2] = t.slot(in.nest("Failed", in.Src.Failed, in.Parent.Failed), false)
	return out, errors.Join(errs[:]...)
}

// progressTemplates are the compiled [ProgressTemplates].
type progressTemplates struct{ Label, Metrics, Hint, Frame *compiledSlot }

// progress compiles the progress slots, which have no cross-family parent.
// Label, Metrics and Hint may be hidden; the frame holds the bar, so it cannot.
func (t *Templates) progress(in templated[ProgressTemplates, progressTemplates]) (out progressTemplates, _ error) {
	var errs [4]error
	out.Label, errs[0] = t.slot(in.nest("Label", in.Src.Label, in.Parent.Label), true)
	out.Metrics, errs[1] = t.slot(in.nest("Metrics", in.Src.Metrics, in.Parent.Metrics), true)
	out.Hint, errs[2] = t.slot(in.nest("Hint", in.Src.Hint, in.Parent.Hint), true)
	out.Frame, errs[3] = t.slot(in.nest("Frame", in.Src.Frame, in.Parent.Frame), false)
	if errs[3] == nil && in.Src.Frame != "" {
		errs[3] = t.barOnce(out.Frame)
	}
	return out, errors.Join(errs[:]...)
}

// barOnce checks that a frame inserts the bar once, as a top-level action
// printing .Bar unchanged, and refers to .Bar nowhere else. Then nothing in
// the frame depends on the bar, and the renderer can measure the frame
// before sizing it.
func (t *Templates) barOnce(s *compiledSlot) error {
	inserts, uses := 0, 0
	if s.tmpl.Tree != nil {
		for _, n := range s.tmpl.Root.Nodes {
			if a, ok := n.(*parse.ActionNode); ok && t.isBar(a.Pipe) {
				inserts++
			}
		}
	}
	for _, tt := range s.tmpl.Templates() {
		if tt.Tree == nil {
			continue
		}
		err := t.walk(tt.Tree, tt.Root, func(_ *parse.Tree, n parse.Node) error {
			if t.usesBar(n) {
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
func (*Templates) isBar(p *parse.PipeNode) bool {
	if len(p.Decl) > 0 || len(p.Cmds) != 1 || len(p.Cmds[0].Args) != 1 {
		return false
	}
	f, ok := p.Cmds[0].Args[0].(*parse.FieldNode)
	return ok && len(f.Ident) == 1 && f.Ident[0] == "Bar"
}

// usesBar reports whether n reads a Bar field: .Bar, $.Bar, $x.Bar or (x).Bar.
func (*Templates) usesBar(n parse.Node) bool {
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
type tableTemplates struct{ Header, Cell *compiledSlot }

// table compiles the table slots, which have no cross-family parent. Only
// the header may be hidden: a cell keeps its value.
func (t *Templates) table(in templated[TableTemplates, tableTemplates]) (out tableTemplates, _ error) {
	var errs [2]error
	out.Header, errs[0] = t.slot(in.nest("Header", in.Src.Header, in.Parent.Header), true)
	out.Cell, errs[1] = t.slot(in.nest("Cell", in.Src.Cell, in.Parent.Cell), false)
	return out, errors.Join(errs[:]...)
}
