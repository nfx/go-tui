package tui

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"text/template"
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
			return fmt.Errorf("need a input, got %v", a)
		}
		return o(d)
	}
}

type input struct {
	config

	Label string
	Hide  bool

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

func newInput() *input {
	return &input{
		config: config{
			ctx: context.Background(),
			out: os.Stderr,
			in:  os.Stdin,
		},
		Label:          "Enter input:",
		LabelTemplate:  DefaultLabelTemplate,
		AnswerTemplate: DefaultAnswerTemplate,
		makeTermIO:     makeTermIO,
	}
}

func Input(label string, option ...opt) (string, error) {
	p := newInput()
	p.Label = label
	err := opts(option).Apply(p)
	if err != nil {
		return "", err
	}
	err = p.parseTemplates()
	if err != nil {
		return "", err
	}
	return p.run()
}

func (p *input) run() (string, error) {
	io, err := p.makeTermIO(p.in, p.out)
	if err != nil {
		return "", err
	}
	defer io.Restore()
	var frame bytes.Buffer
	var init bool
	for {
		if !init {
			init = true
		}
		err = p.labelTemplate.Execute(&frame, p.Label)
		if err != nil {
			return "", fmt.Errorf("label: %w", err)
		}
		frame.WriteString(p.typed)
		frame.WriteRune('\r')
		frame.WriteRune('\n')
		_, err = frame.WriteTo(io.out)
		if err != nil {
			return "", err
		}
		select {
		case <-p.ctx.Done():
			return "", p.ctx.Err()
		default:
			key, err := io.ReadRune()
			io.clear(1, &frame)
			if err != nil {
				if errors.Is(err, ErrUnknownRune) {
					continue
				}
				frame.WriteTo(io) // clear the screen
				// Ctrl+C or Ctrl+D
				return "", err
			}
			switch key {
			case keyEnter:
				frame.WriteTo(io)
				return string(p.typed), nil
			case 0x7f: // backspace
				if len(p.typed) > 0 {
					fmt.Fprintf(&frame, "\x1b[1K")
					if p.cursor > 0 {
						p.typed = p.typed[:p.cursor-1] + p.typed[p.cursor:]
						p.cursor--
					}
				}
			case '←':
				if p.cursor > 0 {
					p.cursor--
				}
			case '→':
				if p.cursor < len(p.typed) {
					p.cursor++
				}
			default:
				p.typed = p.typed[:p.cursor] + string(key) + p.typed[p.cursor:]
				p.cursor++
			}
		}
	}
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
