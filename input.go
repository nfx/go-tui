// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"text/template"
	"unicode/utf8"
)

func WithDefault(d string) opt {
	return inputOpt(func(p *input) error {
		p.typed = d
		p.cursor = len(d)

		return nil
	})
}

func inputOpt(o func(d *input) error) opt {
	return func(a any) error {
		d, ok := a.(*input)
		if !ok {
			return fmt.Errorf("%w: need a input, got %v", ErrInvalidState, a)
		}

		return o(d)
	}
}

type input struct {
	config

	Label    string
	Hide     bool
	Password bool

	LabelTemplate  string
	labelTemplate  *template.Template
	AnswerTemplate string
	answerTemplate *template.Template
	typed          string
	cursor         int

	CheckFn func(rawInput string) (string, bool)

	// TODO: special case for testing?..
	makeTermIO func(in io.Reader, out io.Writer) (*termIO, error)
}

func newInput(label string) *input {
	return &input{
		config: config{
			ctx: context.Background(),
			out: os.Stderr,
			in:  os.Stdin,
		},
		Label:          label,
		LabelTemplate:  DefaultLabelTemplate,
		AnswerTemplate: DefaultAnswerTemplate,
		makeTermIO:     makeTermIO,
	}
}

func Input(label string, option ...opt) (string, error) {
	i := newInput(label)

	return i.read(option...)
}

func Password(label string, option ...opt) (string, error) {
	i := newInput(label)
	i.Password = true

	return i.read(option...)
}

func (i *input) read(option ...opt) (string, error) {
	err := opts(option).Apply(i)
	if err != nil {
		return "", err
	}
	err = i.parseTemplates()
	if err != nil {
		return "", err
	}
	out, err := i.run()
	if err != nil {
		return "", err
	}
	err = i.showAnswer()
	if err != nil {
		return "", err
	}

	return out, nil
}

func (i *input) showAnswer() error {
	if i.Hide || i.Password {
		return nil
	}
	var buf bytes.Buffer
	// TODO: unify reused structures
	err := i.answerTemplate.Execute(&buf, dropdownAnswer{
		Label:  i.Label,
		Answer: i.typed,
	})
	if err != nil {
		return fmt.Errorf("answer: %w", err)
	}
	_, err = buf.WriteTo(i.out)
	if err != nil {
		return fmt.Errorf("write: %w", err)
	}

	return nil
}

func (p *input) run() (string, error) {
	io, err := p.makeTermIO(p.in, p.out)
	if err != nil {
		return "", err
	}
	defer io.Restore() //nolint:errcheck
	frame := &bytes.Buffer{}
	for {
		err = p.render(io, frame)
		if err != nil {
			return "", errors.Join(err, p.clear(io))
		}
		select {
		case <-p.ctx.Done():
			return "", errors.Join(p.ctx.Err(), p.clear(io))
		default:
			// key press handlers has to be limited to state updates, not writes to the buffer.
			out, err := p.pressKey(io)
			if err != nil { // e.g., Ctrl+C or Ctrl+D
				return "", errors.Join(err, p.clear(io))
			} else if out != "" {
				return p.typed, p.clear(io)
			}
		}
	}
}

func (i *input) render(io *termIO, frame *bytes.Buffer) error {
	frame.Reset()
	var errs []error
	err := frame.WriteByte('\r')
	if err != nil {
		errs = append(errs, err)
	}
	err = i.labelTemplate.Execute(frame, i.Label)
	if err != nil {
		errs = append(errs, err)
	}
	var displayed string
	if i.Password {
		displayed = strings.Repeat("*", utf8.RuneCountInString(i.typed))
	} else {
		displayed = i.typed
	}
	// write displayed text and clear to the end of the line
	_, err = fmt.Fprintf(frame, "%s\x1b[K", displayed)
	if err != nil {
		errs = append(errs, err)
	}
	moveLeft := utf8.RuneCountInString(displayed) - i.cursor
	if moveLeft > 0 {
		// move the cursor left by the difference between
		// the end and the desired position
		_, err = fmt.Fprintf(frame, "\x1b[%dD", moveLeft)
		if err != nil {
			errs = append(errs, err)
		}
	}
	_, err = frame.WriteTo(io.out)
	if err != nil {
		errs = append(errs, err)
	}
	if len(errs) > 0 {
		return errors.Join(errs...)
	}

	return nil
}

func (*input) clear(io *termIO) error {
	var errs []error
	_, err := fmt.Fprintln(io.out)
	if err != nil {
		errs = append(errs, fmt.Errorf("newline: %w", err))
	}
	err = io.clear(1, io.out)
	if err != nil {
		errs = append(errs, fmt.Errorf("clear: %w", err))
	}

	return errors.Join(errs...)
}

func (p *input) pressKey(io *termIO) (string, error) {
	key, _, err := io.ReadRune()
	var more *pasteTextError
	if errors.As(err, &more) {
		// Ctrl+V or CMD+V will just send more bytes. So we emulate typing.
		// This currently works with empty input only. Or appending to the end.
		// There's a bug when you paste in the middle of the text.
		for _, b := range more.buf {
			p.pressAny(rune(b))
		}

		return "", nil
	} else if err != nil {
		// Ctrl+C or Ctrl+D will result in an error like io.EOF
		return "", fmt.Errorf("read: %w", err)
	}
	switch key {
	case keyEnter:
		return p.typed, nil
	case 0x7f: // backspace
		p.pressBackspace()
	case '←':
		p.pressLeft()
	case '→':
		p.pressRight()
	case '↑', '↓': // ignore up/down arrows
		return "", nil
	default:
		p.pressAny(key)
	}

	return "", nil
}

func (p *input) pressBackspace() {
	if len(p.typed) > 0 && p.cursor > 0 {
		p.typed = p.typed[:p.cursor-1] + p.typed[p.cursor:]
		p.cursor--
	}
}

func (p *input) pressLeft() {
	if p.cursor > 0 {
		p.cursor--
	}
}

func (p *input) pressRight() {
	if p.cursor < len(p.typed) {
		p.cursor++
	}
}

func (p *input) pressAny(key rune) {
	p.typed = p.typed[:p.cursor] + string(key) + p.typed[p.cursor:]
	p.cursor++
}

func (p *input) parseTemplates() (err error) {
	tmpl := template.New("dropdown").Funcs(colorFns)
	p.labelTemplate, err = tmpl.New("label").Parse(mustEndWith(p.LabelTemplate, ' '))
	if err != nil {
		return fmt.Errorf("label: %w", err)
	}
	p.answerTemplate, err = tmpl.New("answer").Parse(mustEndWith(p.AnswerTemplate, '\n'))
	if err != nil {
		return fmt.Errorf("answer: %w", err)
	}

	return nil
}
