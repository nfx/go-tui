// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"context"
	"fmt"
	"os"
)

type Tui struct {
	opts
	*termIO // exposes io.ReadWriter
	ctx     context.Context
}

func NewTUI(ctx context.Context, opts ...opt) (*Tui, error) {
	cio, err := NewIO(ctx)
	if err != nil {
		return nil, fmt.Errorf("io: %w", err)
	}
	tio, err := makeTermIO(os.Stdin, cio)
	if err != nil {
		return nil, fmt.Errorf("term: %w", err)
	}
	return &Tui{
		opts:   opts,
		ctx:    ctx,
		termIO: tio,
	}, nil
}

// prependView publishes a started viewport of the given height at the head of
// the chain and shrinks the previous head by the same amount, all under chainMu.
func (t *Tui) prependView(height int) *viewport {
	cio, ok := t.out.(*chanIO)
	if !ok {
		panic("cannot get view")
	}
	cio.chainMu.Lock()
	defer cio.chainMu.Unlock()
	top := initViewport(cio.ctx, cio.notify, cio.width, height)
	top.next = cio.head
	if top.next != nil {
		top.next.height -= height // TODO: propagate down
	}
	cio.head = top
	return top
}

func (t *Tui) view() *viewport {
	cio, ok := t.out.(*chanIO)
	if ok {
		cio.chainMu.Lock()
		defer cio.chainMu.Unlock()
		return cio.head
	}
	return nil
}
