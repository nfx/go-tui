// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"sync"
	"time"
)

// startIO boots the arbiter goroutines that multiplex viewport
// and external writes.
func startIO(ctx context.Context, out io.Writer) (*chanIO, error) {
	fdw, ok := out.(descriptor)
	if !ok {
		return nil, fmt.Errorf("stderr: %w", ErrNoTTY)
	}
	fd := int(fdw.Fd())
	w, h, err := termGetSize(fd)
	if err != nil {
		return nil, fmt.Errorf("get size: %w: %w", ErrNoTTY, err)
	}
	cio := newUnstartedIO(ctx, w, h, fd)
	go cio.handleViewports(ctx)
	go cio.forwardTo(ctx, out)
	return cio, nil
}

// newUnstartedIO allocates a chanIO with an initial viewport but
// no render goroutines.
func newUnstartedIO(ctx context.Context, width, height, fd int) *chanIO {
	cio := &chanIO{
		ctx:    ctx,
		In:     make(chan string),
		Out:    make(chan string, 1024),
		done:   make(chan struct{}),
		syncCh: make(chan chan error),
		ctl:    make(chan controlWrite),
		vreply: make(chan chan *viewport),
		notify: make(chan viewportChanged, 1024), // buffered to avoid blocking
		width:  width,
		height: height,
		fd:     fd,
	}
	cio.head = initViewport(ctx, cio.notify, width, height)
	cio.tail = cio.head
	return cio
}

// implements [io.ReadWriter].
type chanIO struct {
	In     chan string
	Out    chan string
	done   chan struct{}     // closed by forwardTo after draining all pending output
	syncCh chan chan error   // used by Flush to synchronize with forwardTo
	ctl    chan controlWrite // verbatim terminal requests, see [chanIO.writeControl]
	err    error             // first output failure, readable once done is closed

	ctx context.Context

	// readMu guards unread, the rest of an In message that did not fit into Read.
	readMu sync.Mutex
	unread []byte

	// sizeMu guards width and height: widget render loops and the arbiter
	// goroutines all refresh and read the shared terminal geometry.
	sizeMu        sync.Mutex
	width, height int

	chainMu    sync.Mutex
	head, tail *viewport
	vreply     chan chan *viewport
	notify     chan viewportChanged
	fd         int // terminal fd for resize refresh, 0 if unavailable
	rendered   []int
	// cursorUp is the number of rows above the last rendered line that the
	// cursor was left on, e.g. by an input that shows a message below its caret.
	cursorUp int
}

// Flush blocks until all output buffered in Out has been written to the
// terminal, without shutting down the arbiter. It returns the first error
// the terminal writer reported; output that failed is discarded, not retried.
func (i *chanIO) Flush() error {
	reply := make(chan error, 1)
	select {
	case <-i.ctx.Done():
		return i.ctx.Err()
	case i.syncCh <- reply:
	}
	select {
	case <-i.ctx.Done():
		return i.ctx.Err()
	case err := <-reply:
		return err
	}
}

// Read returns bytes left over from the previous input string, or blocks
// until the next one arrives on the In channel. Bytes that do not fit into
// p are kept for the next call.
func (i *chanIO) Read(p []byte) (n int, err error) {
	if len(p) == 0 {
		return 0, nil
	}
	i.readMu.Lock()
	defer i.readMu.Unlock()
	if len(i.unread) == 0 {
		select {
		case <-i.ctx.Done():
			return 0, io.EOF
		case res, ok := <-i.In:
			if !ok {
				return 0, io.EOF
			}
			i.unread = []byte(res)
		}
	}
	n = copy(p, i.unread)
	i.unread = i.unread[n:]
	return n, nil
}

// Write sends output bytes to the Out channel for
// the render loop to flush.
func (i *chanIO) Write(p []byte) (n int, err error) {
	select { // don't send on a closed channel
	case <-i.ctx.Done():
		return 0, io.EOF
	default:
	}
	select {
	case <-i.ctx.Done():
		return 0, io.EOF
	case i.Out <- string(p):
		return len(p), nil
	}
}

// flushOut non-blockingly drains all currently buffered Out messages.
func (i *chanIO) flushOut(w io.Writer, prevH int, pending *bytes.Buffer, nl *externalNewlineState) int {
	for {
		select {
		case chunk, ok := <-i.Out:
			if !ok {
				return prevH
			}
			prevH = i.handleExternalWrite(w, prevH, pending, nl, chunk)
		default:
			return prevH
		}
	}
}

// pushViewport requests a new managed viewport from
// the arbiter's handleViewports loop.
func (i *chanIO) pushViewport() (*viewport, error) {
	select {
	case <-i.ctx.Done():
		return nil, i.ctx.Err()
	default:
	}
	added := make(chan *viewport)
	defer close(added)
	select {
	case <-i.ctx.Done():
		return nil, i.ctx.Err()
	case i.vreply <- added:
		select {
		case <-i.ctx.Done():
			return nil, i.ctx.Err()
		default:
		}
		select {
		case <-i.ctx.Done():
			return nil, i.ctx.Err()
		case vp := <-added:
			return vp, nil
		}
	}
}

// refreshSize re-queries the terminal size for the shared arbiter.
func (i *chanIO) refreshSize() {
	if i.fd < 1 {
		return
	}
	w, h, err := termGetSize(i.fd)
	if err != nil {
		return
	}
	i.sizeMu.Lock()
	defer i.sizeMu.Unlock()
	i.width = w
	i.height = h
}

// size returns a consistent snapshot of the shared terminal geometry.
func (i *chanIO) size() (width, height int) {
	i.sizeMu.Lock()
	defer i.sizeMu.Unlock()
	return i.width, i.height
}

// handleViewports listens for viewport push requests
// and inserts them into the managed chain.
func (i *chanIO) handleViewports(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case reply := <-i.vreply:
			i.refreshSize()
			width, height := i.size()
			// each managed viewport stops with [chanIO.releaseViewport]
			vctx, cancel := context.WithCancel(ctx)
			vp := initViewport(vctx, i.notify, width, height)
			vp.cancel = cancel
			vp.fixedHeight = true
			i.insertManagedViewport(vp)
			select {
			case <-ctx.Done():
				return
			case reply <- vp:
			}
		}
	}
}

// insertManagedViewport keeps background overlays
// above later interactive prompts.
func (i *chanIO) insertManagedViewport(vp *viewport) {
	i.chainMu.Lock()
	defer i.chainMu.Unlock()
	if i.head == nil || !i.head.fixedHeight {
		vp.next = i.head
		i.head = vp
		if i.tail == nil {
			i.tail = vp
		}
		return
	}
	curr := i.head
	for curr.next != nil && curr.next.fixedHeight {
		curr = curr.next
	}
	vp.next = curr.next
	curr.next = vp
	if vp.next == nil {
		i.tail = vp
	}
}

// releaseViewport unlinks a managed viewport, moves its last frame into the
// terminal scrollback and stops its loop. Safe to call more than once.
func (i *chanIO) releaseViewport(vp *viewport) {
	vp.release.Do(func() {
		defer func() {
			if vp.cancel != nil {
				vp.cancel()
			}
		}()
		done := make(chan struct{})
		select {
		case <-i.ctx.Done():
			return
		case i.notify <- viewportChanged{release: vp, done: done}:
		}
		select {
		case <-i.ctx.Done():
		case <-done:
		}
	})
}

// unlinkLocked removes vp from the chain and reports whether it was linked.
func (i *chanIO) unlinkLocked(vp *viewport) bool {
	var prev *viewport
	for curr := i.head; curr != nil; prev, curr = curr, curr.next {
		if curr != vp {
			continue
		}
		if prev == nil {
			i.head = curr.next
		} else {
			prev.next = curr.next
		}
		if i.tail == curr {
			i.tail = prev
		}
		curr.next = nil
		return true
	}
	return false
}

// commitViewport unlinks a released viewport and renders its last frame
// into w as plain output, so it stays in the scrollback.
func (i *chanIO) commitViewport(vp *viewport, w io.Writer) {
	i.chainMu.Lock()
	defer i.chainMu.Unlock()
	if !i.unlinkLocked(vp) {
		return
	}
	_, _ = i.writeViewport(vp, w, 0) //nolint:errcheck // a stopped viewport has nothing to keep
}

// forwardTo is the main render loop that flushes overlay changes and external writes to the terminal.
// On context cancellation it drains any remaining Out messages before returning, then closes done.
// Output that fails to write is discarded; the first failure is reported by Flush and Close.
func (i *chanIO) forwardTo(ctx context.Context, out io.Writer) {
	w := &failWriter{w: out}
	defer close(i.done)
	defer func() { i.err = w.err }()
	var prevH int
	var pending bytes.Buffer
	var nl externalNewlineState
	for {
		select {
		case <-ctx.Done():
			i.drainTo(w, prevH, &pending, &nl)
			return
		case ev := <-i.notify:
			prevH = i.handleOverlayChange(w, prevH, &pending, &nl, ev.release)
			if ev.done != nil {
				close(ev.done)
			}
		case chunk := <-i.Out:
			prevH = i.handleExternalWrite(w, prevH, &pending, &nl, chunk)
		case req := <-i.ctl:
			// Reached only between other writes, and it prints nothing,
			// so the cursor and the managed rows stay where they were.
			_, err := w.Write(req.p)
			req.reply <- err
		case reply := <-i.syncCh:
			prevH = i.flushOut(w, prevH, &pending, &nl)
			if pending.Len() > 0 {
				_, _ = w.Write(nl.normalize(pending.Bytes())) //nolint:errcheck // recorded by failWriter
				pending.Reset()
			}
			reply <- w.err
		}
	}
}

// drainTo flushes any messages already buffered in Out after context cancellation,
// so that output written just before shutdown is not silently dropped.
func (i *chanIO) drainTo(w io.Writer, prevH int, pending *bytes.Buffer, nl *externalNewlineState) {
	for {
		select {
		case chunk, ok := <-i.Out:
			if !ok {
				return
			}
			i.handleExternalWrite(w, prevH, pending, nl, chunk)
		default:
			if pending.Len() > 0 {
				_, _ = w.Write(nl.normalize(pending.Bytes())) //nolint:errcheck // best-effort drain
			}
			return
		}
	}
}

// handleOverlayChange redraws managed viewports and flushes pending output when overlays clear.
// A released viewport is unlinked and its last frame kept above the remaining overlays.
func (i *chanIO) handleOverlayChange(
	w io.Writer,
	prevH int,
	pending *bytes.Buffer,
	nl *externalNewlineState,
	released *viewport,
) int {
	i.refreshSize()
	currH, err := i.redrawManaged(w, prevH, released)
	if err != nil {
		return prevH
	}
	if currH != 0 || pending.Len() == 0 {
		return currH
	}
	_, _ = w.Write(nl.normalize(pending.Bytes())) //nolint:errcheck // recorded by failWriter
	pending.Reset()
	return currH
}

// handleExternalWrite interleaves external output with managed viewport redraws.
func (i *chanIO) handleExternalWrite(
	w io.Writer,
	prevH int,
	pending *bytes.Buffer,
	nl *externalNewlineState,
	chunk string,
) int {
	if prevH == 0 {
		return i.flushWithoutOverlay(w, pending, nl, chunk, prevH)
	}
	i.refreshSize()
	pending.WriteString(chunk)
	flush, rest := i.splitCompletedLines(pending.Bytes())
	pending.Reset()
	_, _ = pending.Write(rest)

	var buf bytes.Buffer
	i.clearManaged(&buf)
	if len(flush) > 0 {
		_, _ = buf.Write(nl.normalize(flush))
	}
	currH, err := i.writeManaged(&buf)
	if err != nil {
		return prevH
	}
	if buf.Len() == 0 {
		return currH
	}
	if _, err = w.Write(buf.Bytes()); err != nil {
		return prevH
	}
	return currH
}

// flushWithoutOverlay writes external output directly when no managed viewports are active.
func (i *chanIO) flushWithoutOverlay(
	w io.Writer,
	pending *bytes.Buffer,
	nl *externalNewlineState,
	chunk string,
	prevH int,
) int {
	if pending.Len() > 0 {
		pending.WriteString(chunk)
		_, _ = w.Write(nl.normalize(pending.Bytes())) //nolint:errcheck // recorded by failWriter
		pending.Reset()
		return prevH
	}
	if _, err := w.Write(nl.normalizeString(chunk)); err != nil {
		return prevH
	}
	return prevH
}

// splitCompletedLines separates fully newline-terminated lines from
// a trailing partial line.
func (*chanIO) splitCompletedLines(p []byte) (flush []byte, rest []byte) {
	idx := bytes.LastIndexByte(p, '\n')
	if idx < 0 {
		return nil, append([]byte(nil), p...)
	}
	flush = append([]byte(nil), p[:idx+1]...)
	rest = append([]byte(nil), p[idx+1:]...)
	return flush, rest
}

// clearManaged erases the previous frame from its bottom row, accounting for
// wrapping and a caret left above the final line.
func (i *chanIO) clearManaged(buf *bytes.Buffer) {
	width, _ := i.size()
	lines := i.wrappedRows(i.rendered, width)
	if lines <= 0 {
		return
	}
	if up := i.cursorUp; up > 0 {
		// clearing walks up from the last line
		fmt.Fprintf(buf, "\x1b[%dB", up)
	}
	for i := range lines {
		fmt.Fprint(buf, "\r")
		fmt.Fprint(buf, "\x1b[K")
		if i < lines-1 {
			fmt.Fprint(buf, "\x1b[1A")
		}
	}
}

// redrawManaged clears old managed output, commits a released viewport if
// any, and rewrites all active managed viewports.
func (i *chanIO) redrawManaged(w io.Writer, prevH int, released *viewport) (int, error) {
	var buf bytes.Buffer
	i.clearManaged(&buf)
	if released != nil {
		i.commitViewport(released, &buf)
	}
	// render separately, so trimming the overlay's last newline keeps the committed one
	var managed bytes.Buffer
	lines, err := i.writeManaged(&managed)
	if err != nil {
		return prevH, err
	}
	_, _ = buf.Write(managed.Bytes())
	if buf.Len() == 0 {
		return lines, nil
	}
	_, err = w.Write(buf.Bytes())
	return lines, err
}

// writeManaged renders managed viewports within the terminal height budget
// and records row widths and the final caret offset for clearing.
func (i *chanIO) writeManaged(w io.Writer) (int, error) {
	i.chainMu.Lock()
	defer i.chainMu.Unlock()
	i.rendered = i.rendered[:0]
	i.cursorUp = 0
	if i.head == nil {
		return 0, nil
	}
	var totalLines int
	curr := i.head
	_, budget := i.size()
	height := 0 // the head keeps its own height
	for curr != nil {
		if !i.isManagedViewport(curr) {
			break
		}
		res, err := i.writeViewport(curr, w, height)
		if err != nil {
			return totalLines, err
		}
		totalLines += res.lines
		i.rendered = append(i.rendered, res.widths...)
		i.cursorUp = res.up
		budget -= res.lines
		if budget <= 0 {
			break
		}
		curr, height = curr.next, budget
		if res.up > 0 && curr != nil && i.isManagedViewport(curr) {
			// return from the caret row, so the next viewport starts below this one
			if _, err := fmt.Fprintf(w, "\x1b[%dB", res.up); err != nil {
				return totalLines, err
			}
			i.cursorUp = 0
		}
	}
	i.trimTrailingNewline(w)
	return totalLines, nil
}

func (i *chanIO) wrappedRows(widths []int, termWidth int) int {
	if len(widths) == 0 {
		return 0
	}
	if termWidth <= 0 {
		return len(widths)
	}
	rows := 0
	for _, lineWidth := range widths {
		if lineWidth <= 0 {
			rows++
			continue
		}
		rows += (lineWidth + termWidth - 1) / termWidth
	}
	return rows
}

func (i *chanIO) isManagedViewport(curr *viewport) bool {
	return curr == i.head || curr.fixedHeight
}

// trimTrailingNewline removes a trailing newline
// from the buffer to avoid extra blank lines.
func (*chanIO) trimTrailingNewline(w io.Writer) {
	buf, ok := w.(*bytes.Buffer)
	if !ok {
		return
	}
	x := buf.Bytes()
	if len(x) > 0 && x[len(x)-1] == '\n' {
		buf.Truncate(len(x) - 1)
	}
}

// writeViewport sends a writeTo request to a viewport and waits for its rendered response.
// A positive height becomes the viewport's height budget inside its own loop.
func (*chanIO) writeViewport(curr *viewport, w io.Writer, height int) (writeToResponse, error) {
	respond := make(chan writeToResponse)
	select {
	case <-curr.ctx.Done():
		return writeToResponse{}, io.EOF
	case curr.writeTos <- &writeTo{Writer: w, res: respond, height: height}:
	}
	select {
	case <-curr.ctx.Done():
		return writeToResponse{}, io.EOF
	case res := <-respond:
		close(respond)
		if res.err != nil {
			return writeToResponse{}, res.err
		}
		return res, nil
	}
}

// writeControl is serialized with all other terminal output by [chanIO.forwardTo].
func (i *chanIO) writeControl(ctx context.Context, p []byte) error {
	reply := make(chan error, 1)
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-i.ctx.Done():
		return i.ctx.Err()
	case i.ctl <- controlWrite{p: p, reply: reply}:
	}
	select {
	case <-i.ctx.Done():
		return i.ctx.Err()
	case err := <-reply:
		return err
	}
}

// maxPollWait is how long a single readiness check blocks before
// [waitForReadableInput] looks at its context again.
const maxPollWait = 50 * time.Millisecond

// pollWait returns how long the next readiness check may block:
// [maxPollWait], or less when the context deadline comes sooner.
func (t *termIO) pollWait(ctx context.Context) time.Duration {
	deadline, ok := ctx.Deadline()
	if !ok {
		return maxPollWait
	}
	return min(maxPollWait, max(0, time.Until(deadline)))
}
