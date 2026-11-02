// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package progress

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nfx/go-tui/internal/assert"
	"github.com/nfx/go-tui/internal/errs"
	"github.com/nfx/go-tui/internal/option"
	"github.com/nfx/go-tui/internal/term"
)

func transformWithoutTerminal() option.Opt {
	return option.OpT(func(p *Progressbar) error {
		p.makeTermIO = func(in io.Reader, out io.Writer) (*term.IO, error) {
			return nil, errs.ErrNoTTY
		}
		return nil
	})
}

func TestNewTransformProgressBarConvertsItemType(t *testing.T) {
	got, err := NewTransformProgressBar(t.Context(), "transform", []int{1, 2, 3}, func(v int) (string, error) {
		return strconv.Itoa(v * 2), nil
	}, transformWithoutTerminal())
	assert.NoError(t, err)
	assert.Equal(t, []string{"2", "4", "6"}, got)
}

func TestNewTransformProgressBarKeepsInputOrderWhenLastItemFinishesFirst(t *testing.T) {
	gates := []chan struct{}{make(chan struct{}), make(chan struct{}), make(chan struct{})}
	close(gates[2])
	var finished atomic.Int64
	var order [3]int64
	got, err := NewTransformProgressBar(t.Context(), "transform", []int{0, 1, 2}, func(v int) (int, error) {
		err := openedWithin(gates[v])
		if err != nil {
			return 0, err
		}
		order[v] = finished.Add(1)
		if v > 0 {
			close(gates[v-1])
		}
		return v + 10, nil
	}, WithWorkers(3), transformWithoutTerminal())
	assert.NoError(t, err)
	assert.Equal(t, []int{10, 11, 12}, got)
	assert.Equal(t, [3]int64{3, 2, 1}, order)
}

func TestNewTransformProgressBarDuplicateItemsKeepOwnResults(t *testing.T) {
	var calls atomic.Int64
	got, err := NewTransformProgressBar(
		t.Context(),
		"transform",
		[]string{"a", "a", "a"},
		func(v string) (string, error) {
			calls.Add(1)
			return v + "!", nil
		},
		WithWorkers(2),
		transformWithoutTerminal(),
	)
	assert.NoError(t, err)
	assert.Equal(t, []string{"a!", "a!", "a!"}, got)
	assert.Equal(t, int64(3), calls.Load())
}

func TestNewTransformProgressBarEmptyInputReturnsEmptyNonNilResults(t *testing.T) {
	var calls atomic.Int64
	got, err := NewTransformProgressBar(t.Context(), "transform", []int{}, func(int) (string, error) {
		calls.Add(1)
		return "", nil
	}, transformWithoutTerminal())
	assert.NoError(t, err)
	assert.True(t, got != nil)
	assert.Equal(t, 0, len(got))
	assert.Equal(t, int64(0), calls.Load())
}

func TestNewTransformProgressBarSkippedItemsLeftOutKeepingOrder(t *testing.T) {
	tests := []struct {
		name string
		skip error
		want []int
	}{
		{name: "sentinel", skip: errs.ErrSkip, want: []int{1, 3, 5}},
		{name: "wrapped sentinel", skip: fmt.Errorf("no match: %w", errs.ErrSkip), want: []int{1, 3, 5}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NewTransformProgressBar(
				t.Context(),
				"transform",
				[]int{1, 2, 3, 4, 5},
				func(v int) (int, error) {
					if v%2 == 0 {
						return v, tt.skip
					}
					return v, nil
				},
				WithWorkers(2),
				transformWithoutTerminal(),
			)
			assert.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestNewTransformProgressBarAllSkippedReturnsEmptyNonNilResults(t *testing.T) {
	got, err := NewTransformProgressBar(t.Context(), "transform", []int{1, 2}, func(int) (string, error) {
		return "", errs.ErrSkip
	}, transformWithoutTerminal())
	assert.NoError(t, err)
	assert.True(t, got != nil)
	assert.Equal(t, 0, len(got))
}

func TestNewTransformProgressBarSkipDoesNotStopRemainingItems(t *testing.T) {
	var calls atomic.Int64
	_, err := NewTransformProgressBar(t.Context(), "transform", make([]int, 32), func(int) (int, error) {
		calls.Add(1)
		return 0, errs.ErrSkip
	}, WithWorkers(4), transformWithoutTerminal())
	assert.NoError(t, err)
	assert.Equal(t, int64(32), calls.Load())
}

func TestNewTransformProgressBarReturnsResultsAndRestoresTerminal(t *testing.T) {
	ft := newFakeTerminal(t, 40)
	got, err := NewTransformProgressBar(t.Context(), "build", []int{1, 2}, func(v int) (int, error) {
		return v * v, nil
	}, append(ft.options(), WithWorkers(2))...)
	assert.NoError(t, err)
	assert.Equal(t, []int{1, 4}, got)
	assert.True(t, ft.restored.Load())
}

func TestNewTransformProgressBarFailuresReturnNoResults(t *testing.T) {
	fail := errors.New("fail")
	tests := []struct {
		name  string
		yield func(int) (int, error)
		want  error
	}{
		{
			name: "callback error",
			yield: func(v int) (int, error) {
				if v == 2 {
					return 0, fail
				}
				return v, nil
			},
			want: fail,
		},
		{
			name: "callback panic",
			yield: func(v int) (int, error) {
				if v == 2 {
					panic("boom")
				}
				return v, nil
			},
			want: errs.ErrBug,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NewTransformProgressBar(t.Context(), "transform", []int{1, 2, 3}, tt.yield,
				WithWorkers(2), transformWithoutTerminal())
			assert.ErrorIs(t, err, tt.want)
			assert.True(t, got == nil)
		})
	}
}

func TestNewTransformProgressBarParentCancelReturnsNoResults(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var ran atomic.Int64
	got, err := NewTransformProgressBar(ctx, "transform", make([]int, 100), func(v int) (int, error) {
		ran.Add(1)
		cancel()
		return v, nil
	}, WithWorkers(1), transformWithoutTerminal())
	assert.ErrorIs(t, err, context.Canceled)
	assert.True(t, got == nil)
	assert.True(t, ran.Load() < 100)
}

func TestNewTransformProgressBarInvalidOptionSkipsCallback(t *testing.T) {
	var calls atomic.Int64
	got, err := NewTransformProgressBar(t.Context(), "transform", []int{1}, func(v int) (int, error) {
		calls.Add(1)
		return v, nil
	}, WithWorkers(0))
	assert.ErrorIs(t, err, errs.ErrInvalidState)
	assert.True(t, got == nil)
	assert.Equal(t, int64(0), calls.Load())
}

func TestNewTransformProgressBarCloseErrorReturnsNoResults(t *testing.T) {
	closeErr := io.EOF
	got, err := NewTransformProgressBar(t.Context(), "transform", []int{1}, func(v int) (int, error) {
		return v, nil
	}, option.OpT(func(p *Progressbar) error {
		p.err = closeErr
		p.ticks = make(chan time.Time)
		p.ticker = time.NewTicker(time.Hour)
		p.makeTermIO = func(in io.Reader, out io.Writer) (*term.IO, error) {
			return &term.IO{
				In:      in,
				Out:     &bytes.Buffer{},
				Width:   40,
				Height:  1,
				Restore: func() error { return nil },
			}, nil
		}
		return nil
	}))
	assert.ErrorIs(t, err, closeErr)
	assert.True(t, got == nil)
}
