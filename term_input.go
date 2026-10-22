// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"reflect"
	"sync"
	"sync/atomic"
)

// inputOrder orders reads, loading completion, and cancellation even when
// the system clock returns equal timestamps for successive events.
var inputOrder atomic.Uint64

func nextInputOrder() uint64 {
	return inputOrder.Add(1)
}

// readEvents reads one event per permit; nil permits reads a single event.
// Cancellation retains an unconsumed read for the next prompt on this input.
func (t *termIO) readEvents(ctx context.Context, permits <-chan struct{}) <-chan keyEvent {
	// Finish publishing the previous key's pending bytes before starting a read.
	t.awaitReader()
	ch := make(chan keyEvent)
	done := make(chan struct{})
	t.reader = done
	go func() {
		defer close(done)
		defer close(ch)
		for {
			if permits != nil {
				select {
				case <-ctx.Done():
					return
				case _, ok := <-permits:
					if !ok {
						return
					}
				}
			}
			if ctx.Err() != nil {
				return
			}
			// Buffered bytes never reach the fd again, so do not poll for them.
			if !t.hasInput() && len(t.pending) == 0 {
				if err := t.waitForReadableInput(ctx, t.in); err != nil {
					select {
					case <-ctx.Done():
					case ch <- keyEvent{err: err}:
					}
					return
				}
			}
			if ctx.Err() != nil {
				return
			}
			read, consumed := t.startRead(ctx)
			select {
			case <-ctx.Done():
				t.abandonPaste(read)
				return
			case <-read.done:
			}
			event := t.shapeEvent(read)
			select {
			case <-ctx.Done():
				t.abandonPaste(read)
				return
			case ch <- event:
				t.pending = read.pending
				t.pendingOrder = read.pendingOrder
				consumed()
			}
			if permits == nil || (read.event.err != nil && read.event.paste == nil) {
				return
			}
		}
	}()
	return ch
}

// shapeEvent adapts a completed read to the consumer's paste mode, dropping
// bracketed events whose opening bytes preceded cancellation.
func (t *termIO) shapeEvent(read *inputRead) keyEvent {
	inputStates.Lock()
	defer inputStates.Unlock()
	event := read.event
	if event.bracketed && event.readOrder < read.discardBefore {
		return keyEvent{key: keyIgnored}
	}
	if event.bracketed && !t.bracketedPaste {
		// Legacy consumers still receive their buffered-text event shape.
		event.err = &pasteTextError{buf: event.paste}
	}
	return event
}

// awaitReader waits until the last [termIO.readEvents] goroutine exits after its
// context is cancelled. It does not receive the key, so an unreceived key stays
// with the input for its next prompt.
func (t *termIO) awaitReader() {
	if t.reader != nil {
		<-t.reader
	}
}

type inputRead struct {
	done          chan struct{}
	event         keyEvent
	discardBefore uint64
	pending       []byte
	pendingOrder  uint64
}

// inputState retains only input decoding state across sequential prompt runs.
// The registry contains states with an outstanding read or buffered bytes.
type inputState struct {
	source       any
	read         *inputRead
	pending      []byte
	pendingOrder uint64
	// replay holds decoded events that precede the in-flight read.
	replay []keyEvent
	// queries await terminal replies, guarded by inputStates.
	queries []*controlQuery
}

var inputStates = struct {
	sync.Mutex
	states map[any]*inputState
}{states: make(map[any]*inputState)}

// sharedInputState shares retained reads only through a comparable reader identity.
func sharedInputState(in io.Reader) *inputState {
	// Reader identity is never inferred from a descriptor number.
	if in == nil || !reflect.ValueOf(in).Comparable() {
		return &inputState{}
	}
	inputStates.Lock()
	defer inputStates.Unlock()
	if state := inputStates.states[in]; state != nil {
		return state
	}
	// Idle files remain local; outstanding reads retain this exact file object.
	if _, file := in.(*os.File); file {
		return &inputState{}
	}
	return &inputState{source: in}
}

func (t *termIO) inputState() *inputState {
	if t.input == nil {
		t.input = sharedInputState(t.in)
	}
	return t.input
}

func (t *termIO) hasInput() bool {
	state := t.inputState()
	inputStates.Lock()
	defer inputStates.Unlock()
	return state.read != nil || len(state.pending) != 0 || len(state.replay) != 0
}

// startRead replays consumed input or adopts an outstanding read before reading anew.
func (t *termIO) startRead(ctx context.Context) (*inputRead, func()) {
	state := t.inputState()
	inputStates.Lock()
	defer inputStates.Unlock()
	// Retain file identity to prevent competing reads after raw mode ends.
	if state.source == nil {
		if _, file := t.in.(*os.File); file {
			state.source = t.in
		}
	}
	if len(state.replay) != 0 {
		return t.startReplay(state)
	}
	if state.read != nil {
		read := state.read
		return read, func() { t.acceptRead(state, read) }
	}
	pending, pendingOrder := state.pending, state.pendingOrder
	if len(pending) == 0 {
		pending, pendingOrder = t.pending, t.pendingOrder
	}
	read := &inputRead{done: make(chan struct{})}
	state.read = read
	if state.source != nil {
		inputStates.states[state.source] = state
	}
	// The read owns its decoder even after its original prompt returns.
	decoder := &termIO{
		in: t.in, input: state, pending: pending, pendingOrder: pendingOrder, splitKeys: t.splitKeys,
	}
	readCtx := context.WithoutCancel(ctx)
	go func() {
		defer close(read.done)
		event, n := decoder.readEvent(readCtx)
		read.event = event
		err := event.err
		read.pending = decoder.pending
		read.pendingOrder = decoder.pendingOrder
		// Release empty failed reads even if the cancelled prompt never accepts them.
		if n == 0 && err != nil && len(read.pending) == 0 {
			inputStates.Lock()
			if len(state.replay) == 0 && inputStates.states[state.source] == state {
				delete(inputStates.states, state.source)
			}
			inputStates.Unlock()
		}
	}()
	return read, func() { t.acceptRead(state, read) }
}

// startReplay hands over the oldest retained event, consuming it only on
// acceptance. Its pending bytes are the caller's own, so delivery keeps them.
// Requires inputStates held.
func (t *termIO) startReplay(state *inputState) (*inputRead, func()) {
	read := &inputRead{
		done:         make(chan struct{}),
		event:        state.replay[0],
		pending:      t.pending,
		pendingOrder: t.pendingOrder,
	}
	close(read.done)
	return read, func() {
		inputStates.Lock()
		defer inputStates.Unlock()
		state.replay = state.replay[1:]
		if state.source != nil && state.read == nil && len(state.pending) == 0 && len(state.replay) == 0 &&
			inputStates.states[state.source] == state {
			delete(inputStates.states, state.source)
		}
	}
}

// acceptRead retains decoder bytes and releases idle registry entries.
func (t *termIO) acceptRead(state *inputState, read *inputRead) {
	inputStates.Lock()
	defer inputStates.Unlock()
	if state.read != read {
		return
	}
	state.read = nil
	state.pending = read.pending
	state.pendingOrder = read.pendingOrder
	if state.source != nil && len(state.pending) == 0 && len(state.replay) == 0 &&
		inputStates.states[state.source] == state {
		delete(inputStates.states, state.source)
	}
}

// readEvent keeps paste framing with the retained read so partial payloads cannot become keys.
func (t *termIO) readEvent(ctx context.Context) (event keyEvent, size int) {
	defer func() { event.readOrder = t.eventOrder }()
	buf, n, err := t.readRuneBytes(ctx)
	for err == nil {
		// Terminal replies are out-of-band: route them and keep waiting for a
		// key, so they neither become input nor end this read.
		f := t.scanControlFrame(buf[:n])
		if !f.isComplete {
			break
		}
		t.pushPending(buf, f.size, n)
		t.inputState().routeControl(f.reply)
		buf, n, err = t.readRuneBytes(ctx)
	}
	if err != nil {
		return keyEvent{err: err}, n
	}
	if bytes.HasPrefix(buf[:n], []byte("\x1b[200~")) {
		t.pushPending(buf, 6, n)
		payload, err := t.readBracketedPayload()
		if err != nil {
			return keyEvent{err: err}, 0
		}
		return keyEvent{paste: payload, bracketed: true}, len(payload) + 12
	}
	key, n, err := t.decodeRuneBytes(buf, n)
	event = keyEvent{key: key, err: err}
	var more *pasteTextError
	if errors.As(err, &more) {
		event.paste = append([]byte(nil), more.buf[:n]...)
	}
	return event, n
}

// replayKeys retains a coalesced event's arrival order and post-Enter bytes.
func (t *termIO) replayKeys(event keyEvent) {
	t.awaitReader()
	state := t.inputState()
	inputStates.Lock()
	defer inputStates.Unlock()
	state.pending = append(append([]byte(nil), event.paste...), state.pending...)
	state.pendingOrder = event.readOrder
	t.pending, t.pendingOrder = state.pending, state.pendingOrder
	if state.source != nil {
		inputStates.states[state.source] = state
	}
}

// restoreKeys puts events read while probing back in front of the unread input,
// keeping their boundaries and arrival orders.
func (t *termIO) restoreKeys(events []keyEvent) {
	if len(events) == 0 {
		return
	}
	state := t.inputState()
	inputStates.Lock()
	defer inputStates.Unlock()
	state.replay = append(events, state.replay...)
	if state.source != nil {
		inputStates.states[state.source] = state
	}
}
