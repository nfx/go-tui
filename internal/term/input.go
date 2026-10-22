// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package term

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

func NextInputOrder() uint64 {
	return inputOrder.Add(1)
}

// ReadEvents reads one event per permit; nil permits reads a single event.
// Cancellation retains an unconsumed read for the next prompt on this input.
func (t *IO) ReadEvents(ctx context.Context, permits <-chan struct{}) <-chan KeyEvent {
	// Finish publishing the previous key's pending bytes before starting a read.
	t.AwaitReader()
	ch := make(chan KeyEvent)
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
			if !t.hasInput() && len(t.Pending) == 0 {
				if err := t.waitForReadableInput(ctx, t.In); err != nil {
					select {
					case <-ctx.Done():
					case ch <- KeyEvent{Err: err}:
					}
					return
				}
			}
			if ctx.Err() != nil {
				return
			}
			read, consumed := t.StartRead(ctx)
			select {
			case <-ctx.Done():
				t.abandonPaste(read)
				return
			case <-read.Done:
			}
			event := t.shapeEvent(read)
			select {
			case <-ctx.Done():
				t.abandonPaste(read)
				return
			case ch <- event:
				t.Pending = read.pending
				t.pendingOrder = read.pendingOrder
				consumed()
			}
			if permits == nil || (read.Event.Err != nil && read.Event.Paste == nil) {
				return
			}
		}
	}()
	return ch
}

// shapeEvent adapts a completed read to the consumer's paste mode, dropping
// bracketed events whose opening bytes preceded cancellation.
func (t *IO) shapeEvent(read *inputRead) KeyEvent {
	InputStates.Lock()
	defer InputStates.Unlock()
	event := read.Event
	if event.Bracketed && event.ReadOrder < read.discardBefore {
		return KeyEvent{Key: KeyIgnored}
	}
	if event.Bracketed && !t.BracketedPaste {
		// Legacy consumers still receive their buffered-text event shape.
		event.Err = &PasteTextError{Buf: event.Paste}
	}
	return event
}

// AwaitReader waits until the last [termIO.readEvents] goroutine exits after its
// context is cancelled. It does not receive the key, so an unreceived key stays
// with the input for its next prompt.
func (t *IO) AwaitReader() {
	if t.reader != nil {
		<-t.reader
	}
}

type inputRead struct {
	Done          chan struct{}
	Event         KeyEvent
	discardBefore uint64
	pending       []byte
	pendingOrder  uint64
}

// inputState retains only input decoding state across sequential prompt runs.
// The registry contains states with an outstanding read or buffered bytes.
type inputState struct {
	source       any
	Read         *inputRead
	pending      []byte
	pendingOrder uint64
	// replay holds decoded events that precede the in-flight read.
	replay []KeyEvent
	// queries await terminal replies, guarded by inputStates.
	queries []*controlQuery
}

var InputStates = struct {
	sync.Mutex
	States map[any]*inputState
}{States: make(map[any]*inputState)}

// SharedInputState shares retained reads only through a comparable reader identity.
func SharedInputState(in io.Reader) *inputState {
	// Reader identity is never inferred from a descriptor number.
	if in == nil || !reflect.ValueOf(in).Comparable() {
		return &inputState{}
	}
	InputStates.Lock()
	defer InputStates.Unlock()
	if state := InputStates.States[in]; state != nil {
		return state
	}
	// Idle files remain local; outstanding reads retain this exact file object.
	if _, file := in.(*os.File); file {
		return &inputState{}
	}
	return &inputState{source: in}
}

func (t *IO) inputState() *inputState {
	if t.Input == nil {
		t.Input = SharedInputState(t.In)
	}
	return t.Input
}

func (t *IO) hasInput() bool {
	state := t.inputState()
	InputStates.Lock()
	defer InputStates.Unlock()
	return state.Read != nil || len(state.pending) != 0 || len(state.replay) != 0
}

// StartRead replays consumed input or adopts an outstanding read before reading anew.
func (t *IO) StartRead(ctx context.Context) (*inputRead, func()) {
	state := t.inputState()
	InputStates.Lock()
	defer InputStates.Unlock()
	// Retain file identity to prevent competing reads after raw mode ends.
	if state.source == nil {
		if _, file := t.In.(*os.File); file {
			state.source = t.In
		}
	}
	if len(state.replay) != 0 {
		return t.startReplay(state)
	}
	if state.Read != nil {
		read := state.Read
		return read, func() { t.acceptRead(state, read) }
	}
	pending, pendingOrder := state.pending, state.pendingOrder
	if len(pending) == 0 {
		pending, pendingOrder = t.Pending, t.pendingOrder
	}
	read := &inputRead{Done: make(chan struct{})}
	state.Read = read
	if state.source != nil {
		InputStates.States[state.source] = state
	}
	// The read owns its decoder even after its original prompt returns.
	decoder := &IO{
		In: t.In, Input: state, Pending: pending, pendingOrder: pendingOrder, SplitKeys: t.SplitKeys,
	}
	readCtx := context.WithoutCancel(ctx)
	go func() {
		defer close(read.Done)
		event, n := decoder.readEvent(readCtx)
		read.Event = event
		err := event.Err
		read.pending = decoder.Pending
		read.pendingOrder = decoder.pendingOrder
		// Release empty failed reads even if the cancelled prompt never accepts them.
		if n == 0 && err != nil && len(read.pending) == 0 {
			InputStates.Lock()
			if len(state.replay) == 0 && InputStates.States[state.source] == state {
				delete(InputStates.States, state.source)
			}
			InputStates.Unlock()
		}
	}()
	return read, func() { t.acceptRead(state, read) }
}

// startReplay hands over the oldest retained event, consuming it only on
// acceptance. Its pending bytes are the caller's own, so delivery keeps them.
// Requires inputStates held.
func (t *IO) startReplay(state *inputState) (*inputRead, func()) {
	read := &inputRead{
		Done:         make(chan struct{}),
		Event:        state.replay[0],
		pending:      t.Pending,
		pendingOrder: t.pendingOrder,
	}
	close(read.Done)
	return read, func() {
		InputStates.Lock()
		defer InputStates.Unlock()
		state.replay = state.replay[1:]
		if state.source != nil && state.Read == nil && len(state.pending) == 0 && len(state.replay) == 0 &&
			InputStates.States[state.source] == state {
			delete(InputStates.States, state.source)
		}
	}
}

// acceptRead retains decoder bytes and releases idle registry entries.
func (t *IO) acceptRead(state *inputState, read *inputRead) {
	InputStates.Lock()
	defer InputStates.Unlock()
	if state.Read != read {
		return
	}
	state.Read = nil
	state.pending = read.pending
	state.pendingOrder = read.pendingOrder
	if state.source != nil && len(state.pending) == 0 && len(state.replay) == 0 &&
		InputStates.States[state.source] == state {
		delete(InputStates.States, state.source)
	}
}

// readEvent keeps paste framing with the retained read so partial payloads cannot become keys.
func (t *IO) readEvent(ctx context.Context) (event KeyEvent, size int) {
	defer func() { event.ReadOrder = t.eventOrder }()
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
		return KeyEvent{Err: err}, n
	}
	if bytes.HasPrefix(buf[:n], []byte("\x1b[200~")) {
		t.pushPending(buf, 6, n)
		payload, err := t.readBracketedPayload()
		if err != nil {
			return KeyEvent{Err: err}, 0
		}
		return KeyEvent{Paste: payload, Bracketed: true}, len(payload) + 12
	}
	key, n, err := t.decodeRuneBytes(buf, n)
	event = KeyEvent{Key: key, Err: err}
	var more *PasteTextError
	if errors.As(err, &more) {
		event.Paste = append([]byte(nil), more.Buf[:n]...)
	}
	return event, n
}

// ReplayKeys retains a coalesced event's arrival order and post-Enter bytes.
func (t *IO) ReplayKeys(event KeyEvent) {
	t.AwaitReader()
	state := t.inputState()
	InputStates.Lock()
	defer InputStates.Unlock()
	state.pending = append(append([]byte(nil), event.Paste...), state.pending...)
	state.pendingOrder = event.ReadOrder
	t.Pending, t.pendingOrder = state.pending, state.pendingOrder
	if state.source != nil {
		InputStates.States[state.source] = state
	}
}

// restoreKeys puts events read while probing back in front of the unread input,
// keeping their boundaries and arrival orders.
func (t *IO) restoreKeys(events []KeyEvent) {
	if len(events) == 0 {
		return
	}
	state := t.inputState()
	InputStates.Lock()
	defer InputStates.Unlock()
	state.replay = append(events, state.replay...)
	if state.source != nil {
		InputStates.States[state.source] = state
	}
}
