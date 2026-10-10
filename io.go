// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"golang.org/x/term"
)

// tio owns package-default terminal IO and lazily upgrades stderr to a shared
// arbiter when the configured output is an interactive TTY.
type tio struct {
	reader   io.Reader
	writer   io.Writer
	external io.Writer
	mu       sync.RWMutex
	arbiter  *chanIO
	cancel   context.CancelCauseFunc
}

type terminalStderr struct {
	parent *tio
}

func (w *terminalStderr) Write(p []byte) (n int, err error) {
	if w == nil || w.parent == nil {
		return 0, io.ErrClosedPipe
	}
	return w.parent.writeExternal(p)
}

// Close flushes and shuts down the terminal arbiter by delegating to the parent tio.
func (w *terminalStderr) Close() error {
	if w == nil || w.parent == nil {
		return nil
	}
	return w.parent.Close()
}

// Sync flushes all buffered terminal output to the underlying writer without closing.
// Satisfies the same interface as os.File.Sync so callers can drain TUI
// output without closing the descriptor.
func (w *terminalStderr) Sync() error {
	if w == nil || w.parent == nil {
		return nil
	}
	return w.parent.Flush()
}

// Fd returns the file descriptor of
// the underlying terminal writer.
func (w *terminalStderr) Fd() uintptr {
	if w == nil || w.parent == nil {
		return 0
	}
	fdw, ok := w.parent.rawOutput().(descriptor)
	if !ok {
		return 0
	}
	return fdw.Fd()
}

var defaultIO = &tio{
	reader: os.Stdin,
	writer: os.Stderr,
}

// Stderr returns the package-default append-only terminal writer.
//
// On interactive TTYs this routes writes through go-tui's shared terminal
// arbiter so prompts and external output are ordered by one renderer. On
// non-TTY outputs it returns the configured raw writer directly.
func Stderr() io.Writer {
	return defaultIO.stderrWriter()
}

// SetDefaultIO overrides package-wide default input/output used by widgets that
// were not explicitly configured with [WithInput] or [WithOutput].
//
// Deprecated: prefer the package defaults plus [Stderr] for normal CLI + TUI
// integration. This remains for tests and explicit advanced overrides.
func SetDefaultIO(in io.Reader, out io.Writer) {
	defaultIO.setIO(in, out)
}

// defaultStreams returns the default reader and writer as one snapshot, so a
// concurrent [SetDefaultIO] cannot pair the old reader with the new writer.
func defaultStreams() (io.Reader, io.Writer) {
	return defaultIO.streams()
}

// Flush drains all buffered terminal output without shutting down the arbiter.
func (t *tio) Flush() error {
	t.mu.RLock()
	arbiter := t.arbiter
	t.mu.RUnlock()
	if arbiter == nil {
		return nil
	}
	return arbiter.Flush()
}

// Close cancels the terminal arbiter and blocks until forwardTo has drained
// all buffered output to the underlying writer. Safe to call more than once.
func (t *tio) Close() error {
	t.mu.Lock()
	arbiter := t.arbiter
	cancel := t.cancel
	t.mu.Unlock()
	if cancel != nil {
		cancel(nil)
	}
	if arbiter == nil {
		return nil
	}
	<-arbiter.done
	return arbiter.err
}

func (t *tio) input() io.Reader {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.reader
}

func (t *tio) rawOutput() io.Writer {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.writer
}

// streams takes the exclusive lock because [tio.outputLocked] may start the arbiter.
func (t *tio) streams() (io.Reader, io.Writer) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.reader, t.outputLocked()
}

// outputLocked resolves the effective writer,
// upgrading to arbiter for TTY outputs.
func (t *tio) outputLocked() io.Writer {
	if cio, ok := t.writer.(*chanIO); ok {
		return cio
	}
	if !t.isTerminalWriter(t.writer) {
		return t.writer
	}
	cio := t.ensureArbiterLocked()
	if cio == nil {
		return t.writer
	}
	return cio
}

// stderrWriter returns a writer for external
// stderr, routed through the arbiter on TTYs.
func (t *tio) stderrWriter() io.Writer {
	t.mu.Lock()
	defer t.mu.Unlock()
	cio, ok := t.writer.(*chanIO)
	if ok {
		return cio
	}
	if !t.isTerminalWriter(t.writer) {
		return t.writer
	}
	cio = t.ensureArbiterLocked()
	if cio == nil {
		return t.writer
	}
	if t.external == nil {
		t.external = &terminalStderr{parent: t}
	}
	return t.external
}

// writeExternal sends output through the arbiter or
// falls back to the raw writer.
func (t *tio) writeExternal(p []byte) (int, error) {
	t.mu.Lock()
	cio := t.ensureArbiterLocked()
	raw := t.writer
	t.mu.Unlock()
	if cio != nil {
		return cio.Write(p)
	}
	if raw == nil {
		return 0, io.ErrClosedPipe
	}
	return raw.Write(p)
}

// setIO replaces the default reader and/or writer,
// resetting the arbiter on writer change.
func (t *tio) setIO(in io.Reader, out io.Writer) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if in != nil {
		t.reader = in
	}
	if out == nil {
		return
	}
	t.writer = t.unwrapWriterLocked(out)
	t.resetArbiterLocked()
}

// unwrapWriterLocked resolves self-referential writers
// to avoid circular write chains.
func (t *tio) unwrapWriterLocked(out io.Writer) io.Writer {
	switch w := out.(type) {
	case *terminalStderr:
		if w.parent == t {
			return t.writer
		}
		return w.parent.rawOutput()
	case *chanIO:
		if w == t.arbiter {
			return t.writer
		}
	}
	return out
}

// resetArbiterLocked tears down
// the arbiter goroutine and clears
// related state.
func (t *tio) resetArbiterLocked() {
	if t.cancel != nil {
		t.cancel(nil)
	}
	t.cancel = nil
	t.arbiter = nil
	t.external = nil
}

// ensureArbiterLocked lazily creates
// the channel-based IO arbiter for TTY outputs.
func (t *tio) ensureArbiterLocked() *chanIO {
	if cio, ok := t.writer.(*chanIO); ok {
		return cio
	}
	if t.arbiter != nil {
		return t.arbiter
	}
	if !t.isTerminalWriter(t.writer) {
		return nil
	}
	ctx, cancel := context.WithCancelCause(context.Background())
	cio, err := startIO(ctx, t.writer)
	if err != nil {
		cancel(err)
		return nil
	}
	t.arbiter = cio
	t.cancel = cancel
	return cio
}

var termGetSize = term.GetSize
var terminalWriterChecker = func(fd int) bool {
	return term.IsTerminal(fd)
}

func (*tio) isTerminalWriter(w io.Writer) bool {
	fdw, ok := w.(descriptor)
	if !ok {
		return false
	}
	return terminalWriterChecker(int(fdw.Fd()))
}

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
		return nil, fmt.Errorf("get size: %w", err)
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
	done   chan struct{}   // closed by forwardTo after draining all pending output
	syncCh chan chan error // used by Flush to synchronize with forwardTo
	err    error           // first output failure, readable once done is closed

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
}

// Deprecated: use [Stderr].
func NewIO(ctx context.Context) (*chanIO, error) {
	return startIO(ctx, os.Stderr)
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

// failWriter records the first error of the underlying terminal writer.
type failWriter struct {
	w   io.Writer
	err error
}

func (f *failWriter) Write(p []byte) (int, error) {
	n, err := f.w.Write(p)
	if err != nil && f.err == nil {
		f.err = err
	}
	return n, err
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

type externalNewlineState struct {
	prevCR bool
}

// normalize rewrites lone LF bytes to CRLF for raw terminal output.
func (s *externalNewlineState) normalize(p []byte) []byte {
	if len(p) == 0 {
		return nil
	}
	out := make([]byte, 0, len(p))
	for _, b := range p {
		if b == '\n' && !s.prevCR {
			out = append(out, '\r')
		}
		out = append(out, b)
		s.prevCR = b == '\r'
	}
	return out
}

func (s *externalNewlineState) normalizeString(x string) []byte {
	return s.normalize([]byte(x))
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

// clearManaged emits ANSI escape sequences to erase
// previously rendered managed lines.
func (i *chanIO) clearManaged(buf *bytes.Buffer) {
	width, _ := i.size()
	lines := i.wrappedRows(i.rendered, width)
	if lines <= 0 {
		return
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

// writeManaged renders all managed viewports into
// the writer within the terminal height budget.
func (i *chanIO) writeManaged(w io.Writer) (int, error) {
	i.chainMu.Lock()
	defer i.chainMu.Unlock()
	i.rendered = i.rendered[:0]
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
		budget -= res.lines
		if budget <= 0 {
			break
		}
		curr, height = curr.next, budget
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

func newWriteC(ctx context.Context) *writeC {
	return &writeC{
		Context: ctx,
		C:       make(chan string, 128),
	}
}

type writeC struct {
	context.Context
	C chan string
}

func (x *writeC) Write(p []byte) (n int, err error) {
	select {
	case <-x.Done():
		return 0, io.EOF
	case x.C <- string(p):
		return len(p), nil
	}
}

// maxPollWait is how long a single readiness check blocks before
// [waitForReadableInput] looks at its context again.
const maxPollWait = 50 * time.Millisecond

// pollWait returns how long the next readiness check may block:
// [maxPollWait], or less when the context deadline comes sooner.
func pollWait(ctx context.Context) time.Duration {
	deadline, ok := ctx.Deadline()
	if !ok {
		return maxPollWait
	}
	return min(maxPollWait, max(0, time.Until(deadline)))
}
