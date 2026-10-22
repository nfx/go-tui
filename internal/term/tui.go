// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package term

import (
	"context"
	"fmt"
	"github.com/nfx/go-tui/internal/option"
	"os"
)

type Tui struct {
	option.Opts
	*IO // exposes io.ReadWriter
	Ctx context.Context
}

func NewTUI(ctx context.Context, opts ...option.Opt) (*Tui, error) {
	cio, err := StartIO(ctx, os.Stderr)
	if err != nil {
		return nil, fmt.Errorf("io: %w", err)
	}
	tio, err := makeTermIO(os.Stdin, cio)
	if err != nil {
		return nil, fmt.Errorf("term: %w", err)
	}
	return &Tui{
		Opts: opts,
		Ctx:  ctx,
		IO:   tio,
	}, nil
}

// PrependView publishes a started viewport of the given height at the head of
// the chain and shrinks the previous head by the same amount, all under chainMu.
func (t *Tui) PrependView(height int) *Viewport {
	cio, ok := t.Out.(*ChanIO)
	if !ok {
		panic("cannot get view")
	}
	cio.ChainMu.Lock()
	defer cio.ChainMu.Unlock()
	width, _ := cio.size()
	top := initViewport(cio.Ctx, cio.notify, width, height)
	top.Next = cio.Head
	if top.Next != nil {
		top.Next.height -= height // TODO: propagate down
	}
	cio.Head = top
	return top
}

func (t *Tui) view() *Viewport {
	cio, ok := t.Out.(*ChanIO)
	if ok {
		cio.ChainMu.Lock()
		defer cio.ChainMu.Unlock()
		return cio.Head
	}
	return nil
}
