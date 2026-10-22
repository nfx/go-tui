// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"bytes"
	"context"
	"fmt"
	"github.com/nfx/go-tui/internal/ansi"
	"github.com/nfx/go-tui/internal/render"
	"strings"
	"unicode"
)

// feedbackSafe drops line breaks and terminal controls, so that a message
// cannot move the cursor or start an escape sequence.
func (p *input) feedbackSafe(message string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\r' || r == '\n' || r == '\t':
			return ' '
		case unicode.IsControl(r):
			return -1
		}
		return r
	}, strings.ToValidUTF8(message, "�"))
}

// slots selects the theme slots of the prompt family. A password retains no answer.
func (p *input) slots() render.PromptSlots {
	t := &p.theme.Templates
	switch {
	case p.Password:
		return render.PromptSlots{Label: t.Password.Label, Hint: t.Password.Hint, Feedback: t.Password.Feedback}
	case p.numeric:
		return render.PromptSlots{
			Label:    t.Number.Label,
			Answer:   t.Input.Answer,
			Hint:     t.Number.Hint,
			Feedback: t.Number.Feedback,
		}
	}
	return t.Input
}

// bind captures the presentation of one run for its destination, before the
// first frame: the helpers, the bound
// slots, and the label and hint, which render once. tt is the terminal lease.
func (p *input) bind(ctx context.Context, tt *termIO) error {
	p.look = defaultIO.bindTheme(ctx, p.theme, p.Out, tt)
	slots := p.slots()
	var err error
	if p.answer, err = slots.Answer.Bind(p.look.Funcs); err != nil {
		return err
	}
	if p.feedback, err = slots.Feedback.Bind(p.look.Funcs); err != nil {
		return err
	}
	if err = p.bindHint(slots.Hint); err != nil {
		return err
	}
	slot, err := slots.Label.Bind(p.look.Funcs)
	if err != nil {
		return err
	}
	p.label, err = slot.Render(render.LabelView{Label: p.Label})
	if err != nil {
		return err
	}
	if p.theme.Source.Inline && p.label.Width() > 0 {
		// the renderer owns the spacing before the value
		p.label = append(p.label, ' ')
	}
	return nil
}

// bindHint renders the hint row once; a prompt without keys beyond editing has none.
func (p *input) bindHint(hint *render.Slot) error {
	p.hint = nil
	if len(p.keys) == 0 {
		return nil
	}
	slot, err := hint.Bind(p.look.Funcs)
	if err != nil {
		return err
	}
	p.hint, err = slot.Render(render.HintView(p.keys))
	return err
}

// render draws the prompt, leaving the caret on the value row for the next frame.
func (i *input) render(io *termIO, frame *bytes.Buffer) error {
	io.refreshSize()
	frame.Reset()
	g := i.look.Geometry(io.Width, io.Height)
	lead := g.ValueLead(i.label)
	cols, err := g.ValueColumns(lead)
	if err != nil {
		return err
	}
	runes := i.displayRunes()
	cells := ansi.Text(string(runes)).Clusters()
	first, last := i.visibleWindow(cells, cols)
	visible := ansi.Text(string(runes[cells.RuneOffset(first):cells.RuneOffset(last)]))
	if i.Password {
		// Keep one mask per visible typed rune, even for multi-rune masks.
		visible = ansi.Text(strings.Repeat(i.theme.Source.Symbols.Mask, last-first))
	}
	feedback, err := i.themedFeedback()
	if err != nil {
		return err
	}
	rows := make([]render.FrameRow, 0, 4)
	if !g.Inline {
		rows = append(rows, render.FrameRow{Content: i.label, Drop: 1})
	}
	value := len(rows)
	row := append(append(ansi.Text{}, lead...), i.look.Style(i.look.Roles.Foreground, visible)...)
	rows = append(rows, render.FrameRow{Content: row})
	if len(feedback) > 0 {
		rows = append(rows, render.FrameRow{Content: feedback, Guided: true})
	}
	// the hint goes first on a short terminal, before the label
	rows = append(rows, render.FrameRow{Content: i.hint, Guided: true, Drop: 2})
	fit, err := g.Fit(rows)
	if err != nil {
		return err
	}
	// a cursor inside of a cluster stands on its first rune
	at := min(max(cells.At(i.cursor), first), last)
	i.writeFrame(io, frame, fit, fit.At[value], lead.Width()+cells[first:at].Width(), cells[at:last].Width())
	_, err = frame.WriteTo(io)
	return err
}

// writeFrame draws fitted rows from the top of the previous frame, erases
// the rows it had beyond them and puts the caret on row caret, col columns
// in, which is left columns before the end of that row.
func (i *input) writeFrame(io *termIO, frame *bytes.Buffer, fit render.Fitted, caret, col, left int) {
	if io.vp == nil && i.caretRow > 0 {
		fmt.Fprintf(frame, "\x1b[%dA", i.caretRow)
	}
	for k, line := range fit.Lines {
		if k > 0 {
			frame.WriteByte('\n')
		}
		frame.WriteByte('\r')
		frame.Write(line)
		frame.WriteString("\x1b[K")
	}
	drawn, stale := len(fit.Lines), i.rows
	if io.Height > 0 {
		// rows past the bottom of a shorter terminal are gone, and a newline
		// there would scroll away the rows just drawn
		stale = min(stale, max(io.Height, drawn))
	}
	for ; drawn < stale; drawn++ {
		// erase a row of the taller previous frame
		frame.WriteString("\n\r\x1b[K")
	}
	switch up := drawn - 1 - caret; {
	case up > 0:
		// an absolute column, as a managed viewport drops the carriage return
		fmt.Fprintf(frame, "\x1b[%dA\x1b[%dG", up, col+1)
	case left > 0:
		fmt.Fprintf(frame, "\x1b[%dD", left)
	}
	i.rows, i.caretRow = len(fit.Lines), caret
}

// themedFeedback renders the rejection with the feedback slot. The geometry
// clips it.
func (i *input) themedFeedback() (ansi.Text, error) {
	if i.rejection == "" {
		return nil, nil
	}
	return i.feedback.Render(render.FeedbackView{Message: i.feedbackSafe(i.rejection)})
}

// themedAnswer renders the answer slot of the family and ends its line. A
// hidden or empty answer retains nothing.
func (i *input) themedAnswer(buf *bytes.Buffer) error {
	value := i.safeText(i.typed)
	out, err := i.answer.Render(render.AnswerView{Label: i.Label, Value: value, Values: []string{value}, Count: 1})
	if err != nil || out.Width() == 0 {
		return err
	}
	buf.Write(out)
	return buf.WriteByte('\n')
}
